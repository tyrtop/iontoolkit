package ui

import (
	"embed"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"gopkg.in/yaml.v3"
)

//go:embed themes.yaml
var builtinFS embed.FS

const (
	ThemesFileName = "themes.yaml"
	ThemesDirName  = "themes"
)

func ThemeFileName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "scheme.yaml"
	}
	return b.String() + ".yaml"
}

func pathIsFile(path string) bool {
	if fi, err := os.Stat(path); err == nil {
		return !fi.IsDir()
	}
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".yaml" || ext == ".yml"
}

func userThemeFiles(path string) ([]string, error) {
	if pathIsFile(path) {
		return []string{path}, nil
	}

	var out []string
	if legacy := strings.TrimSuffix(path, string(filepath.Separator)) + ".yaml"; legacy != "" {
		if fi, err := os.Stat(legacy); err == nil && !fi.IsDir() {
			out = append(out, legacy)
		}
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".yaml", ".yml":
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	for _, n := range names {
		out = append(out, filepath.Join(path, n))
	}
	return out, nil
}

type Theme struct {
	Name  string `yaml:"name"`
	Blurb string `yaml:"blurb,omitempty"`

	OK        string `yaml:"ok,omitempty"`
	Err       string `yaml:"err,omitempty"`
	Warn      string `yaml:"warn,omitempty"`
	Cmd       string `yaml:"cmd,omitempty"`
	Element   string `yaml:"element,omitempty"`
	Row       string `yaml:"row,omitempty"`
	Hot       string `yaml:"hot,omitempty"`
	Border    string `yaml:"border,omitempty"`
	Divider   string `yaml:"divider,omitempty"`
	Bg        string `yaml:"bg,omitempty"`
	Fg        string `yaml:"fg,omitempty"`
	Animation string `yaml:"animation,omitempty"`

	TabMode  string   `yaml:"tab_mode,omitempty"`
	TabShell []string `yaml:"tab_shell,omitempty"`
	TabData  []string `yaml:"tab_data,omitempty"`

	Spinner      string   `yaml:"spinner,omitempty"`
	SpinnerColor string   `yaml:"spinner_color,omitempty"`
	Tabs         []string `yaml:"tabs,omitempty"`

	Mono bool `yaml:"mono,omitempty"`
}

type Styles struct {
	OK      lipgloss.Style
	Err     lipgloss.Style
	Warn    lipgloss.Style
	Cmd     lipgloss.Style
	Element lipgloss.Style
	Hot     lipgloss.Style
	Row     lipgloss.Style
	Dim     lipgloss.Style
	Ghost   lipgloss.Style
	Divider lipgloss.Style
	Spinner lipgloss.Style

	Border    color.Color
	Tabs      []lipgloss.Style
	TabsShell []lipgloss.Style
	TabsData  []lipgloss.Style
}

type TabKind int

const (
	TabList TabKind = iota
	TabShell
	TabData
)

const (
	TabRotate = "rotate"
	TabByType = "type"
	TabBoth   = "both"
	TabShared = "shared"
)

func TabModes() []string { return []string{TabRotate, TabByType, TabBoth, TabShared} }

var TabModeBlurb = map[string]string{
	TabRotate: "a color per position in the bar",
	TabByType: "one color for shells, one for data",
	TabBoth:   "a rotation of its own per kind",
	TabShared: "one rotation, counted within the kind",
}

func KnownTabMode(mode string) bool {
	return mode == "" || slices.Contains(TabModes(), mode)
}

func TabFallback(kind TabKind) int {
	if kind == TabData {
		return 1
	}
	return 0
}

func (t Theme) TabColors(kind TabKind) []string {
	switch kind {
	case TabShell:
		if len(t.TabShell) > 0 {
			return t.TabShell
		}
	case TabData:
		if len(t.TabData) > 0 {
			return t.TabData
		}
	}
	return t.Tabs
}

var fallback = Theme{
	Name:  "classic",
	Blurb: "your terminal's own 16 colors",
	OK:    "2", Err: "1", Warn: "3", Cmd: "6", Element: "4",
	Row: "5", Hot: "9", Border: "3", Divider: "8",
	Spinner: DefaultSpinner, SpinnerColor: "5",
	Tabs: []string{"7", "4", "6", "5", "12", "14", "13"},
}

var (
	loaded  []Theme
	loadErr string
)

type themeSet struct {
	Themes []Theme `yaml:"themes"`
}

var hidden []string

func HideThemes(names []string) { hidden = slices.Clone(names) }

func HiddenThemes() []string { return slices.Clone(hidden) }

func IsHidden(name string) bool {
	for _, h := range hidden {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}

func Themes() []Theme {
	all := allThemes()
	if len(hidden) == 0 {
		return all
	}
	out := make([]Theme, 0, len(all))
	for _, t := range all {
		if !IsHidden(t.Name) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return []Theme{fallback}
	}
	return out
}

func allThemes() []Theme {
	if loaded == nil {
		set, err := builtin()
		if err != nil {
			loaded, loadErr = []Theme{fallback}, err.Error()
		} else {
			loaded = set
		}
	}
	return loaded
}

func ThemesErr() string {
	Themes()
	return loadErr
}

func builtin() ([]Theme, error) {
	b, err := builtinFS.ReadFile(ThemesFileName)
	if err != nil {
		return nil, fmt.Errorf("read built-in themes: %w", err)
	}
	set, err := ParseThemes(b)
	if err != nil {
		return nil, fmt.Errorf("built-in themes: %w", err)
	}
	return set, nil
}

func LoadThemes(path string) error {
	set, err := builtin()
	if err != nil {
		loaded, loadErr = []Theme{fallback}, err.Error()
		return err
	}
	loaded, loadErr = set, ""

	if path == "" {
		return nil
	}
	files, err := userThemeFiles(path)
	if err != nil {
		loadErr = fmt.Sprintf("read %s: %s", filepath.Base(path), err)
		return fmt.Errorf("read %s: %w", path, err)
	}

	for _, f := range files {
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				continue
			}
			loadErr = fmt.Sprintf("read %s: %s", filepath.Base(f), rerr)
			return fmt.Errorf("read %s: %w", f, rerr)
		}
		user, perr := ParseThemes(b)
		if perr != nil {
			loadErr = fmt.Sprintf("%s: %s", filepath.Base(f), perr)
			continue
		}
		for _, t := range user {
			if i := indexOf(loaded, t.Name); i >= 0 {
				loaded[i] = t
				continue
			}
			loaded = append(loaded, t)
		}
	}
	if loadErr != "" {
		return fmt.Errorf("%s", loadErr)
	}
	return nil
}

