package tutorial

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheShippedPlaceholdersWalkEveryPlaceAPageCanGo(t *testing.T) {
	s, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin() error = %v", err)
	}

	run := s.Shown()
	if len(run) < 2 {
		t.Fatalf("the shipped tutorial runs %d pages", len(run))
	}
	if !run[0].Box || !run[len(run)-1].Box {
		t.Error("the first and last page should be boxes")
	}

	reached := map[string]bool{}
	for _, p := range run {
		reached[p.Go] = true
	}
	for _, name := range Places {
		if !reached[name] {
			t.Errorf("no placeholder page goes to %s, so that place cannot be seen", name)
		}
	}

	var locked, read bool
	for _, p := range run {
		if len(p.Keys) > 0 && p.Stop != "" {
			locked = true
		}
		if len(p.Keys) == 0 && !p.Box {
			read = true
		}
	}
	if !locked {
		t.Error("no placeholder page names its keys and its stop key, so neither is demonstrated")
	}
	if !read {
		t.Error("no placeholder page is one the tutorial takes the stop key on")
	}

	for _, p := range s.Pages {
		if !p.Visible() {
			return
		}
	}
	t.Error("no page is show: no, so the switch is never demonstrated")
}

func TestAPageIsReadAsWritten(t *testing.T) {
	s, err := Parse([]byte(`
pages:
  - name: welcome
    box: yes
    text:
      - iontk talks to ION appliances.
      - Nothing here touches a real one.
  - name: open-the-list
    text:
      - Press enter to open the element list.
    keys: [enter]
`), "test")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(s.Pages) != 2 {
		t.Fatalf("got %d pages, want 2", len(s.Pages))
	}

	first := s.Pages[0]
	if !first.Box || len(first.Text) != 2 || len(first.Keys) != 0 {
		t.Errorf("first page = %+v, want a two-line modal that names no keys", first)
	}
	if got := s.Pages[1].StopKey(); got != "enter" {
		t.Errorf("stop = %q, want a page with no stop: of its own to end on enter", got)
	}
	if !first.Visible() {
		t.Error("a page with no show: should be visible")
	}
}

func TestShowNoHidesAPageWithoutDeletingIt(t *testing.T) {
	s, err := Parse([]byte(`
pages:
  - name: first
    box: yes
    text: [one]
  - name: skipped
    show: no
    text: [two]
    keys: [/]
  - name: last
    box: yes
    text: [three]
`), "test")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if len(s.Pages) != 3 {
		t.Fatalf("Parse kept %d pages, want all three", len(s.Pages))
	}
	shown := s.Shown()
	if len(shown) != 2 {
		t.Fatalf("Shown() = %d pages, want the two that are not hidden", len(shown))
	}
	for _, p := range shown {
		if p.Name == "skipped" {
			t.Error("a page with show: no was shown anyway")
		}
	}
}

