package ui

import (
	"charm.land/lipgloss/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/colorprofile"
)

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

func TestTabPalettesCarryNoStatusMeaning(t *testing.T) {
	reserved := map[string]string{
		"1": "red", "2": "green", "3": "yellow",
		"9": "bright red", "10": "bright green", "11": "bright yellow",
	}
	for _, th := range Themes() {
		if th.Mono {
			continue
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
	if _, ok := ThemeByName("nord"); !ok {
		t.Error("a user file replaced the whole built-in set")
	}
	if ThemesErr() != "" {
		t.Errorf("ThemesErr() = %q for a file that parsed", ThemesErr())
	}
}

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
	if !UseTheme("BEN'S THEME") {
		t.Error("UseTheme() is case-sensitive, which a hand-written config will not be")
	}
}

func TestApplyRestylesTheMarks(t *testing.T) {
	restore(t)
	mono, _ := ThemeByName("mono")
	Apply(mono)
	if got := MarkCheck.String(); strings.Contains(got, "[3") {
		t.Errorf("MarkCheck = %q, want no colour under mono", got)
	}
	nord, _ := ThemeByName("nord")
	Apply(nord)
	if got := MarkCheck.String(); !strings.Contains(got, "163") {
		t.Errorf("MarkCheck = %q, want nord's green", got)
	}
}

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
	if StyleTab(TabShell, 1, 0, true).Render("x") == StyleTab(TabShell, 1, 0, false).Render("x") {
		t.Error("the active tab renders the same as an inactive one")
	}
	StyleTab(TabShell, len(styles.Tabs)+3, len(styles.Tabs)+3, false)
}

func TestTheTabModesColourDifferently(t *testing.T) {
	restore(t)
	base := Theme{
		Name: "tabs", OK: "2",
		Tabs:     []string{"#111111", "#222222", "#333333"},
		TabShell: []string{"#aa0000", "#bb0000"},
		TabData:  []string{"#00aa00", "#00bb00"},
	}
	tests := []struct {
		mode string
		want []string
	}{
		{TabRotate, []string{"#111111", "#222222", "#333333", "#111111", "#222222"}},
		{TabByType, []string{"#111111", "#aa0000", "#aa0000", "#00aa00", "#00aa00"}},
		{TabBoth, []string{"#111111", "#aa0000", "#bb0000", "#00aa00", "#00bb00"}},
		{TabShared, []string{"#111111", "#111111", "#222222", "#111111", "#222222"}},
	}

	for _, tc := range tests {
		t.Run(tc.mode, func(t *testing.T) {
			th := base.Clone()
			th.TabMode = tc.mode
			Apply(th)

			got := []string{
				fgOf(StyleTab(TabList, 0, 0, false)),
				fgOf(StyleTab(TabShell, 1, 0, false)),
				fgOf(StyleTab(TabShell, 2, 1, false)),
				fgOf(StyleTab(TabData, 3, 0, false)),
				fgOf(StyleTab(TabData, 4, 1, false)),
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("mode %s painted %v, want %v", tc.mode, got, tc.want)
			}
		})
	}

	bare := Theme{Name: "bare", Tabs: []string{"#111111", "#222222"}, TabMode: TabBoth}
	Apply(bare)
	if got := fgOf(StyleTab(TabShell, 1, 1, false)); got != "#222222" {
		t.Errorf("with no shell list the tab is %q, want the rotation", got)
	}

	if err := (&Theme{Name: "x", TabMode: "sideways"}).validate(); err == nil {
		t.Error("tab_mode sideways was accepted")
	}
}

func fgOf(s lipgloss.Style) string {
	c := s.GetForeground()
	if c == nil {
		return ""
	}
	r, g, b, _ := c.RGBA()
	return HexFromRGB([3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8)})
}

