package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

// restore puts the shipped set and the default scheme back. Both are process
// state, so a test that left one changed would decide what every later test
// rendered.
func restore(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		loaded, loadErr = nil, ""
		Apply(Default())
	})
}

func TestTheShippedThemesParse(t *testing.T) {
	restore(t)
	loaded, loadErr = nil, ""

	set := Themes()
	if err := ThemesErr(); err != "" {
		t.Fatalf("themes.yaml: %s", err)
	}
	if len(set) < 8 {
		t.Errorf("%d themes shipped, want the vim-flavoured set too", len(set))
	}
	for _, want := range []string{"classic", "mono", "nord", "desert", "morning", "Ben's Theme"} {
		if _, ok := ThemeByName(want); !ok {
			t.Errorf("no theme called %q", want)
		}
	}
	if Default().Name != "classic" {
		t.Errorf("Default() = %q, want classic", Default().Name)
	}
}

func TestEveryThemeIsUsable(t *testing.T) {
	seen := map[string]bool{}
	for _, th := range Themes() {
		if th.Name == "" || th.Blurb == "" {
			t.Errorf("theme %q has no name or no blurb, so the picker cannot list it", th.Name)
		}
		if seen[strings.ToLower(th.Name)] {
			t.Errorf("two themes are called %q", th.Name)
		}
		seen[strings.ToLower(th.Name)] = true

		// Every scheme names its spinner: the shape is part of the theme, and
		// one that named nothing would quietly be somebody else's default.
		if !KnownSpinner(th.Spinner) {
			t.Errorf("%s names spinner %q, which is not a shape", th.Name, th.Spinner)
		}

		s := th.Styles()
		if len(s.Tabs) == 0 {
			t.Errorf("%s has no tab styles, so StyleTab would divide by zero", th.Name)
		}
		if th.Swatch() == "" {
			t.Errorf("%s renders no swatch, so the picker shows nothing", th.Name)
		}
		if s.Row.Render("x") == "x" && !th.Mono {
			t.Errorf("%s leaves the row highlight unstyled", th.Name)
		}
	}
}

// Tabs are identity, not status: a tab coloured like an error reads as a failed
// one. The ANSI check is by index; the general one is that no tab may reuse the
// scheme's own ok, err or warn, which catches a hex palette too.
func TestTabPalettesCarryNoStatusMeaning(t *testing.T) {
	reserved := map[string]string{
		"1": "red", "2": "green", "3": "yellow",
		"9": "bright red", "10": "bright green", "11": "bright yellow",
	}
	for _, th := range Themes() {
		if th.Mono {
			continue // nothing to confuse: the active tab is reversed, not coloured
		}
		if len(th.Tabs) < 4 {
			t.Errorf("%s has %d tab colours, want enough to tell tabs apart", th.Name, len(th.Tabs))
		}
		status := map[string]string{th.OK: "ok", th.Err: "err", th.Warn: "warn"}
		for _, c := range th.Tabs {
			if name, bad := reserved[c]; bad {
				t.Errorf("%s uses %s (%s) for a tab, which carries status meaning", th.Name, c, name)
			}
			if role, clash := status[c]; clash {
				t.Errorf("%s uses its %s colour (%s) for a tab", th.Name, role, c)
			}
		}
	}
}

// The default has to be the one scheme that cannot clash with a terminal's own
// palette, which is the only reason the 0-15 rule existed.
func TestOnlyAnOptedInSchemeLeavesTheAnsiRange(t *testing.T) {
	if Default().Deep() {
		t.Errorf("the default scheme %q leaves ANSI 0-15", Default().Name)
	}
	for _, name := range []string{"vivid", "mono"} {
		th, _ := ThemeByName(name)
		if th.Deep() {
			t.Errorf("%s leaves ANSI 0-15, so it depends on the terminal's depth", name)
		}
	}
	for _, name := range []string{"nord", "Ben's Theme"} {
		th, _ := ThemeByName(name)
		if !th.Deep() {
			t.Errorf("%s is hex and should report itself as deep", name)
		}
	}
}