func TestAPageThatCouldNotWorkIsRefusedAtLoad(t *testing.T) {
	tests := []struct {
		name, yaml, want string
	}{
		{
			"a box with keys of its own",
			"pages:\n  - name: p\n    box: yes\n    text: [go]\n    keys: [enter]\n",
			"holds every key",
		},
		{
			"a key with a space in it",
			"pages:\n  - name: p\n    text: [go]\n    keys: [\"alt m\"]\n",
			"is not a key",
		},
		{
			"a stop key with a space in it",
			"pages:\n  - name: p\n    text: [go]\n    stop: \"alt m\"\n",
			"is not a key",
		},
		{
			"an unknown place",
			"pages:\n  - name: p\n    text: [go]\n    go: narnia\n",
			`unknown go: "narnia"`,
		},
		{
			"no text",
			"pages:\n  - name: p\n    keys: [enter]\n",
			"no text",
		},
		{
			"two pages with one name",
			"pages:\n  - name: p\n    text: [a]\n  - name: p\n    text: [b]\n",
			"two pages are named",
		},
		{
			"a misspelled key",
			"pages:\n  - name: p\n    txt: [go]\n",
			"field txt not found",
		},
		{
			"a predicate from the old format",
			"pages:\n  - name: p\n    text: [go]\n    when: menu\n",
			"field when not found",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.yaml), "test")
			if err == nil {
				t.Fatal("Parse() error = nil, want the page refused")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestTheErrorNamesThePageAndTheVocabulary(t *testing.T) {
	_, err := Parse([]byte("pages:\n  - name: open-the-list\n    text: [go]\n    go: nope\n"), "tutorial.yaml")
	if err == nil {
		t.Fatal("Parse() error = nil")
	}
	for _, want := range []string{"tutorial.yaml", "page 1 (open-the-list)", "theme-section"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to name %q", err, want)
		}
	}
}

func TestAFolderOfTutorialsIsReadInFileOrder(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("2-second.yaml", "name: Second\npages:\n  - name: p\n    text: [b]\n")
	write("1-first.yaml", "name: First\npages:\n  - name: p\n    text: [a]\n")
	write("notes.txt", "not a tutorial")

	flows, err := Flows(dir)
	if err != nil {
		t.Fatalf("Flows() error = %v", err)
	}
	if len(flows) != 2 {
		t.Fatalf("got %d tutorials, want one per yaml file", len(flows))
	}
	if flows[0].Name != "First" || flows[1].Name != "Second" {
		t.Errorf("order = %q, %q; want file order", flows[0].Name, flows[1].Name)
	}

	one, err := Flows(filepath.Join(dir, "1-first.yaml"))
	if err != nil || len(one) != 1 || one[0].Name != "First" {
		t.Errorf("Flows(file) = %+v, %v; want the one tutorial in it", one, err)
	}
}

func TestWithNoTutorialsOfYourOwnTheShippedOneIsTheList(t *testing.T) {
	flows, err := Flows(filepath.Join(t.TempDir(), DirName))
	if err != nil {
		t.Fatalf("Flows() error = %v", err)
	}
	if len(flows) != 1 || flows[0].Name == "" {
		t.Fatalf("flows = %+v, want the shipped tutorial, named", flows)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mine.yaml"),
		[]byte("pages:\n  - name: p\n    text: [a]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flows, err = Flows(dir)
	if err != nil {
		t.Fatalf("Flows() error = %v", err)
	}
	if len(flows) != 1 || flows[0].Name != "Mine" {
		t.Errorf("flows = %+v, want yours to have taken the shipped one's place", flows)
	}
}

func TestOneUnreadableFileDoesNotLoseTheFolder(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{
		"good.yaml": "name: Good\npages:\n  - name: p\n    text: [a]\n",
		"bad.yaml":  "pages:\n  - name: p\n    text: [a]\n    go: narnia\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	flows, err := Flows(dir)
	if err == nil {
		t.Fatal("Flows() error = nil, want the file it could not read named")
	}
	if !strings.Contains(err.Error(), "bad.yaml") || !strings.Contains(err.Error(), "narnia") {
		t.Errorf("error = %q, want the file and the reason", err)
	}
	if len(flows) != 1 || flows[0].Name != "Good" {
		t.Errorf("flows = %+v, want the one that parses", flows)
	}
}

func TestAPageCanNameTheKeysThatReachIt(t *testing.T) {
	s, err := Parse([]byte(`
pages:
  - name: open-the-filter
    text: [press /]
    keys: [/]
    stop: /
  - name: type-a-name
    text: [type part of a name]
    keys: [typing, backspace, enter]
  - name: read-this
    text: [nothing to press but enter]
`), "test")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if got := s.Pages[0].Keys; len(got) != 1 || got[0] != "/" {
		t.Errorf("keys = %v, want the one key the page teaches", got)
	}
	if got := s.Pages[1].Keys; len(got) != 3 || got[0] != Typing {
		t.Errorf("keys = %v, want typing, backspace and enter", got)
	}
	if got := s.Pages[2].Keys; got != nil {
		t.Errorf("keys = %v on a page that named none, want every key through", got)
	}
}

func TestAPageIsItsKeysAndItsStopKey(t *testing.T) {
	s, err := Parse([]byte(`
pages:
  - name: read-this
    text: [look at the list]
  - name: do-this
    text: [press /]
    keys: [/]
    stop: /
`), "test")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	read, do := s.Pages[0], s.Pages[1]
	if len(read.Keys) != 0 || read.StopKey() != DefaultStop {
		t.Errorf("read page = %+v, want nothing through it and enter to end it", read)
	}
	if len(do.Keys) != 1 || do.StopKey() != "/" {
		t.Errorf("action page = %+v, want / both allowed and the end of it", do)
	}
}
