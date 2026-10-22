package view

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, text string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAFolderOfScreensLoadsInFileOrder(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "elements.yaml", "kind: list\ncolumns:\n  - name\n")
	write(t, dir, "menu.yaml", "kind: menu\nname: Main Menu\nentries: [quit]\n")
	write(t, dir, "notes.txt", "not a screen")

	files, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("%d files, want the two yaml ones", len(files))
	}
	if files[0].Name != "Elements" || files[0].Kind != KindList {
		t.Errorf("first file = %+v, want the element list named after its file", files[0])
	}
	if files[1].Name != "Main Menu" {
		t.Errorf("second file is called %q, want the name it gives itself", files[1].Name)
	}
	if files[0].Source == "" || files[0].Bytes == nil {
		t.Error("a file arrives without its path or its text")
	}
}

func TestAScreenIontkCannotDrawIsNamedAndTheRestLoad(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a-elements.yaml", "kind: list\ncolumns:\n  - name\n")
	write(t, dir, "b-shell.yaml", "kind: shell\n")
	write(t, dir, "c-nothing.yaml", "columns:\n  - name\n")

	files, err := Load(dir)
	if err == nil {
		t.Fatal("Load() took a kind iontk does not draw")
	}
	for _, want := range []string{"b-shell.yaml", `"shell"`, "c-nothing.yaml", "no kind"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %v, want it to name %q", err, want)
		}
	}
	if len(files) != 1 {
		t.Errorf("%d files loaded, want the good one to survive", len(files))
	}
}

func TestOneFileIsAFolderOfOne(t *testing.T) {
	dir := t.TempDir()
	path := write(t, dir, "menu.yaml", "kind: menu\nentries: [quit]\n")

	files, err := Load(path)
	if err != nil || len(files) != 1 {
		t.Fatalf("Load(%q) = %d files, %v", path, len(files), err)
	}
	if files, err := Load(filepath.Join(dir, "gone")); err != nil || files != nil {
		t.Errorf("a folder that is not there = %v, %v; want nothing and no error", files, err)
	}
}

func TestTheEnvelopeIsCheckedBeforeAFileIsWritten(t *testing.T) {
	if err := CheckEnvelope([]byte("columns:\n  - name\n"), KindList); err == nil {
		t.Error("a file with no kind passed the check")
	}
	if err := CheckEnvelope([]byte("kind: menu\n"), KindList); err == nil {
		t.Error("a menu file passed as the element list")
	}
	if err := CheckEnvelope([]byte("kind: list\ncolumns: []\n"), KindList); err != nil {
		t.Errorf("a list file failed the check: %v", err)
	}
}

func TestAScreenIsNamedAfterItsFileAndBack(t *testing.T) {
	for name, want := range map[string]string{
		"Elements":  "elements.yaml",
		"Main Menu": "main-menu.yaml",
		"HA":        "ha.yaml",
		"":          "view.yaml",
	} {
		if got := FileName(name); got != want {
			t.Errorf("FileName(%q) = %q, want %q", name, got, want)
		}
	}
	if got := titleFrom("/x/main-menu.yaml"); got != "Main Menu" {
		t.Errorf("titleFrom() = %q, want %q", got, "Main Menu")
	}
}