var setShape = regexp.MustCompile(`(?m)^themes:`)

func ParseThemes(b []byte) ([]Theme, error) {
	if !setShape.Match(b) {
		var one Theme
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err := dec.Decode(&one); err != nil {
			return nil, fmt.Errorf("parse: %w", err)
		}
		if err := one.validate(); err != nil {
			return nil, err
		}
		return []Theme{one}, nil
	}

	var set themeSet
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&set); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	for _, t := range set.Themes {
		if err := t.validate(); err != nil {
			return nil, err
		}
	}
	return set.Themes, nil
}

// lipgloss renders an unparseable color as nothing, not as wrong
func (t Theme) validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("a theme needs a name")
	}
	for _, role := range RoleNames() {
		if err := checkColor(t.Role(role)); err != nil {
			return fmt.Errorf("theme %s: %s: %w", t.Name, role, err)
		}
	}
	for _, set := range []struct {
		what   string
		colors []string
	}{{"tab", t.Tabs}, {"tab_shell", t.TabShell}, {"tab_data", t.TabData}} {
		for i, c := range set.colors {
			if err := checkColor(c); err != nil {
				return fmt.Errorf("theme %s: %s %d: %w", t.Name, set.what, i+1, err)
			}
		}
	}
	if !KnownTabMode(t.TabMode) {
		return fmt.Errorf("theme %s: tab_mode %q is not one of %s",
			t.Name, t.TabMode, strings.Join(TabModes(), ", "))
	}
	if t.Spinner != "" && !KnownSpinner(t.Spinner) {
		return fmt.Errorf("theme %s: spinner %q is not one of %s",
			t.Name, t.Spinner, strings.Join(SpinnerNames(), ", "))
	}
	return nil
}

func CheckColor(spec string) error { return checkColor(spec) }