// A themes.yaml in the config root is the whole point of the format: it adds
// schemes, and replaces a built-in by name.
func TestAUserFileAddsAndReplaces(t *testing.T) {
	restore(t)
	path := filepath.Join(t.TempDir(), ThemesFileName)
	err := os.WriteFile(path, []byte(`
themes:
  - name: classic
    blurb: mine, not yours
    ok: "4"
    spinner: monkey
  - name: office
    blurb: for the projector
    ok: "#00ff00"
    spinner: globe
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := LoadThemes(path); err != nil {
		t.Fatalf("LoadThemes() error = %v", err)
	}

	got, ok := ThemeByName("classic")
	if !ok || got.Blurb != "mine, not yours" {
		t.Errorf("classic = %+v, want the file's own", got)
	}
	if got.Spinner != "monkey" {
		t.Errorf("classic spinner = %q, want the file's monkey", got.Spinner)
	}
	if _, ok := ThemeByName("office"); !ok {
		t.Error("the file's new scheme was not added")
	}
	// Replacing one must not drop the rest.
	if _, ok := ThemeByName("nord"); !ok {
		t.Error("a user file replaced the whole built-in set")
	}
	if ThemesErr() != "" {
		t.Errorf("ThemesErr() = %q for a file that parsed", ThemesErr())
	}
}

// A scheme that will not load has to say why: lipgloss reads a colour it
// cannot parse as no colour, so the failure would otherwise be invisible text.
func TestABrokenThemeFileIsRefusedAndTheBuiltinsSurvive(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"a colour that is not one", "themes:\n  - name: x\n    ok: \"blue\"\n", "ANSI index"},
		{"an index past 255", "themes:\n  - name: x\n    ok: \"300\"\n", "ANSI index"},
		{"half a hex colour", "themes:\n  - name: x\n    ok: \"#12345\"\n", "#rrggbb"},
		{"a spinner nobody has", "themes:\n  - name: x\n    spinner: wheel\n", "spinner"},
		{"a misspelled key", "themes:\n  - name: x\n    colour: \"2\"\n", "field colour"},
		{"a theme with no name", "themes:\n  - blurb: nameless\n", "needs a name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			restore(t)
			path := filepath.Join(t.TempDir(), ThemesFileName)
			if err := os.WriteFile(path, []byte(tc.yaml), 0o600); err != nil {
				t.Fatal(err)
			}

			err := LoadThemes(path)
			if err == nil {
				t.Fatal("LoadThemes() accepted it")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
			if ThemesErr() == "" {
				t.Error("ThemesErr() is empty, so the settings page would not say")
			}
			if _, ok := ThemeByName("classic"); !ok {
				t.Error("a broken file took the built-ins with it")
			}
		})
	}
}

// A missing file is the normal case, not an error.
func TestNoUserFileIsNotAnError(t *testing.T) {
	restore(t)
	if err := LoadThemes(filepath.Join(t.TempDir(), "nothing.yaml")); err != nil {
		t.Errorf("LoadThemes() error = %v for a file that is not there", err)
	}
	if ThemesErr() != "" {
		t.Errorf("ThemesErr() = %q", ThemesErr())
	}
}

func TestUseThemeFallsBackToTheDefault(t *testing.T) {
	restore(t)
	if !UseTheme("nord") {
		t.Error("UseTheme(nord) reported the name as unknown")
	}
	if CurrentTheme().Name != "nord" {
		t.Errorf("CurrentTheme() = %q, want nord", CurrentTheme().Name)
	}
	if UseTheme("a scheme from a later version") {
		t.Error("UseTheme() claimed to know a name it does not")
	}
	if CurrentTheme().Name != Default().Name {
		t.Errorf("CurrentTheme() = %q, want the default after an unknown name", CurrentTheme().Name)
	}
	// Names are matched the way a hand-edited config spells them.
	if !UseTheme("BEN'S THEME") {
		t.Error("UseTheme() is case-sensitive, which a hand-written config will not be")
	}
}

// Apply has to rebuild everything derived from the palette, the marks
// included: they were built from StyleOK and StyleErr at package load.
func TestApplyRestylesTheMarks(t *testing.T) {
	restore(t)
	mono, _ := ThemeByName("mono")
	Apply(mono)
	if got := MarkCheck.String(); strings.Contains(got, "[3") {
		t.Errorf("MarkCheck = %q, want no colour under mono", got)
	}
	nord, _ := ThemeByName("nord")
	Apply(nord)
	// nord's green is #a3be8c, which lands on an RGB escape.
	if got := MarkCheck.String(); !strings.Contains(got, "163") {
		t.Errorf("MarkCheck = %q, want nord's green", got)
	}
}

// Mono is not the palette with the colours taken out: a role that was a
// colour has to become an attribute or it stops being distinguishable.
func TestMonoKeepsRolesApartWithoutColour(t *testing.T) {
	mono, _ := ThemeByName("mono")
	s := mono.Styles()
	if s.Err.Render("x") == s.Warn.Render("x") {
		t.Error("mono renders errors and warnings identically")
	}
	if s.Cmd.Render("x") == s.Element.Render("x") {
		t.Error("mono renders commands and element names identically")
	}
	for _, st := range []string{s.Err.Render("x"), s.Cmd.Render("x"), s.Row.Render("x")} {
		if !strings.Contains(st, "\x1b[") {
			t.Errorf("mono rendered %q with no attribute at all", st)
		}
	}
}

// The renderer downsamples what we emit, so the only thing to do about a
// shallow terminal is say that a deep scheme is being approximated.
func TestThemeNoteOnlyWarnsWhenItMatters(t *testing.T) {
	t.Cleanup(func() { SetProfile(colorprofile.Unknown) })
	nord, _ := ThemeByName("nord")

	SetProfile(colorprofile.Unknown)
	if got := ThemeNote(nord); got != "" {
		t.Errorf("ThemeNote() = %q before the terminal has said anything", got)
	}

	SetProfile(colorprofile.TrueColor)
	if got := ThemeNote(nord); got != "" {
		t.Errorf("ThemeNote() = %q on a terminal that can show it", got)
	}

	SetProfile(colorprofile.ANSI256)
	if got := ThemeNote(nord); got == "" {
		t.Error("ThemeNote() says nothing about nord on a 256-colour terminal")
	}
	if got := ThemeNote(Default()); got != "" {
		t.Errorf("ThemeNote() = %q for a 0-15 scheme, which needs no warning", got)
	}
	if got := ProfileName(); got != "256 colours" {
		t.Errorf("ProfileName() = %q", got)
	}
}

func TestStyleTabMarksTheActiveOne(t *testing.T) {
	restore(t)
	Apply(Default())
	if StyleTab(0, true).Render("x") == StyleTab(0, false).Render("x") {
		t.Error("the active tab renders the same as an inactive one")
	}
	// Past the end of the palette rather than out of range: tabs wrap.
	StyleTab(len(styles.Tabs)+3, false)
}
