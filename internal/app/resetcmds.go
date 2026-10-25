package app

import (
	"fmt"
	"slices"

	"tyrtop.com/iontk/internal/config"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/ui"
)

func resetCommands() []runtime.Command {
	return []runtime.Command{
		{
			Name: "reset", Help: "forget everything iontk saved: tenants, sign-ins, caches, themes, settings",
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				st := rt.Store()
				if st == nil {
					return fmt.Errorf("no config to reset")
				}
				rt.Sessions().CloseAll()
				if err := RemoveThemes(st); err != nil {
					return err
				}
				ui.HideThemes(nil)
				themeErr := ui.LoadThemes(ThemesPath(st))
				if err := st.Reset(); err != nil {
					return err
				}
				rt.UseConfig(st, &config.Config{})
				rt.ClearTenant()
				rt.SetAuthMode(runtime.AuthBrowser)
				rt.ResetClients()
				rt.ResetCells()
				rt.ResetHA()
				rt.SetElements(nil)
				rt.ClearSites()
				rt.ForgetSelection()
				rt.ForgetRunAs()
				rt.ClearIssues()
				rt.ForgetAuth()
				rt.ResetFleet()
				c.Say("reset — nothing is saved any more")
				return themeErr
			},
		},
		{
			Name: "theme.hide", Help: "hide a shipped scheme, or delete one of your own: name=",
			Args: []runtime.Arg{{Name: "name", Required: true}},
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				cfg, st, name := rt.Config(), rt.Store(), c.Arg("name")
				if cfg == nil || st == nil {
					return fmt.Errorf("no config to change")
				}
				path := ThemesPath(st)
				if ui.IsUserTheme(path, name) {
					if err := ui.DeleteUserTheme(path, name); err != nil {
						return err
					}
					if _, still := ui.ThemeByName(name); still {
						c.Say("%s is back to the one iontk ships", name)
					} else {
						c.Say("%s is gone", name)
					}
				} else {
					cfg.HiddenThemes = append(slices.Clone(cfg.HiddenThemes), name)
					ui.HideThemes(cfg.HiddenThemes)
					c.Say("%s is hidden · settings → Reset brings it back", name)
				}
				if _, still := ui.ThemeByName(cfg.Theme); !still || ui.IsHidden(cfg.Theme) {
					cfg.Theme = ui.Default().Name
					c.Say("so %s is in use", cfg.Theme)
				}
				return st.SaveConfig(cfg)
			},
		},
		{
			Name: "theme.restore", Help: "bring back every shipped scheme: your overrides removed, nothing hidden",
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				cfg, st := rt.Config(), rt.Store()
				if cfg == nil || st == nil {
					return fmt.Errorf("no config to change")
				}
				n, err := ui.RemoveOverrides(ThemesPath(st))
				if err != nil {
					return err
				}
				hidden := len(cfg.HiddenThemes)
				cfg.HiddenThemes = nil
				ui.HideThemes(nil)
				if now, ok := ui.ThemeByName(cfg.Theme); !ok {
					cfg.Theme = now.Name
				}
				if err := st.SaveConfig(cfg); err != nil {
					return err
				}
				c.Say("the shipped schemes are back · %d override(s) removed, %d unhidden", n, hidden)
				return nil
			},
		},
		{
			Name: "theme.save", Help: "save one colour scheme of your own and use it: text= its YAML",
			Args:      []runtime.Arg{{Name: "text", Required: true}},
			FrontOnly: true,
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				st := rt.Store()
				if st == nil {
					return fmt.Errorf("no config to save a scheme to")
				}
				themes, err := ui.ParseThemes([]byte(c.Arg("text")))
				if err != nil {
					return err
				}
				if len(themes) != 1 {
					return fmt.Errorf("theme.save takes one scheme, and this text has %d", len(themes))
				}
				if err := ui.SaveUserTheme(ThemesPath(st), themes[0]); err != nil {
					return err
				}
				if err := ui.LoadThemes(ThemesPath(st)); err != nil {
					return err
				}
				return rt.Set("theme", themes[0].Name)
			},
		},
	}
}