func checkColor(spec string) error {
	switch {
	case spec == "":
		return nil
	case strings.HasPrefix(spec, "#"):
		if len(spec) != 4 && len(spec) != 7 {
			return fmt.Errorf("%q is not #rgb or #rrggbb", spec)
		}
		if _, err := strconv.ParseUint(spec[1:], 16, 32); err != nil {
			return fmt.Errorf("%q is not a hex color", spec)
		}
		return nil
	}
	n, err := strconv.Atoi(spec)
	if err != nil || n < 0 || n > 255 {
		return fmt.Errorf("%q is neither an ANSI index 0-255 nor a #rrggbb", spec)
	}
	return nil
}

func indexOf(set []Theme, name string) int {
	for i, t := range set {
		if strings.EqualFold(t.Name, name) {
			return i
		}
	}
	return -1
}

func Default() Theme {
	set := Themes()
	if i := indexOf(set, fallback.Name); i >= 0 {
		return set[i]
	}
	if len(set) > 0 {
		return set[0]
	}
	return fallback
}

func ThemeByName(name string) (Theme, bool) {
	set := Themes()
	if i := indexOf(set, name); i >= 0 {
		return set[i], true
	}
	return Default(), false
}

func (t Theme) fg(spec string) lipgloss.Style {
	if spec == "" {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(spec))
}

func (t Theme) Styles() Styles {
	s := Styles{
		Dim:   lipgloss.NewStyle().Faint(true),
		Ghost: lipgloss.NewStyle().Faint(true).Italic(true),
	}

	if t.Mono {
		s.OK = lipgloss.NewStyle()
		s.Err = lipgloss.NewStyle().Bold(true)
		s.Warn = lipgloss.NewStyle().Italic(true)
		s.Cmd = lipgloss.NewStyle().Bold(true)
		s.Element = lipgloss.NewStyle()
		s.Hot = lipgloss.NewStyle().Bold(true).Italic(true)
		s.Row = lipgloss.NewStyle().Reverse(true)
		s.Divider = lipgloss.NewStyle().Faint(true)
		s.Spinner = lipgloss.NewStyle()
		s.Border = lipgloss.NoColor{}
		s.Tabs = []lipgloss.Style{lipgloss.NewStyle()}
		return s
	}

	s.OK = t.fg(t.OK)
	s.Err = t.fg(t.Err)
	s.Warn = t.fg(t.Warn)
	s.Cmd = t.fg(t.Cmd).Bold(true)
	s.Element = t.fg(t.Element)
	s.Hot = t.fg(t.Hot)
	s.Row = t.fg(t.Row).Bold(true)
	s.Divider = t.fg(t.Divider)
	s.Spinner = t.fg(t.SpinnerColor)
	s.Border = lipgloss.Color(t.Border)
	for _, c := range t.Tabs {
		s.Tabs = append(s.Tabs, t.fg(c))
	}
	for _, c := range t.TabShell {
		s.TabsShell = append(s.TabsShell, t.fg(c))
	}
	for _, c := range t.TabData {
		s.TabsData = append(s.TabsData, t.fg(c))
	}
	if len(s.Tabs) == 0 {
		s.Tabs = []lipgloss.Style{lipgloss.NewStyle()}
	}
	return s
}

func (t Theme) Deep() bool {
	specs := make([]string, 0, len(RoleNames())+len(t.Tabs))
	for _, role := range RoleNames() {
		if role == "background" || role == "other" {
			continue
		}
		specs = append(specs, t.Role(role))
	}
	specs = append(specs, t.TabShell...)
	specs = append(specs, t.TabData...)
	for _, c := range append(specs, t.Tabs...) {
		if strings.HasPrefix(c, "#") {
			return true
		}
		if n, err := strconv.Atoi(c); err == nil && n >= 16 {
			return true
		}
	}
	return false
}

func (t Theme) Swatch() string {
	s := t.Styles()
	if t.Mono {
		return s.Cmd.Render("bold") + " " + s.Dim.Render("dim") + " " + s.Row.Render(" row ")
	}
	var b strings.Builder
	for _, st := range []lipgloss.Style{s.Element, s.Cmd, s.OK, s.Warn, s.Err, s.Hot, s.Row} {
		b.WriteString(st.Render("██"))
	}
	return b.String()
}

