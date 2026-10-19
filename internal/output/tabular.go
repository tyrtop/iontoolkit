package output

import (
	"encoding/csv"
	"fmt"
	"io"
	"slices"
	"sort"
	"strconv"
	"strings"

	"tyrtop.com/iontk/internal/sase"
	"tyrtop.com/iontk/internal/ui"
)

// Orientation is a sort over the same rows, never a pivot.
const (
	OrientION     = "ion"
	OrientCommand = "command"
)

var orientations = []string{OrientION, OrientCommand}

// OrientationLabel names an orientation for a header or a status line, where
// "ion" alone does not say what it did to the rows.
func OrientationLabel(o string) string {
	if o == OrientCommand {
		return "command-first"
	}
	return "ion-first"
}

func OrientationHelp(o string) string {
	if o == OrientCommand {
		return "the same command across every element, adjacent"
	}
	return "each element's commands grouped together"
}

func NormalizeOrientation(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", OrientION, "element", "elements":
		return OrientION, nil
	case OrientCommand, "cmd", "commands":
		return OrientCommand, nil
	}
	return "", fmt.Errorf("unknown row orientation %q: want one of %s", s, strings.Join(orientations, ", "))
}

func NextOrientation(o string) string {
	for i, name := range orientations {
		if name == o {
			return orientations[(i+1)%len(orientations)]
		}
	}
	return OrientION
}

// tabularHeader is the column order for both tabular formats. Orientation
// sorts rows; it never moves a column, so one run stays diffable against
// another taken the other way round.
var tabularHeader = []string{"element", "element_id", "model", "software", "connected", "command", "output", "error"}

const (
	colCommand = 5
	colOutput  = 6
	colError   = 7
)

type tabularRow struct {
	// The order given rather than the order finished: elements complete
	// concurrently, and a run has to sort the same way every time.
	elem, cmd int
	cells     []string
}

func tabularRows(results []sase.Result) []tabularRow {
	var rows []tabularRow
	for i, r := range results {
		if r.ElementID == "" && r.Error == "" && len(r.Commands) == 0 {
			continue // a gap Add's index padding left for an element that never reported
		}
		meta := []string{r.Name, r.ElementID, r.Model, r.Software, strconv.FormatBool(r.Connected)}

		// An element that failed before any command ran still gets a row, or a
		// run where every dial failed would write nothing but a header.
		if len(r.Commands) == 0 {
			rows = append(rows, tabularRow{elem: i, cmd: -1, cells: append(slices.Clone(meta), "", "", r.Error)})
			continue
		}
		for j, c := range r.Commands {
			rows = append(rows, tabularRow{elem: i, cmd: j, cells: append(slices.Clone(meta), c.Command, c.Output, c.Error)})
		}
	}
	return rows
}

// An element that failed before any command has no command to group under, so
// it leads in command-first: a run's failures read better together than
// scattered between commands they never reached.
func orderRows(rows []tabularRow, orient string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if orient == OrientCommand {
			if a.cmd != b.cmd {
				return a.cmd < b.cmd
			}
			return a.elem < b.elem
		}
		if a.elem != b.elem {
			return a.elem < b.elem
		}
		return a.cmd < b.cmd
	})
}

type csvSink struct {
	w       io.Writer
	opts    sinkOpts
	results []sase.Result
}

func (s *csvSink) Add(index int, r sase.Result) error {
	s.results = addAt(s.results, index, r)
	return nil
}

func (s *csvSink) Close() error {
	rows := tabularRows(s.results)
	orderRows(rows, s.opts.orient)

	// csv.Writer quotes the newlines and commas in command output itself.
	// Pre-escaping any of it here would double it.
	cw := csv.NewWriter(s.w)
	if err := cw.Write(tabularHeader); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	for _, r := range rows {
		if err := cw.Write(r.cells); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}
	cw.Flush()
	return cw.Error()
}

type tableSink struct {
	w       io.Writer
	opts    sinkOpts
	results []sase.Result
}