func TestTheBackgroundIsOnlySetByASchemeThatAsksForIt(t *testing.T) {
	restore(t)

	Apply(Theme{Name: "bare", OK: "2"})
	if got := Background(); got != nil {
		t.Errorf("Background() = %v for a scheme with no bg, want nil", got)
	}
	if got := Foreground(); got != nil {
		t.Errorf("Foreground() = %v for a scheme with no fg, want nil", got)
	}

	Apply(Theme{Name: "mocha", Bg: "#1e1e2e", Fg: "#cdd6f4"})
	bg := Background()
	if bg == nil {
		t.Fatal("Background() = nil for a scheme that sets bg")
	}
	r, g, b, a := bg.RGBA()
	if r>>8 != 0x1e || g>>8 != 0x1e || b>>8 != 0x2e || a>>8 != 0xff {
		t.Errorf("Background() = %04x%04x%04x alpha %04x, want #1e1e2e opaque", r, g, b, a)
	}

	Apply(Theme{Name: "indexed", Bg: "4"})
	if got := Background(); got == nil {
		t.Error("an index background did not resolve")
	}
}

func TestBackgroundIsARoleLikeTheOthers(t *testing.T) {
	restore(t)

	var found int
	for _, role := range RoleNames() {
		if role == "background" || role == "other" {
			found++
		}
	}
	if found != 2 {
		t.Errorf("RoleNames() holds %d of background and other, want both", found)
	}

	var th Theme
	if err := th.SetRole("background", "#102030"); err != nil {
		t.Fatalf("SetRole(background) = %v", err)
	}
	if th.Bg != "#102030" || th.Role("background") != "#102030" {
		t.Errorf("background did not round-trip: %q / %q", th.Bg, th.Role("background"))
	}
	if err := (&Theme{Name: "x", Bg: "chartreuse"}).validate(); err == nil {
		t.Error("a background that will not parse was accepted")
	}

	classic := Theme{Name: "c", OK: "2", Err: "1", Bg: "#1e1e2e"}
	if classic.Deep() {
		t.Error("a scheme in the first sixteen reads as deep because of its bg")
	}
}

func TestEveryShippedSchemeSetsABackgroundExceptTwo(t *testing.T) {
	restore(t)
	loaded, loadErr = nil, ""

	bare := map[string]string{
		"classic": "the default, and the terminal's own sixteen colours",
		"mono":    "no colour at all is the whole premise",
	}

	for _, th := range Themes() {
		why, exempt := bare[th.Name]
		switch {
		case exempt && (th.Bg != "" || th.Fg != ""):
			t.Errorf("%s sets bg %q / fg %q, but %s", th.Name, th.Bg, th.Fg, why)
		case exempt:
		case th.Bg == "":
			t.Errorf("%s sets no bg, so choosing it leaves the terminal unchanged", th.Name)
		case th.Fg == "":
			t.Errorf("%s sets a bg but no fg, so uncoloured text keeps the old colour", th.Name)
		}

		for _, role := range []string{"background", "other"} {
			if spec := th.Role(role); spec != "" {
				if _, ok := RGBOf(spec); !ok {
					t.Errorf("%s: %s = %q does not resolve", th.Name, role, spec)
				}
			}
		}
	}
}

func TestNoShippedSchemeHidesItsTextInItsBackground(t *testing.T) {
	restore(t)
	loaded, loadErr = nil, ""

	for _, th := range Themes() {
		if th.Bg == "" {
			continue
		}
		bg, _ := RGBOf(th.Bg)
		_, _, bgv := HSVOf(bg)
		for _, role := range RoleNames() {
			if role == "background" || role == "divider" {
				continue
			}
			spec := th.Role(role)
			if spec == "" {
				continue
			}
			c, ok := RGBOf(spec)
			if !ok {
				continue
			}
			_, _, v := HSVOf(c)
			if d := v - bgv; d < 0.2 && d > -0.2 {
				t.Errorf("%s: %s (%s) is as bright as its background (%s)",
					th.Name, role, spec, th.Bg)
			}
		}
	}
}