var (
	current      = fallback
	styles       = fallback.Styles()
	StyleOK      = styles.OK
	StyleErr     = styles.Err
	StyleWarn    = styles.Warn
	StyleCmd     = styles.Cmd
	StyleElement = styles.Element
	StyleDim     = styles.Dim
	StyleHot     = styles.Hot
	StyleRow     = styles.Row
	StyleGhost   = styles.Ghost
	StyleBold    = lipgloss.NewStyle().Bold(true)
	StyleDivider = styles.Divider
	StyleSpinner = styles.Spinner

	MarkCheck = StyleOK.SetString("✓")
	MarkCross = StyleErr.SetString("✗")
)

func UseTheme(name string) bool {
	t, ok := ThemeByName(name)
	Apply(t)
	return ok
}

func Apply(t Theme) {
	current = t
	styles = t.Styles()
	StyleOK, StyleErr, StyleWarn = styles.OK, styles.Err, styles.Warn
	StyleCmd, StyleElement, StyleHot = styles.Cmd, styles.Element, styles.Hot
	StyleRow, StyleDim, StyleGhost = styles.Row, styles.Dim, styles.Ghost
	StyleDivider, StyleSpinner = styles.Divider, styles.Spinner
	MarkCheck = StyleOK.SetString("✓")
	MarkCross = StyleErr.SetString("✗")
}

func CurrentTheme() Theme { return current }

func StyleNames() []string {
	return []string{"ok", "err", "warn", "cmd", "element", "hot", "row",
		"divider", "spinner", "dim", "ghost", "bold", "plain"}
}

func StyleNamed(name string) (lipgloss.Style, bool) {
	switch name {
	case "ok":
		return StyleOK, true
	case "err":
		return StyleErr, true
	case "warn":
		return StyleWarn, true
	case "cmd":
		return StyleCmd, true
	case "element":
		return StyleElement, true
	case "hot":
		return StyleHot, true
	case "row":
		return StyleRow, true
	case "divider":
		return StyleDivider, true
	case "spinner":
		return StyleSpinner, true
	case "dim":
		return StyleDim, true
	case "ghost":
		return StyleGhost, true
	case "bold":
		return StyleBold, true
	case "plain":
		return lipgloss.NewStyle(), true
	}
	return lipgloss.NewStyle(), false
}

func StyleTab(kind TabKind, ring, nth int, active bool) lipgloss.Style {
	s := tabStyle(kind, ring, nth)
	if active {
		return s.Reverse(true).Bold(true)
	}
	return s
}

func tabStyle(kind TabKind, ring, nth int) lipgloss.Style {
	if kind == TabList {
		return atTab(styles.Tabs, 0)
	}

	set, at := styles.TabsShell, 0
	if kind == TabData {
		set, at = styles.TabsData, TabFallback(TabData)
	}
	if len(set) == 0 {
		set, nth = styles.Tabs, nth+at
	} else {
		at = 0
	}

	switch current.TabMode {
	case TabByType:
		return atTab(set, at)
	case TabBoth:
		return atTab(set, nth)
	case TabShared:
		return atTab(styles.Tabs, nth)
	}
	return atTab(styles.Tabs, ring)
}

func atTab(set []lipgloss.Style, i int) lipgloss.Style {
	if len(set) == 0 {
		return lipgloss.NewStyle()
	}
	return set[((i%len(set))+len(set))%len(set)]
}

func BorderColor() color.Color { return styles.Border }

func Background() color.Color { return terminalColor(current.Bg) }

func Foreground() color.Color { return terminalColor(current.Fg) }

func terminalColor(spec string) color.Color {
	c, ok := RGBOf(spec)
	if !ok {
		return nil
	}
	return color.RGBA{R: c[0], G: c[1], B: c[2], A: 0xff}
}

var profile colorprofile.Profile

func SetProfile(p colorprofile.Profile) { profile = p }

func Profile() colorprofile.Profile { return profile }

func TrueColor() bool {
	return profile == colorprofile.Unknown || profile >= colorprofile.TrueColor
}

func ProfileName() string {
	switch profile {
	case colorprofile.TrueColor:
		return "truecolor"
	case colorprofile.ANSI256:
		return "256 colors"
	case colorprofile.ANSI:
		return "16 colors"
	case colorprofile.ASCII, colorprofile.NoTTY:
		return "no color"
	}
	return ""
}

func ShouldQueryDepth() bool {
	return current.Deep() && profile != colorprofile.Unknown && profile < colorprofile.TrueColor
}

