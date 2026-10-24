package runtime

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/output"
)

const (
	OptBool   = "bool"
	OptInt    = "int"
	OptString = "string"
	OptChoice = "choice"

	AreaOptions = "options"
)

type Option struct {
	Name    string
	Help    string
	Kind    string
	Choices []string
	Min     int
	Max     int

	Get   func(c *config.Config) string
	Put   func(c *config.Config, v string)
	Check func(v string) (string, error)
	After func(rt *Runtime, v string)
}

func (rt *Runtime) UseConfig(st *config.Store, cfg *config.Config) {
	rt.mu.Lock()
	rt.store, rt.cfg = st, cfg
	rt.mu.Unlock()
	rt.changed(Event{Area: AreaOptions})
}

func (rt *Runtime) RegisterOption(o Option) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.opts[o.Name] = o
}

func (rt *Runtime) Options() []Option {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make([]Option, 0, len(rt.opts))
	for _, o := range rt.opts {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (rt *Runtime) Get(name string) (string, error) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	o, ok := rt.opts[name]
	if !ok {
		return "", fmt.Errorf("no option is called %q", name)
	}
	if rt.cfg == nil {
		return o.Get(&config.Config{}), nil
	}
	return o.Get(rt.cfg), nil
}

func (rt *Runtime) Set(name, value string) error {
	return rt.Run("set", map[string]string{"name": name, "value": value})
}

func (rt *Runtime) set(name, value string) (string, error) {
	rt.mu.Lock()
	o, ok := rt.opts[name]
	if !ok {
		rt.mu.Unlock()
		return "", fmt.Errorf("no option is called %q; options lists them", name)
	}
	if rt.cfg == nil {
		rt.mu.Unlock()
		return "", fmt.Errorf("set %s: no config to change", name)
	}
	v, err := o.resolve(o.Get(rt.cfg), value)
	if err != nil {
		rt.mu.Unlock()
		return "", fmt.Errorf("set %s: %w", name, err)
	}
	o.Put(rt.cfg, v)
	st, cfg := rt.store, rt.cfg
	rt.mu.Unlock()

	if st != nil {
		if err := st.SaveConfig(cfg); err != nil {
			return v, fmt.Errorf("set %s: saved in memory, not to disk: %w", name, err)
		}
	}
	if o.After != nil {
		o.After(rt, v)
	}
	rt.changed(Event{Area: AreaOptions, Status: name})
	return v, nil
}

func (o Option) resolve(cur, typed string) (string, error) {
	typed = strings.TrimSpace(typed)
	if typed == "next" && len(o.Choices) > 0 {
		next := o.Choices[0]
		for i, c := range o.Choices {
			if c == cur {
				next = o.Choices[(i+1)%len(o.Choices)]
				break
			}
		}
		typed = next
	}
	switch o.Kind {
	case OptBool:
		if typed == "" {
			b, _ := strconv.ParseBool(cur)
			return strconv.FormatBool(!b), nil
		}
		switch strings.ToLower(typed) {
		case "true", "on", "yes", "1":
			return "true", nil
		case "false", "off", "no", "0":
			return "false", nil
		}
		return "", fmt.Errorf("%q is not true or false", typed)
	case OptInt:
		n, err := strconv.Atoi(typed)
		if err != nil {
			return "", fmt.Errorf("%q is not a whole number", typed)
		}
		if (o.Min != 0 || o.Max != 0) && (n < o.Min || n > o.Max) {
			return "", fmt.Errorf("%d is outside %d–%d", n, o.Min, o.Max)
		}
		typed = strconv.Itoa(n)
	case OptChoice:
		if o.Check == nil {
			for _, c := range o.Choices {
				if strings.EqualFold(c, typed) {
					return c, nil
				}
			}
			return "", fmt.Errorf("%q is not one of %s", typed, strings.Join(o.Choices, ", "))
		}
	}
	if o.Check != nil {
		return o.Check(typed)
	}
	return typed, nil
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func boolText(b bool) string { return strconv.FormatBool(b) }

func intOr(n, def int) string {
	if n > 0 {
		return strconv.Itoa(n)
	}
	return strconv.Itoa(def)
}

const (
	DefaultPageStep    = 10
	DefaultSessionCols = 200
	DefaultSessionRows = 50
)

var PageSteps = []string{"5", "10", "20", "25", "50"}

func builtinOptions() []Option {
	return []Option{
		{
			Name: "allow_writes", Help: "device CLI commands that are not read-only may run", Kind: OptBool,
			Get: func(c *config.Config) string { return boolText(c.AllowWrites) },
			Put: func(c *config.Config, v string) { c.AllowWrites = v == "true" },
		},
		{
			Name: "default_mode", Help: "what the element list opens in", Kind: OptChoice, Choices: modes,
			Get: func(c *config.Config) string {
				for _, m := range modes {
					if m == c.DefaultMode {
						return m
					}
				}
				return ModeShell
			},
			Put:   func(c *config.Config, v string) { c.DefaultMode = v },
			After: func(rt *Runtime, v string) { rt.SetMode(v) },
		},
		{
			Name: "page_step", Help: "how far a jump moves in the element list", Kind: OptInt,
			Choices: PageSteps, Min: 1, Max: 1000,
			Get: func(c *config.Config) string { return intOr(c.PageStep, DefaultPageStep) },
			Put: func(c *config.Config, v string) { c.PageStep = atoi(v) },
		},
		{
			Name: "session_cols", Help: "the width a device session is opened at", Kind: OptInt, Min: 40, Max: 400,
			Get: func(c *config.Config) string { return intOr(c.SessionCols, DefaultSessionCols) },
			Put: func(c *config.Config, v string) { c.SessionCols = atoi(v) },
		},
		{
			Name: "session_rows", Help: "the height a device session is opened at", Kind: OptInt, Min: 10, Max: 200,
			Get: func(c *config.Config) string { return intOr(c.SessionRows, DefaultSessionRows) },
			Put: func(c *config.Config, v string) { c.SessionRows = atoi(v) },
		},
		{
			Name: "batch_format", Help: "how batch results are written", Kind: OptChoice, Choices: output.Formats(),
			Get: func(c *config.Config) string {
				f, err := output.NormalizeFormat(c.BatchFormat)
				if err != nil {
					return output.FormatJSON
				}
				return f
			},
			Put:   func(c *config.Config, v string) { c.BatchFormat = v },
			Check: output.NormalizeFormat,
		},
		{
			Name: "batch_orient", Help: "row order for table and csv results", Kind: OptChoice, Choices: output.Orientations(),
			Get: func(c *config.Config) string {
				o, err := output.NormalizeOrientation(c.BatchOrient)
				if err != nil {
					return output.OrientION
				}
				return o
			},
			Put:   func(c *config.Config, v string) { c.BatchOrient = v },
			Check: output.NormalizeOrientation,
		},
		{
			Name: "batch_to_file", Help: "batch results go to a file in batch_dir rather than the screen", Kind: OptBool,
			Get: func(c *config.Config) string { return boolText(c.BatchToFile) },
			Put: func(c *config.Config, v string) { c.BatchToFile = v == "true" },
		},
		{
			Name: "batch_dir", Help: "the folder batch results are written to", Kind: OptString,
			Get: func(c *config.Config) string { return c.BatchDir },
			Put: func(c *config.Config, v string) { c.BatchDir = v },
		},
	}
}

func optionCommands() []Command {
	return []Command{
		{
			Name: "set", Help: "change an option: set name value; a bool with no value toggles, next steps a choice",
			Args: []Arg{{Name: "name"}, {Name: "value"}},
			Run: func(rt *Runtime, c *Call) error {
				name, value := c.Arg("name"), c.Arg("value")
				words := c.Rest
				if name == "" && len(words) > 0 {
					name, words = words[0], words[1:]
				}
				if value == "" && len(words) > 0 {
					value = strings.Join(words, " ")
				}
				if name == "" {
					return fmt.Errorf("set: which option? options lists them")
				}
				v, err := rt.set(name, value)
				if err != nil {
					return err
				}
				c.Say("%s = %s", name, v)
				return nil
			},
		},
		{
			Name: "get", Help: "an option's value", Args: []Arg{{Name: "name"}},
			Run: func(rt *Runtime, c *Call) error {
				name := c.Arg("name")
				if name == "" && len(c.Rest) > 0 {
					name = c.Rest[0]
				}
				v, err := rt.Get(name)
				if err != nil {
					return err
				}
				c.Say("%s = %s", name, v)
				return nil
			},
		},
		{
			Name: "options", Help: "every option with its value",
			Run: func(rt *Runtime, c *Call) error {
				for _, o := range rt.Options() {
					v, _ := rt.Get(o.Name)
					c.Say("%-18s %-12s %s", o.Name, v, o.Help)
				}
				return nil
			},
		},
	}
}
