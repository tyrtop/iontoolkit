package ui

import (
	"embed"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"gopkg.in/yaml.v3"
)

//go:embed themes.yaml
var builtinFS embed.FS

const ThemesFileName = "themes.yaml"

// colour per role, not per widget: nothing here asks for "blue"
type Theme struct {
	Name  string `yaml:"name"`
	Blurb string `yaml:"blurb"`

	OK      string `yaml:"ok"`
	Err     string `yaml:"err"`
	Warn    string `yaml:"warn"`
	Cmd     string `yaml:"cmd"`
	Element string `yaml:"element"`
	Row     string `yaml:"row"`
	Hot     string `yaml:"hot"`
	Border  string `yaml:"border"`
	Divider string `yaml:"divider"`
	// the spinner is part of the scheme too
	Spinner      string   `yaml:"spinner"`
	SpinnerColor string   `yaml:"spinner_color"`
	Tabs         []string `yaml:"tabs"`

	// attributes instead of colour: roles still have to differ
	Mono bool `yaml:"mono"`
}

// Dim and Ghost are attributes: a fixed grey is wrong on half of
// the backgrounds there are.
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

	Border color.Color
	Tabs   []lipgloss.Style
}

// something has to draw if themes.yaml breaks
var fallback = Theme{
	Name:  "classic",
	Blurb: "your terminal's own 16 colours",
	OK:    "2", Err: "1", Warn: "3", Cmd: "6", Element: "4",
	Row: "5", Hot: "9", Border: "3", Divider: "8",
	Spinner: DefaultSpinner, SpinnerColor: "5",
	Tabs: []string{"7", "4", "6", "5", "12", "14", "13"},
}

var (
	loaded []Theme
	// only ever a themes.yaml that would not parse
	loadErr string
)

type themeSet struct {
	Themes []Theme `yaml:"themes"`
}

// picker order, default first
func Themes() []Theme {
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

// a broken file leaves the built-ins; a missing one is normal
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
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		loadErr = fmt.Sprintf("read %s: %s", filepath.Base(path), err)
		return fmt.Errorf("read %s: %w", path, err)
	}
	user, err := ParseThemes(b)
	if err != nil {
		loadErr = fmt.Sprintf("%s: %s", filepath.Base(path), err)
		return fmt.Errorf("%s: %w", filepath.Base(path), err)
	}

	for _, t := range user {
		if i := indexOf(loaded, t.Name); i >= 0 {
			loaded[i] = t
			continue
		}
		loaded = append(loaded, t)
	}
	return nil
}

func ParseThemes(b []byte) ([]Theme, error) {
	var set themeSet
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	// a misspelled key otherwise looks like the scheme not working
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

// lipgloss renders an unparseable colour as nothing, not as wrong
func (t Theme) validate() error {
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("a theme needs a name")
	}
	for _, role := range [][2]string{
		{"ok", t.OK}, {"err", t.Err}, {"warn", t.Warn}, {"cmd", t.Cmd},
		{"element", t.Element}, {"row", t.Row}, {"hot", t.Hot},
		{"border", t.Border}, {"divider", t.Divider},
		{"spinner_color", t.SpinnerColor},
	} {
		if err := checkColour(role[1]); err != nil {
			return fmt.Errorf("theme %s: %s: %w", t.Name, role[0], err)
		}
	}
	for i, c := range t.Tabs {
		if err := checkColour(c); err != nil {
			return fmt.Errorf("theme %s: tab %d: %w", t.Name, i+1, err)
		}
	}
	if t.Spinner != "" && !KnownSpinner(t.Spinner) {
		return fmt.Errorf("theme %s: spinner %q is not one of %s",
			t.Name, t.Spinner, strings.Join(SpinnerNames(), ", "))
	}
	return nil
}

