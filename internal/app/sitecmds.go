package app

import (
	"encoding/json"
	"fmt"
	"os"

	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/script"
	"tyrtop.com/iontk/internal/siteconfig"
)

type portalAdapter struct {
	get  func(string, map[string]string) (any, error)
	send func(string, string, any, map[string]string) (any, error)
}

func (s portalAdapter) Get(path string) (any, error) { return s.get(path, nil) }

func (s portalAdapter) Send(method, path string, body any) (any, error) {
	return s.send(method, path, body, nil)
}

type SitePush struct {
	Writes []script.Write
	DoIt   bool
}

func (r SitePush) PlannedWrites() []script.Write { return r.Writes }

func siteCommands() []runtime.Command {
	return []runtime.Command{
		{
			Name: "site.pull", Slow: true,
			Help: "read a site and its elements as a model keyed by name: site= to= writes it to a file",
			Args: []runtime.Arg{{Name: "site", Required: true, Help: "the site's name"}, {Name: "to", Help: "file to write the model to, as JSON"}},
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				p, _, err := rt.Portal(c.Ctx)
				if err != nil {
					return err
				}
				sp := script.Portal{Ctx: api.Patient(c.Ctx), Client: p}
				model, err := siteconfig.Pull(portalAdapter{get: sp.Get()}, c.Arg("site"), func(l string) { c.Say("%s", l) })
				if err != nil {
					return err
				}
				c.Result = model
				if to := c.Arg("to"); to != "" {
					b, err := json.MarshalIndent(model, "", "  ")
					if err != nil {
						return err
					}
					if err := os.WriteFile(to, append(b, '\n'), 0o600); err != nil {
						return err
					}
					c.Say("written to %s", to)
				}
				return nil
			},
		},
		{
			Name: "site.push", Slow: true, Writes: true,
			Help: "make a site match a model: its routers claimed, upgraded, assigned and configured, in do_site's order",
			Args: []runtime.Arg{{Name: "model", Required: true, Help: "the site's model, as site.pull writes it: JSON"}},
			Run: func(rt *runtime.Runtime, c *runtime.Call) error {
				var model map[string]any
				if err := json.Unmarshal([]byte(c.Arg("model")), &model); err != nil {
					return fmt.Errorf("model: %w", err)
				}
				p, tenant, err := rt.Portal(c.Ctx)
				if err != nil {
					return err
				}
				res := SitePush{DoIt: c.DoIt}
				sp := script.Portal{Ctx: api.Patient(c.Ctx), Client: p, Guard: guardFor(rt, tenant), What: "site.push",
					DoIt: c.DoIt, Planned: &res.Writes}
				err = siteconfig.Push(c.Ctx, portalAdapter{get: sp.Get(), send: sp.Send()}, model,
					siteconfig.Options{DoIt: c.DoIt, Say: func(l string) { c.Say("%s", l) }})
				c.Result = res
				for _, l := range res.lines() {
					c.Say("%s", l)
				}
				return err
			},
		},
	}
}

func (r SitePush) lines() []string {
	verb := "planned"
	if r.DoIt {
		verb = "sent"
	}
	out := []string{"", fmt.Sprintf("%d write(s) %s:", len(r.Writes), verb)}
	for _, w := range r.Writes {
		out = append(out, "  "+w.Method+" "+w.Path)
		if w.Body != nil {
			b, _ := json.Marshal(w.Body)
			out = append(out, "    "+string(b))
		}
	}
	if !r.DoIt {
		out = append(out, "", "dry run: nothing sent — add --doit to send")
	}
	return out
}