func ThemeNote(t Theme) string {
	if !t.Deep() || profile == colorprofile.Unknown || profile >= colorprofile.TrueColor {
		return ""
	}
	if n := ProfileName(); n != "" {
		return "this terminal reports " + n + ", so these are approximated"
	}
	return ""
}

func RoleNames() []string {
	return []string{"ok", "err", "warn", "cmd", "element", "row", "hot",
		"border", "divider", "other", "background", "spinner_color"}
}

var roleLabels = map[string]string{
	"ok": "OK", "err": "Err", "warn": "Warn", "cmd": "Cmd",
	"element": "Element", "row": "Row", "hot": "Hot", "border": "Border",
	"divider": "Divider", "other": "Other", "background": "Background",
	"spinner_color": "Spinner Color",
}

func RoleLabel(role string) string {
	if l, ok := roleLabels[role]; ok {
		return l
	}
	return role
}

func (t Theme) Role(name string) string {
	switch name {
	case "ok":
		return t.OK
	case "err":
		return t.Err
	case "warn":
		return t.Warn
	case "cmd":
		return t.Cmd
	case "element":
		return t.Element
	case "row":
		return t.Row
	case "hot":
		return t.Hot
	case "border":
		return t.Border
	case "divider":
		return t.Divider
	case "background":
		return t.Bg
	case "other":
		return t.Fg
	case "spinner_color":
		return t.SpinnerColor
	}
	return ""
}

func (t *Theme) SetRole(name, spec string) error {
	spec = strings.TrimSpace(spec)
	if err := checkColor(spec); err != nil {
		return err
	}
	switch name {
	case "ok":
		t.OK = spec
	case "err":
		t.Err = spec
	case "warn":
		t.Warn = spec
	case "cmd":
		t.Cmd = spec
	case "element":
		t.Element = spec
	case "row":
		t.Row = spec
	case "hot":
		t.Hot = spec
	case "border":
		t.Border = spec
	case "divider":
		t.Divider = spec
	case "background":
		t.Bg = spec
	case "other":
		t.Fg = spec
	case "spinner_color":
		t.SpinnerColor = spec
	default:
		return fmt.Errorf("no color called %q", name)
	}
	return nil
}

func Palette() []string {
	out := make([]string, 0, 64)
	seen := map[string]bool{}
	add := func(c string) {
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		out = append(out, c)
	}
	for i := range 16 {
		add(strconv.Itoa(i))
	}
	for _, t := range Themes() {
		for _, r := range RoleNames() {
			add(t.Role(r))
		}
		for _, c := range t.Tabs {
			add(c)
		}
	}
	return out
}

func NextInPalette(spec string, delta int) string {
	p := Palette()
	at := 0
	for i, c := range p {
		if c == spec {
			at = i
			break
		}
	}
	return p[((at+delta)%len(p)+len(p))%len(p)]
}

func NextSpinner(name string, delta int) string {
	names := SpinnerNames()
	at := 0
	for i, n := range names {
		if n == name {
			at = i
			break
		}
	}
	return names[((at+delta)%len(names)+len(names))%len(names)]
}

func SaveUserTheme(path string, t Theme) error {
	if path == "" {
		return fmt.Errorf("no themes file to write to")
	}
	if err := t.validate(); err != nil {
		return err
	}

	if !pathIsFile(path) {
		return saveThemeFile(path, t)
	}

	set := themeSet{}
	if b, err := os.ReadFile(path); err == nil {
		existing, perr := ParseThemes(b)
		if perr != nil {
			return fmt.Errorf("%s will not parse, so it was left alone: %w", filepath.Base(path), perr)
		}
		set.Themes = existing
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("read %s: %w", path, err)
	}

	if i := indexOf(set.Themes, t.Name); i >= 0 {
		set.Themes[i] = t
	} else {
		set.Themes = append(set.Themes, t)
	}

	b, err := yaml.Marshal(set)
	if err != nil {
		return fmt.Errorf("encode themes: %w", err)
	}
	header := "# Written by iontk's theme editor. A scheme here replaces the built-in\n" +
		"# one of the same name; anything not set falls back to no color.\n"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(header), b...), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return LoadThemes(path)
}

