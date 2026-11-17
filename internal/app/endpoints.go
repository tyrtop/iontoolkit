package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
	"tyrtop.com/iontk/internal/endpoint"
	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/workflow"
)

type listedEndpoint struct {
	Name   string `yaml:"name"`
	Method string `yaml:"method,omitempty"`
	Host   string `yaml:"host,omitempty"`
	Path   string `yaml:"path"`
	In     string `yaml:"in,omitempty"`
	From   string `yaml:"from"`
}

func endpointsCommand() runtime.Command {
	return runtime.Command{
		Name: "endpoints.list", Help: "the endpoints iontk knows by name, or with workflow= the ones that workflow declares itself",
		Args: []runtime.Arg{
			{Name: "workflow", Help: "Workflow name, or the path to one"},
			{Name: "q", Help: "Only names or paths holding this text"},
		},
		Run: func(rt *runtime.Runtime, c *runtime.Call) error {
			var out []listedEndpoint
			if name := c.Arg("workflow"); name != "" {
				w, err := findWorkflow(rt, name)
				if err != nil {
					return err
				}
				for _, e := range w.Calls() {
					out = append(out, listedEndpoint{Name: e.Name, Method: e.Method, Host: e.Host, Path: e.Path, In: e.In, From: w.From(e.Name)})
				}
			} else {
				m := endpoint.Master()
				for _, e := range m.All() {
					out = append(out, listedEndpoint{Name: e.Name, Method: e.Method, Path: e.Path, From: m.Name})
				}
			}
			if q := strings.ToLower(c.Arg("q")); q != "" {
				kept := out[:0]
				for _, e := range out {
					if strings.Contains(strings.ToLower(e.Name), q) || strings.Contains(strings.ToLower(e.Path), q) {
						kept = append(kept, e)
					}
				}
				out = kept
			}
			b, err := yaml.Marshal(out)
			if err != nil {
				return err
			}
			for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
				c.Say("%s", line)
			}
			return nil
		},
	}
}

func findWorkflow(rt *runtime.Runtime, name string) (workflow.Workflow, error) {
	if strings.ContainsAny(name, `/\`) {
		return workflow.LoadOne(name)
	}
	all, err := LoadWorkflows(rt.Store(), WorkflowsFolder)
	for _, p := range WorkflowPaths {
		if w, perr := workflow.LoadOne(p); perr == nil {
			all = append(all, w)
		}
	}
	for _, w := range all {
		if w.Name == name {
			return w, nil
		}
	}
	if err != nil {
		return workflow.Workflow{}, fmt.Errorf("no workflow called %s loaded: %w", name, err)
	}
	if _, serr := os.Stat(filepath.Join(name, workflow.ManifestName)); serr == nil {
		return workflow.LoadOne(name)
	}
	return workflow.Workflow{}, fmt.Errorf("no workflow is called %s", name)
}
