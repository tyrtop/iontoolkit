package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tyrtop.com/iontk/internal/app"
	"tyrtop.com/iontk/internal/view"
)

func runtimeModel(t *testing.T, files map[string]string) tuiModel {
	t.Helper()
	dir := filepath.Join(t.TempDir(), view.DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	app.ViewsFolder = dir
	t.Cleanup(func() { app.ViewsFolder = "" })
	return listModel(t, 3)
}

func TestTheFolderDrawsTheListTheMenuAndTheViews(t *testing.T) {
	m := runtimeModel(t, map[string]string{
		"elements.yaml": "kind: list\ncolumns:\n  - name\n  - state\n",
		"menu.yaml":     "kind: menu\nentries:\n  - elements\n  - name: quit\n    label: Leave\n",
		"overview.yaml": "kind: data\nname: Overview\nsections:\n  - title: Device\n    rows:\n      - label: Name\n        value: \"{{ .el.name }}\"\n",
	})

	if err := m.runtimeErr(); err != nil {
		t.Fatalf("the folder was refused: %v", err)
	}
	if got := columnNames(&m); strings.Join(got, " ") != colCaret+" "+colName+" "+colState {
		t.Errorf("columns = %v, want the ones elements.yaml names", got)
	}

	entries := m.menuEntries()
	if len(entries) != 2 || entries[1].label != "Leave" {
		t.Errorf("menu = %+v, want the two entries menu.yaml names, the last relabelled", entries)
	}

	v, ok := m.runtime().Views().View("Overview")
	if !ok {
		t.Fatal("the folder's Overview did not reach the view set")
	}
	if v.Source == "" || len(v.Sections) != 1 {
		t.Errorf("Overview = %+v, want the one the folder replaced it with", v)
	}
	if got := len(m.viewNames()); got != 4 {
		t.Errorf("%d views, want the four shipped with Overview replaced", got)
	}
}

func TestTheFolderWinsOverTheSingleFiles(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, view.DirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "columns.yaml"), []byte("columns:\n  - site\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "elements.yaml"),
		[]byte("kind: list\ncolumns:\n  - name\n  - model\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	app.ColumnsFile, app.ViewsFolder = filepath.Join(root, "columns.yaml"), dir
	t.Cleanup(func() { app.ColumnsFile, app.ViewsFolder = "", "" })

	m := listModel(t, 2)
	if got := columnNames(&m); strings.Join(got, " ") != colCaret+" "+colName+" "+colModel {
		t.Errorf("columns = %v, want the folder's and not the single file's", got)
	}
	if m.listFrom == "" || !strings.Contains(m.listFrom, view.DirName) {
		t.Errorf("the list says it came from %q, want the folder's file", m.listFrom)
	}
}

func TestAMenuFileWithNoWayOutIsRefused(t *testing.T) {
	m := runtimeModel(t, map[string]string{
		"menu.yaml": "kind: menu\nentries:\n  - elements\n",
	})
	if m.menuErr == "" {
		t.Fatal("a menu with no quit entry loaded")
	}
	if !strings.Contains(m.menuErr, "quit") {
		t.Errorf("menuErr = %q, want it to name what is missing", m.menuErr)
	}
	if m.menuSpec != nil {
		t.Error("the refused menu was taken anyway")
	}
	if got := len(m.menuEntries()); got < 3 {
		t.Errorf("%d entries, want the menu iontk ships", got)
	}
}

func TestAnEntryIontkDoesNotRunIsNamed(t *testing.T) {
	m := runtimeModel(t, map[string]string{
		"menu.yaml": "kind: menu\nentries:\n  - reboot-everything\n  - quit\n",
	})
	for _, want := range []string{"reboot-everything", "settings"} {
		if !strings.Contains(m.menuErr, want) {
			t.Errorf("menuErr = %q, want it to name %q", m.menuErr, want)
		}
	}
}

func TestAltEOnTheMenuOpensTheMenusFile(t *testing.T) {
	m := runtimeModel(t, map[string]string{
		"menu.yaml": "kind: menu\nentries:\n  - elements\n  - quit\n",
	})
	m.phase = phaseMenu
	mm := openEditor(t, m)

	if got := mm.edit.target.what; got != menuViewFile {
		t.Errorf("the editor opened on %q, want %q", got, menuViewFile)
	}
	if !strings.Contains(mm.edit.area.Value(), "- elements") {
		t.Errorf("the pane holds %q, want the file as it stands", mm.edit.area.Value())
	}
}