func TestASchemeIsWrittenToItsOwnFile(t *testing.T) {
	restore(t)
	dir := filepath.Join(t.TempDir(), ThemesDirName)

	if got := ThemeFileName("Ben's Theme"); got != "benstheme.yaml" {
		t.Errorf("ThemeFileName(Ben's Theme) = %q", got)
	}
	if got := ThemeFileName("classic"); got != "classic.yaml" {
		t.Errorf("ThemeFileName(classic) = %q", got)
	}

	mine := Theme{Name: "workbench", OK: "#a6e3a1", Err: "1", Spinner: "dot"}
	if err := SaveUserTheme(dir, mine); err != nil {
		t.Fatalf("SaveUserTheme: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "workbench.yaml"))
	if err != nil {
		t.Fatalf("the scheme has no file of its own: %v", err)
	}
	if strings.Contains(string(b), "themes:") {
		t.Errorf("the file still holds a list of schemes:\n%s", b)
	}
	if strings.Contains(string(b), `bg: ""`) {
		t.Errorf("an unset role was written out:\n%s", b)
	}

	if got, ok := ThemeByName("workbench"); !ok || got.OK != "#a6e3a1" {
		t.Errorf("the scheme did not load back: %+v", got)
	}
	if _, ok := ThemeByName("nord"); !ok {
		t.Error("writing one scheme took the shipped set with it")
	}
}

func TestADirectoryOfSchemesLoadsInNameOrder(t *testing.T) {
	restore(t)
	dir := filepath.Join(t.TempDir(), ThemesDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"zinc", "amber", "cobalt"} {
		body := "name: " + name + "\nok: \"2\"\n"
		if err := os.WriteFile(filepath.Join(dir, name+".yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := LoadThemes(dir); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	var mine []string
	for _, th := range Themes() {
		switch th.Name {
		case "zinc", "amber", "cobalt":
			mine = append(mine, th.Name)
		}
	}
	want := []string{"amber", "cobalt", "zinc"}
	if !slices.Equal(mine, want) {
		t.Errorf("loaded %v, want %v", mine, want)
	}
}

func TestOneBadFileLeavesTheRestLoaded(t *testing.T) {
	restore(t)
	dir := filepath.Join(t.TempDir(), ThemesDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "good.yaml"),
		[]byte("name: good\nok: \"2\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.yaml"),
		[]byte("name: bad\nok: chartreuse\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := LoadThemes(dir); err == nil {
		t.Error("LoadThemes said nothing about the file that will not parse")
	}
	if _, ok := ThemeByName("good"); !ok {
		t.Error("the good scheme was lost with the bad one")
	}
	if _, ok := ThemeByName("bad"); ok {
		t.Error("the scheme with an unparseable colour was loaded anyway")
	}
	if ThemesErr() == "" {
		t.Error("nothing is on the settings page about it")
	}
}

func TestALegacyThemesFileIsStillRead(t *testing.T) {
	restore(t)
	root := t.TempDir()
	body := "themes:\n  - name: heirloom\n    ok: \"2\"\n"
	if err := os.WriteFile(filepath.Join(root, ThemesFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := LoadThemes(filepath.Join(root, ThemesDirName)); err != nil {
		t.Fatalf("LoadThemes: %v", err)
	}
	if _, ok := ThemeByName("heirloom"); !ok {
		t.Error("the legacy themes.yaml was not read")
	}
}

func TestDeletingASchemeFileRestoresTheShippedOne(t *testing.T) {
	restore(t)
	dir := filepath.Join(t.TempDir(), ThemesDirName)

	override := Theme{Name: "nord", OK: "#ffffff", Spinner: "dot"}
	if err := SaveUserTheme(dir, override); err != nil {
		t.Fatal(err)
	}
	if got, _ := ThemeByName("nord"); got.OK != "#ffffff" {
		t.Fatalf("the override did not take: ok = %q", got.OK)
	}
	if !IsUserTheme(dir, "nord") {
		t.Error("IsUserTheme says the override is not the user's")
	}
	if IsUserTheme(dir, "desert") {
		t.Error("IsUserTheme claims a scheme with no file of its own")
	}

	if err := DeleteUserTheme(dir, "nord"); err != nil {
		t.Fatalf("DeleteUserTheme: %v", err)
	}
	got, ok := ThemeByName("nord")
	if !ok {
		t.Fatal("nord went away with the override; it is one of the shipped set")
	}
	if got.OK == "#ffffff" {
		t.Error("the override is still in the set after its file was deleted")
	}

	if err := SaveUserTheme(dir, Theme{Name: "workbench", OK: "2"}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteUserTheme(dir, "workbench"); err != nil {
		t.Fatal(err)
	}
	if _, ok := ThemeByName("workbench"); ok {
		t.Error("the scheme is still in the set after being deleted")
	}
	if err := DeleteUserTheme(dir, "workbench"); err == nil {
		t.Error("deleting it twice did not say there was nothing to delete")
	}
}

func TestASchemeInTheLegacyFileIsStillTheUsers(t *testing.T) {
	restore(t)
	root := t.TempDir()
	dir := filepath.Join(root, ThemesDirName)
	body := "themes:\n  - name: test\n    ok: \"2\"\n  - name: keeper\n    ok: \"3\"\n"
	if err := os.WriteFile(filepath.Join(root, ThemesFileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadThemes(dir); err != nil {
		t.Fatal(err)
	}

	if !IsUserTheme(dir, "test") {
		t.Error("a scheme in the legacy file reads as shipped")
	}
	if err := DeleteUserTheme(dir, "test"); err != nil {
		t.Fatalf("DeleteUserTheme: %v", err)
	}
	if _, ok := ThemeByName("test"); ok {
		t.Error("test is still in the set")
	}
	if _, ok := ThemeByName("keeper"); !ok {
		t.Error("deleting one scheme took the rest of the file with it")
	}
}

func TestHiddenSchemesAreLeftOutOfTheSet(t *testing.T) {
	restore(t)
	t.Cleanup(func() { HideThemes(nil) })

	before := len(Themes())
	HideThemes([]string{"desert", "evening"})
	if got := len(Themes()); got != before-2 {
		t.Errorf("Themes() has %d, want %d", got, before-2)
	}
	if _, ok := ThemeByName("desert"); ok {
		t.Error("a hidden scheme is still in the set")
	}
	if !IsHidden("DESERT") {
		t.Error("IsHidden is case sensitive")
	}
	if _, ok := BuiltinTheme("desert"); !ok {
		t.Error("hiding a scheme reached the embedded set")
	}

	var all []string
	for _, th := range allThemes() {
		all = append(all, th.Name)
	}
	HideThemes(all)
	if got := Themes(); len(got) != 1 {
		t.Errorf("Themes() = %d schemes with everything hidden, want the fallback", len(got))
	}
}

func TestRemoveOverridesLeavesTheUsersOwnSchemes(t *testing.T) {
	restore(t)
	dir := filepath.Join(t.TempDir(), ThemesDirName)

	if err := SaveUserTheme(dir, Theme{Name: "nord", OK: "#ffffff"}); err != nil {
		t.Fatal(err)
	}
	if err := SaveUserTheme(dir, Theme{Name: "workbench", OK: "2"}); err != nil {
		t.Fatal(err)
	}

	n, err := RemoveOverrides(dir)
	if err != nil {
		t.Fatalf("RemoveOverrides: %v", err)
	}
	if n != 1 {
		t.Errorf("removed %d, want the one override", n)
	}
	if got, _ := ThemeByName("nord"); got.OK == "#ffffff" {
		t.Error("the override survived")
	}
	if _, ok := ThemeByName("workbench"); !ok {
		t.Error("a scheme of the user's own was removed")
	}
}