func BuiltinTheme(name string) (Theme, bool) {
	set, err := builtin()
	if err != nil {
		return fallback, strings.EqualFold(name, fallback.Name)
	}
	if i := indexOf(set, name); i >= 0 {
		return set[i], true
	}
	return Theme{}, false
}

func (t Theme) Clone() Theme {
	t.Tabs = slices.Clone(t.Tabs)
	t.TabShell = slices.Clone(t.TabShell)
	t.TabData = slices.Clone(t.TabData)
	return t
}

func (t Theme) Equal(o Theme) bool {
	if t.Name != o.Name || t.Blurb != o.Blurb || t.Mono != o.Mono ||
		t.Spinner != o.Spinner || t.Animation != o.Animation ||
		t.TabMode != o.TabMode || !slices.Equal(t.Tabs, o.Tabs) ||
		!slices.Equal(t.TabShell, o.TabShell) || !slices.Equal(t.TabData, o.TabData) {
		return false
	}
	for _, role := range RoleNames() {
		if t.Role(role) != o.Role(role) {
			return false
		}
	}
	return true
}

func ColorStyle(spec string) lipgloss.Style {
	if spec == "" {
		return lipgloss.NewStyle()
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(spec))
}

func (t Theme) RoleStyle(role string) lipgloss.Style {
	if t.Mono {
		return t.Styles().Dim
	}
	return t.fg(t.Role(role))
}

const themeFileHeader = "# Written by iontk's theme editor. One scheme to a file; a scheme here\n" +
	"# replaces the built-in of the same name, and deleting the file puts it back.\n"

func ThemeYAML(t Theme) (string, error) {
	b, err := yaml.Marshal(t)
	if err != nil {
		return "", fmt.Errorf("encode %s: %w", t.Name, err)
	}
	return themeFileHeader + string(b), nil
}

func saveThemeFile(dir string, t Theme) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("make %s: %w", dir, err)
	}
	b, err := yaml.Marshal(t)
	if err != nil {
		return fmt.Errorf("encode %s: %w", t.Name, err)
	}

	path := filepath.Join(dir, ThemeFileName(t.Name))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(themeFileHeader), b...), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return LoadThemes(dir)
}

func DeleteUserTheme(path, name string) error {
	file, ok := userThemeSource(path, name)
	if !ok {
		return fmt.Errorf("%s has no file of yours to delete", name)
	}

	b, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(file), err)
	}
	existing, err := ParseThemes(b)
	if err != nil {
		return fmt.Errorf("%s will not parse, so it was left alone: %w", filepath.Base(file), err)
	}

	i := indexOf(existing, name)
	if i < 0 {
		return fmt.Errorf("%s has no file of yours to delete", name)
	}
	rest := slices.Delete(slices.Clone(existing), i, i+1)

	if len(rest) == 0 {
		if err := os.Remove(file); err != nil {
			return fmt.Errorf("remove %s: %w", filepath.Base(file), err)
		}
		return LoadThemes(path)
	}

	out, err := yaml.Marshal(themeSet{Themes: rest})
	if err != nil {
		return fmt.Errorf("encode themes: %w", err)
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(themeFileHeader), out...), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, file); err != nil {
		return fmt.Errorf("write %s: %w", file, err)
	}
	return LoadThemes(path)
}

func RemoveOverrides(path string) (int, error) {
	shipped, err := builtin()
	if err != nil {
		return 0, err
	}
	var n int
	for _, t := range shipped {
		if !IsUserTheme(path, t.Name) {
			continue
		}
		if derr := DeleteUserTheme(path, t.Name); derr != nil {
			return n, derr
		}
		n++
	}
	return n, nil
}

func UserThemePath(path, name string) (string, bool) {
	if path == "" {
		return "", false
	}
	if file, ok := userThemeSource(path, name); ok {
		return file, true
	}
	if pathIsFile(path) {
		return path, true
	}
	return filepath.Join(path, ThemeFileName(name)), true
}

func IsUserTheme(path, name string) bool {
	_, ok := userThemeSource(path, name)
	return ok
}

func userThemeSource(path, name string) (string, bool) {
	if path == "" {
		return "", false
	}
	files, err := userThemeFiles(path)
	if err != nil {
		return "", false
	}
	for _, f := range files {
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			continue
		}
		set, perr := ParseThemes(b)
		if perr != nil {
			continue
		}
		if indexOf(set, name) >= 0 {
			return f, true
		}
	}
	return "", false
}
