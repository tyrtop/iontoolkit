package output

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/sase/ioncli"
)

// Two elements running the same two commands, plus one that never connected:
// the only shape where the two orientations differ observably.
func fleet() []sase.Result {
	el := func(id, name string, outs ...string) sase.Result {
		return sase.Result{
			ElementID: id, Name: name, Model: "ion1200", Software: "6.5.1", Connected: true,
			Commands: []ioncli.CommandOutput{
				{Command: "dump device status", Output: outs[0]},
				{Command: "dump interface status", Output: outs[1]},
			},
		}
	}
	return []sase.Result{
		el("1000000000000000001", "s0001", "Serial     : A1\nUptime     : 4 days\n", "eth0 up, eth1 down"),
		el("1000000000000000002", "s0002", "Serial     : B2\nUptime     : 9 days\n", "eth0 down"),
		{ElementID: "1000000000000000003", Error: "dial: context deadline exceeded"},
	}
}

// fill adds the fleet out of order, the way elements actually finish.
func fill(sink Sink, t *testing.T) {
	t.Helper()
	rs := fleet()
	for _, i := range []int{1, 2, 0} {
		if err := sink.Add(i, rs[i]); err != nil {
			t.Fatalf("Add(%d) error = %v", i, err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func readCSV(t *testing.T, b []byte) [][]string {
	t.Helper()
	recs, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		t.Fatalf("the csv we wrote does not parse: %v\n%s", err, b)
	}
	return recs
}

func pairs(recs [][]string) []string {
	var out []string
	for _, r := range recs[1:] {
		out = append(out, r[0]+"/"+r[colCommand])
	}
	return out
}

func TestTabularOrientationOrdersRows(t *testing.T) {
	tests := []struct {
		orient string
		want   []string
	}{
		// ION-first groups an element's commands; command-first puts one
		// command across the fleet together, which is the comparison.
		{OrientION, []string{
			"s0001/dump device status", "s0001/dump interface status",
			"s0002/dump device status", "s0002/dump interface status",
			"/",
		}},
		{OrientCommand, []string{
			"/",
			"s0001/dump device status", "s0002/dump device status",
			"s0001/dump interface status", "s0002/dump interface status",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.orient, func(t *testing.T) {
			var buf bytes.Buffer
			fill(NewSink(&buf, FormatCSV, WithOrientation(tt.orient)), t)

			got := pairs(readCSV(t, buf.Bytes()))
			if strings.Join(got, "|") != strings.Join(tt.want, "|") {
				t.Errorf("row order = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTabularHeaderIsTheSameBothWays(t *testing.T) {
	var ion, cmd bytes.Buffer
	fill(NewSink(&ion, FormatCSV, WithOrientation(OrientION)), t)
	fill(NewSink(&cmd, FormatCSV, WithOrientation(OrientCommand)), t)

	a, b := readCSV(t, ion.Bytes())[0], readCSV(t, cmd.Bytes())[0]
	if strings.Join(a, ",") != strings.Join(b, ",") {
		t.Errorf("headers differ by orientation: %q vs %q — a sort must not move a column", a, b)
	}
	if len(a) != len(tabularHeader) {
		t.Errorf("header has %d columns, want %d", len(a), len(tabularHeader))
	}
}

// The columns the code indexes by number have to stay where it thinks they
// are; inserting one above them would silently shift every row.
func TestTabularColumnIndexesMatchHeader(t *testing.T) {
	for col, want := range map[int]string{colCommand: "command", colOutput: "output", colError: "error"} {
		if got := tabularHeader[col]; got != want {
			t.Errorf("tabularHeader[%d] = %q, want %q", col, got, want)
		}
	}
}

func TestCSVKeepsMultilineAndCommaOutputIntact(t *testing.T) {
	var buf bytes.Buffer
	fill(NewSink(&buf, FormatCSV, WithOrientation(OrientION)), t)
	recs := readCSV(t, buf.Bytes())

	want := fleet()[0].Commands[0].Output
	if got := recs[1][colOutput]; got != want {
		t.Errorf("output cell = %q, want %q — the newlines must survive the round trip", got, want)
	}
	if got := recs[2][colOutput]; got != "eth0 up, eth1 down" {
		t.Errorf("output cell = %q, want the comma to stay inside one field", got)
	}
	// Quoted newlines make the file longer than its record count. Escaping
	// them to \n instead would parse and still be the wrong format.
	if lines := strings.Count(strings.TrimRight(buf.String(), "\n"), "\n") + 1; lines <= len(recs) {
		t.Errorf("csv has %d lines for %d records, want the quoted output to span lines", lines, len(recs))
	}
}

func TestCSVCarriesAnElementThatNeverConnected(t *testing.T) {
	var buf bytes.Buffer
	fill(NewSink(&buf, FormatCSV, WithOrientation(OrientION)), t)
	recs := readCSV(t, buf.Bytes())

	last := recs[len(recs)-1]
	if last[1] != "1000000000000000003" {
		t.Fatalf("last row is %q, want the failed element to have one", last)
	}
	if last[colError] != "dial: context deadline exceeded" {
		t.Errorf("error cell = %q, want the dial failure", last[colError])
	}
	if last[colCommand] != "" || last[colOutput] != "" {
		t.Errorf("failed element reported command %q output %q, want both empty", last[colCommand], last[colOutput])
	}
}

func TestEmptyRunWritesAHeaderAndNothingElse(t *testing.T) {
	for _, format := range []string{FormatCSV, FormatTable} {
		t.Run(format, func(t *testing.T) {
			var buf bytes.Buffer
			sink := NewSink(&buf, format)
			if err := sink.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
			first := strings.SplitN(buf.String(), "\n", 2)[0]
			if !strings.Contains(first, "element") {
				t.Errorf("first line = %q, want the header", first)
			}
			if strings.Contains(buf.String(), "dump") {
				t.Errorf("empty run wrote rows:\n%s", buf.String())
			}
		})
	}
}

// Padding over an element that never reported must not become a blank row.
func TestTabularSkipsUnreportedElements(t *testing.T) {
	var buf bytes.Buffer
	sink := NewSink(&buf, FormatCSV)
	if err := sink.Add(3, fleet()[0]); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if recs := readCSV(t, buf.Bytes()); len(recs) != 3 {
		t.Errorf("wrote %d records, want a header and the two commands of the one element that ran", len(recs))
	}
}

func TestTableFlattensOutputToOneLinePerCommand(t *testing.T) {
	var buf bytes.Buffer
	fill(NewSink(&buf, FormatTable, WithOrientation(OrientION)), t)

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	// header, rule, two commands each for two elements, one failure
	if len(lines) != 7 {
		t.Fatalf("table has %d lines, want 7:\n%s", len(lines), buf.String())
	}
	if !strings.Contains(lines[2], "Serial : A1 Uptime : 4 days") {
		t.Errorf("row = %q, want the multiline output collapsed onto it", lines[2])
	}
}

func TestTableFitsTheGivenWidth(t *testing.T) {
	const width = 60
	var buf bytes.Buffer
	fill(NewSink(&buf, FormatTable, WithWidth(width)), t)

	for i, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if n := len([]rune(line)); n > width {
			t.Errorf("line %d is %d columns wide, want at most %d: %q", i, n, width, line)
		}
	}
}

// A file holds the only copy of what it is given, so nothing is capped there.
func TestTableWithoutAWidthKeepsWholeCells(t *testing.T) {
	long := strings.Repeat("x", 200)
	var buf bytes.Buffer
	sink := NewSink(&buf, FormatTable)
	if err := sink.Add(0, sase.Result{
		ElementID: "1", Name: "s0001",
		Commands: []ioncli.CommandOutput{{Command: "show", Output: long}},
	}); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !strings.Contains(buf.String(), long) {
		t.Errorf("table truncated a cell with no width set:\n%s", buf.String())
	}
}

func TestNormalizeOrientation(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", OrientION, false},
		{"ion", OrientION, false},
		{"ION", OrientION, false},
		{" element ", OrientION, false},
		{"command", OrientCommand, false},
		{"cmd", OrientCommand, false},
		{"commands", OrientCommand, false},
		{"sideways", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := NormalizeOrientation(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("NormalizeOrientation(%q) error = nil, want an error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("NormalizeOrientation(%q) error = %v", tt.in, err)
			}
			if got != tt.want {
				t.Errorf("NormalizeOrientation(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNextOrientationCyclesThroughEveryOrientation(t *testing.T) {
	seen := map[string]bool{}
	o := OrientION
	for range orientations {
		seen[o] = true
		o = NextOrientation(o)
	}
	if o != OrientION {
		t.Errorf("cycling returned to %q, want to wrap around to %q", o, OrientION)
	}
	if len(seen) != len(orientations) {
		t.Errorf("cycle visited %d orientations, want all %d", len(seen), len(orientations))
	}
	if got := NextOrientation("sideways"); got != OrientION {
		t.Errorf("NextOrientation(%q) = %q, want the fallback %q", "sideways", got, OrientION)
	}
}

// An unknown orientation reaching a sink must not silently sort by nothing.
func TestNewSinkFallsBackToIONFirst(t *testing.T) {
	var buf bytes.Buffer
	fill(NewSink(&buf, FormatCSV, WithOrientation("sideways")), t)

	got := pairs(readCSV(t, buf.Bytes()))
	if got[0] != "s0001/dump device status" {
		t.Errorf("first row = %q, want ION-first ordering", got[0])
	}
}

func TestTableShrinksOutputBeforeError(t *testing.T) {
	var buf bytes.Buffer
	fill(NewSink(&buf, FormatTable, WithWidth(130)), t)

	if !strings.Contains(buf.String(), "dial: context deadline exceeded") {
		t.Errorf("the error cell was truncated while output still had columns:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "Serial : A1 Uptime : 4 days") {
		t.Errorf("output was left whole, so nothing gave up the width:\n%s", buf.String())
	}
}

func TestTableDropsAnEmptyColumnToFitAWidth(t *testing.T) {
	clean := fleet()[:2]
	add := func(sink Sink) {
		for i, r := range clean {
			if err := sink.Add(i, r); err != nil {
				t.Fatalf("Add(%d) error = %v", i, err)
			}
		}
		if err := sink.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}

	var fitted bytes.Buffer
	add(NewSink(&fitted, FormatTable, WithWidth(110)))
	if strings.Contains(fitted.String(), "error") {
		t.Errorf("the error column survived a width with nothing in it:\n%s", fitted.String())
	}

	// The header is worth more than the space when there is no width to fit.
	var whole bytes.Buffer
	add(NewSink(&whole, FormatTable))
	if !strings.Contains(whole.String(), "error") {
		t.Errorf("the error column was dropped from a file:\n%s", whole.String())
	}

	// And it stays as soon as one row needs it.
	var failed bytes.Buffer
	fill(NewSink(&failed, FormatTable, WithWidth(110)), t)
	if !strings.Contains(failed.String(), "error") {
		t.Errorf("the error column was dropped while a row had an error:\n%s", failed.String())
	}
}
