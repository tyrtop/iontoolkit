package app

import (
	"fmt"
	"path/filepath"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/script"
)

var ActionsFolder string

const ActionPrefix = "action."

const ActionsRefused = "lua action refused: "

func ActionsPath(st *config.Store) string {
	if ActionsFolder != "" {
		return ActionsFolder
	}
	if st == nil {
		return ""
	}
	return filepath.Join(st.Root, script.DirName)
}

func LoadActions(st *config.Store) (*script.Set, error) {
	return script.Load(ActionsPath(st))
}

var ActionSets = map[string]func(rt *runtime.Runtime, v string) error{
	"theme":  func(rt *runtime.Runtime, v string) error { return rt.Set("theme", v) },
	"jump":   func(rt *runtime.Runtime, v string) error { return rt.Set("page_step", v) },
	"mode":   func(rt *runtime.Runtime, v string) error { return rt.Run("list.mode", map[string]string{"mode": v}) },
	"filter": func(rt *runtime.Runtime, v string) error { return rt.Run("list.filter", map[string]string{"query": v}) },
}

func actionCommand(set *script.Set, name string) runtime.Command {
	return runtime.Command{
		Name: ActionPrefix + name, Help: "lua action " + filepath.Base(set.Source(name)), Slow: true,
		Args: []runtime.Arg{{Name: "screen", Help: "the screen it is run from, for ctx.state.screen"}},
		Run: func(rt *runtime.Runtime, c *runtime.Call) error {
			run := script.Run{
				Name:     name,
				State:    rt.ActionState(c.Arg("screen")),
				Export:   func(text, slug, ext string) (string, error) { return rt.Export(slug, ext, text) },
				OnStatus: func(line string) { c.Say("%s", line) },
				Exec:     scriptExec(rt, c, false),
			}
			effects, err := set.Run(c.Ctx, run)
			c.Result = effects
			for _, e := range effects {
				switch e.Kind {
				case script.EffectExport:
					c.Say("exported to %s", e.Value)
				case script.EffectSet:
					apply, ok := ActionSets[e.Field]
					if !ok {
						return fmt.Errorf("%s: %q is not something to set", name, e.Field)
					}
					if serr := apply(rt, e.Value); serr != nil {
						return fmt.Errorf("%s: %s", name, serr)
					}
				}
			}
			return err
		},
	}
}