func checkColour(spec string) error {
	switch {
	case spec == "":
		return nil
	case strings.HasPrefix(spec, "#"):
		if len(spec) != 4 && len(spec) != 7 {
			return fmt.Errorf("%q is not #rgb or #rrggbb", spec)
		}
		if _, err := strconv.ParseUint(spec[1:], 16, 32); err != nil {
			return fmt.Errorf("%q is not a hex colour", spec)
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

// built once, so no view asks the theme anything at render time
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
		// StyleTab reverses the active one
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
	if len(s.Tabs) == 0 {
		s.Tabs = []lipgloss.Style{lipgloss.NewStyle()}
	}
	return s
}

// outside ANSI 0-15, so it depends on the terminal's depth
func (t Theme) Deep() bool {
	for _, c := range append([]string{t.OK, t.Err, t.Warn, t.Cmd, t.Element,
		t.Row, t.Hot, t.Border, t.Divider, t.SpinnerColor}, t.Tabs...) {
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

// One process, one theme, so package state rather than threaded through
// every view. Written from Update, read by the next render.
var (
	current  = fallback
	styles   = fallback.Styles()
	StyleOK  = styles.OK
	StyleErr = styles.Err
	// StyleWarn is a thing worth noticing that is not a failure: an offline
	// ION next to a live session, a tenant that keeps refusing.
	StyleWarn    = styles.Warn
	StyleCmd     = styles.Cmd
	StyleElement = styles.Element
	StyleDim     = styles.Dim
	// StyleHot is a session about to go.
	StyleHot = styles.Hot
	// StyleRow is the highlight on the row a list is on, and on the line an
	// open result is scrolled to. Defined here rather than taken from the
	// table widget's default, which is a 256-colour pink a terminal theme
	// cannot remap.
	StyleRow = styles.Row
	// StyleGhost is a line that is not there yet: the next command a macro
	// would take, or the macro that would be made, before anyone has typed
	// anything.
	StyleGhost   = styles.Ghost
	StyleDivider = styles.Divider
	// Set into the bubbles widget when a spinner is made, so a theme change
	// has to make it again.
	StyleSpinner = styles.Spinner

	MarkCheck = StyleOK.SetString("✓")
	MarkCross = StyleErr.SetString("✗")
)

// unknown name lands on the default: a stale preference still draws
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

// reversed, not recoloured: legible in a scheme with few colours
func StyleTab(i int, active bool) lipgloss.Style {
	s := styles.Tabs[i%len(styles.Tabs)]
	if active {
		return s.Reverse(true).Bold(true)
	}
	return s
}

func BorderColor() color.Color { return styles.Border }

// the renderer downsamples for us; this only says a scheme is approximated
var profile colorprofile.Profile

func SetProfile(p colorprofile.Profile) { profile = p }

func Profile() colorprofile.Profile { return profile }

func ProfileName() string {
	switch profile {
	case colorprofile.TrueColor:
		return "truecolor"
	case colorprofile.ANSI256:
		return "256 colours"
	case colorprofile.ANSI:
		return "16 colours"
	case colorprofile.ASCII, colorprofile.NoTTY:
		return "no colour"
	}
	return ""
}

// XTGETTCAP corrupts the frame on some terminals, so only a deep scheme asks
func ShouldQueryDepth() bool {
	return current.Deep() && profile != colorprofile.Unknown && profile < colorprofile.TrueColor
}

// empty unless a deep scheme is being approximated here
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
	return []string{"ok", "err", "warn", "cmd", "element", "row", "hot", "border", "divider", "spinner_color"}
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
	case "spinner_color":
		return t.SpinnerColor
	}
	return ""
}

// an unparseable colour would store as invisible
func (t *Theme) SetRole(name, spec string) error {
	spec = strings.TrimSpace(spec)
	if err := checkColour(spec); err != nil {
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
	case "spinner_color":
		t.SpinnerColor = spec
	default:
		return fmt.Errorf("no colour called %q", name)
	}
	return nil
}

// what left/right cycles through
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

// a typed colour is not in the palette, so it starts at the top
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

// editing a built-in writes an override; the file's comments do not survive
func SaveUserTheme(path string, t Theme) error {
	if path == "" {
		return fmt.Errorf("no themes file to write to")
	}
	if err := t.validate(); err != nil {
		return err
	}

	set := themeSet{}
	if b, err := os.ReadFile(path); err == nil {
		existing, perr := ParseThemes(b)
		if perr != nil {
			// the file is the user's, maybe half-written by hand
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
		"# one of the same name; anything not set falls back to no colour.\n"
	// the editor writes on every keypress
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append([]byte(header), b...), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return LoadThemes(path)
}

// the editor's way back from an override
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

func (t Theme) RoleStyle(role string) lipgloss.Style {
	if t.Mono {
		return t.Styles().Dim
	}
	return t.fg(t.Role(role))
}