func (s *tableSink) Add(index int, r sase.Result) error {
	s.results = addAt(s.results, index, r)
	return nil
}

func (s *tableSink) Close() error {
	rows := tabularRows(s.results)
	orderRows(rows, s.opts.orient)

	grid := make([][]string, 0, len(rows)+1)
	grid = append(grid, tabularHeader)
	for _, r := range rows {
		cells := slices.Clone(r.cells)
		// A table is one line per command. The full text is what json, jsonl
		// and text are for, and csv keeps it too.
		cells[colOutput] = flattenCell(cells[colOutput])
		cells[colError] = flattenCell(cells[colError])
		grid = append(grid, cells)
	}

	widths := columnWidths(grid, s.opts.width)
	var b strings.Builder
	for i, cells := range grid {
		b.WriteString(renderRow(cells, widths, s.opts.width) + "\n")
		if i == 0 {
			b.WriteString(renderRule(widths, s.opts.width) + "\n")
		}
	}
	_, err := io.WriteString(s.w, b.String())
	return err
}

// elasticMin is the floor the shrinkable columns stop at. Below it a cell says
// nothing at all, and the row is better cut by the line truncation than padded
// out with columns of one character each.
const elasticMin = 12

const colGap = "  "

func flattenCell(s string) string { return strings.Join(strings.Fields(s), " ") }

func columnWidths(grid [][]string, width int) []int {
	widths := make([]int, len(tabularHeader))
	for _, cells := range grid {
		for i, c := range cells {
			if n := ui.Width(c); n > widths[i] {
				widths[i] = n
			}
		}
	}
	dropEmptyColumns(grid, widths, width)
	fitWidths(widths, width)
	return widths
}

// Zero width is how the renderers are told to skip a column. A clean run's
// error column is the case that matters: five columns and a gap that the
// output was going to be read in. Nothing is dropped without a width to fit,
// where the full header is worth more than the space.
func dropEmptyColumns(grid [][]string, widths []int, width int) {
	if width <= 0 || len(grid) < 2 {
		return
	}
	for col := range widths {
		empty := true
		for _, cells := range grid[1:] {
			if cells[col] != "" {
				empty = false
				break
			}
		}
		if empty {
			widths[col] = 0
		}
	}
}

// Output gives up its width before error does, because json and csv still hold
// the full output while a cut-off error is all a failed row has. The
// identifying columns never shrink: a truncated 19-digit element ID reads as a
// different, valid ID.
func fitWidths(widths []int, width int) {
	if width <= 0 {
		return
	}
	over, kept := -width, 0
	for _, n := range widths {
		if n == 0 {
			continue
		}
		over += n
		kept++
	}
	over += len(colGap) * max(0, kept-1)
	for _, col := range []int{colOutput, colError} {
		if over <= 0 {
			return
		}
		give := min(over, max(0, widths[col]-elasticMin))
		widths[col] -= give
		over -= give
	}
}

func renderRow(cells []string, widths []int, width int) string {
	parts := make([]string, 0, len(cells))
	for i, c := range cells {
		if widths[i] == 0 {
			continue
		}
		parts = append(parts, pad(ui.Truncate(c, widths[i]), widths[i]))
	}
	return fitLine(strings.Join(parts, colGap), width)
}

func renderRule(widths []int, width int) string {
	parts := make([]string, 0, len(widths))
	for _, n := range widths {
		if n == 0 {
			continue
		}
		parts = append(parts, strings.Repeat("-", n))
	}
	return fitLine(strings.Join(parts, colGap), width)
}

// fitLine trims the padding off the last column before truncating, so a narrow
// terminal spends its columns on content rather than on trailing blanks.
func fitLine(line string, width int) string {
	line = strings.TrimRight(line, " ")
	if width > 0 {
		line = ui.Truncate(line, width)
	}
	return line
}

func pad(s string, n int) string {
	if gap := n - ui.Width(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}
