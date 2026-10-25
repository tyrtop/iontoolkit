package workflow

import (
	"bytes"
	"context"
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"tyrtop.com/iontk/internal/endpoint"
	"tyrtop.com/iontk/internal/script"
)

const (
	DirName      = "workflows"
	ManifestName = "workflow.yaml"
	defaultEntry = "main"

	DefaultBudget = 30 * time.Minute
)

type Workflow struct {
	Name        string  `yaml:"-"`
	Title       string  `yaml:"title"`
	Description string  `yaml:"description,omitempty"`
	Inputs      []Input `yaml:"inputs,omitempty"`
	Entry       string  `yaml:"entry,omitempty"`
	Budget      string  `yaml:"budget,omitempty"`

	Endpoints []endpoint.Endpoint `yaml:"endpoints,omitempty"`

	Dir    string        `yaml:"-"`
	budget time.Duration `yaml:"-"`
	set    *script.Set   `yaml:"-"`
	calls  *endpoint.Set `yaml:"-"`
}

type Input struct {
	Name     string `yaml:"name"`
	Prompt   string `yaml:"prompt,omitempty"`
	Default  string `yaml:"default,omitempty"`
	Required bool   `yaml:"required,omitempty"`
	Pattern  string `yaml:"pattern,omitempty"`

	re *regexp.Regexp `yaml:"-"`
}

func Load(dir string) ([]Workflow, error) {
	if dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	var out []Workflow
	var bad []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		w, err := LoadOne(filepath.Join(dir, e.Name()))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			bad = append(bad, err.Error())
			continue
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	if len(bad) > 0 {
		return out, fmt.Errorf("%s", strings.Join(bad, "; "))
	}
	return out, nil
}

func LoadOne(dir string) (Workflow, error) {
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return Workflow{}, err
	}
	return Parse(dir, b)
}

func Parse(dir string, b []byte) (Workflow, error) {
	name := filepath.Base(dir)
	var err error
	var w Workflow
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&w); err != nil {
		return Workflow{}, fmt.Errorf("%s/%s: %w", name, ManifestName, err)
	}
	w.Name, w.Dir = name, dir
	if err := w.normalize(); err != nil {
		return Workflow{}, fmt.Errorf("%s: %w", name, err)
	}
	if w.calls, err = endpoint.Build(name, "", nil, w.Endpoints); err != nil {
		return Workflow{}, fmt.Errorf("%s/%s: %w", name, ManifestName, err)
	}
	for _, e := range w.Endpoints {
		err := e.SDWAN()
		if err == nil && strings.EqualFold(e.Method, "WS") {
			err = fmt.Errorf("%s opens a session; a workflow calls, it does not dial", e.Name)
		}
		if err != nil {
			return Workflow{}, fmt.Errorf("%s/%s: %w", name, ManifestName, err)
		}
	}
	set, err := script.LoadPackage(dir)
	if err != nil {
		return Workflow{}, fmt.Errorf("%s: %w", name, err)
	}
	if !set.Has(w.Entry) {
		return Workflow{}, fmt.Errorf("%s: no %s.lua to start from", name, w.Entry)
	}
	w.set = set
	return w, nil
}

func (w Workflow) Endpoint(name string) (endpoint.Endpoint, bool) {
	if w.calls == nil {
		return endpoint.Endpoint{}, false
	}
	e, err := w.calls.Get(name)
	return e, err == nil
}

func (w *Workflow) normalize() error {
	if w.Title == "" {
		w.Title = w.Name
	}
	if w.Entry == "" {
		w.Entry = defaultEntry
	}
	w.Entry = strings.TrimSuffix(w.Entry, ".lua")
	w.budget = DefaultBudget
	if w.Budget != "" {
		d, err := time.ParseDuration(w.Budget)
		if err != nil || d <= 0 {
			return fmt.Errorf("budget %q is not a duration like 90m", w.Budget)
		}
		w.budget = d
	}
	seen := map[string]bool{}
	for i := range w.Inputs {
		in := &w.Inputs[i]
		if in.Name == "" {
			return fmt.Errorf("input %d has no name", i+1)
		}
		if seen[in.Name] {
			return fmt.Errorf("input %q is listed twice", in.Name)
		}
		seen[in.Name] = true
		if in.Prompt == "" {
			in.Prompt = in.Name
		}
		if in.Pattern != "" {
			re, err := regexp.Compile("^(?:" + in.Pattern + ")$")
			if err != nil {
				return fmt.Errorf("input %q pattern: %w", in.Name, err)
			}
			in.re = re
		}
	}
	return nil
}

func (w Workflow) CheckInputs(given map[string]string) (map[string]string, error) {
	out := map[string]string{}
	known := map[string]bool{}
	var bad []string
	for _, in := range w.Inputs {
		known[in.Name] = true
		v := strings.TrimSpace(given[in.Name])
		if v == "" {
			v = in.Default
		}
		switch {
		case v == "" && in.Required:
			bad = append(bad, in.Name+" is required ("+in.Prompt+")")
			continue
		case v != "" && in.re != nil && !in.re.MatchString(v):
			bad = append(bad, fmt.Sprintf("%s %q does not match %s", in.Name, v, in.Pattern))
			continue
		}
		out[in.Name] = v
	}
	for k := range given {
		if !known[k] {
			bad = append(bad, k+" is not an input of "+w.Name)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return nil, fmt.Errorf("%s: %s", w.Name, strings.Join(bad, "; "))
	}
	return out, nil
}

type Host struct {
	Exec     func(script.Call) ([]string, any, error)
	Export   func(text, slug, ext string) (string, error)
	OnStatus func(string)
}

type Result struct {
	DoIt   bool
	Status []string
	Writes []script.Write
}

func (w Workflow) Run(ctx context.Context, input map[string]string, host Host, doit bool) (Result, error) {
	in, err := w.CheckInputs(input)
	if err != nil {
		return Result{}, err
	}

	res := Result{DoIt: doit}
	run := script.Run{
		Name:   w.Entry,
		Input:  in,
		DoIt:   doit,
		Budget: w.budget,
		Export: host.Export,
		OnStatus: func(line string) {
			res.Status = append(res.Status, line)
			if host.OnStatus != nil {
				host.OnStatus(line)
			}
		},
	}
	if host.Exec != nil {
		run.Exec = func(c script.Call) ([]string, any, error) {
			said, result, err := host.Exec(c)
			if pl, ok := result.(script.Planned); ok {
				res.Writes = append(res.Writes, pl.PlannedWrites()...)
			}
			return said, result, err
		}
	}
	if _, err = w.set.Run(ctx, run); err != nil {
		err = fmt.Errorf("%s: %w", w.Name, err)
	}
	return res, err
}

func Dir(root string) string {
	if root == "" {
		return ""
	}
	return filepath.Join(root, DirName)
}

func ReadRuns(path string) ([]map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(filepath.Ext(path), ".csv") {
		return csvRuns(path, b)
	}
	// yaml.v3 decodes 0272 as the number 272
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.SequenceNode || len(doc.Content[0].Content) == 0 {
		return nil, fmt.Errorf("%s has no runs in it: a list, each entry a map of inputs", path)
	}
	var runs []map[string]string
	for i, entry := range doc.Content[0].Content {
		if entry.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("%s: run %d is not a map of inputs", path, i+1)
		}
		run := map[string]string{}
		for j := 0; j+1 < len(entry.Content); j += 2 {
			k, v := entry.Content[j].Value, entry.Content[j+1]
			if v.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("%s: run %d: %s is not a single value", path, i+1, k)
			}
			if v.Tag != "!!null" {
				run[k] = v.Value
			}
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func csvRuns(path string, b []byte) ([]map[string]string, error) {
	// Excel's "CSV UTF-8" starts the file with a byte-order mark
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))))
	r.TrimLeadingSpace = true
	r.Comment = '#'
	rows, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(rows) < 2 {
		return nil, fmt.Errorf("%s has no runs in it: a header row naming the inputs, then one row per run", path)
	}
	head := rows[0]
	for i, h := range head {
		head[i] = strings.TrimSpace(h)
	}
	runs := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		run := map[string]string{}
		for i, v := range row {
			if v = strings.TrimSpace(v); v != "" && head[i] != "" {
				run[head[i]] = v
			}
		}
		runs = append(runs, run)
	}
	return runs, nil
}

func Describe(run map[string]string) string {
	keys := make([]string, 0, len(run))
	for k := range run {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + run[k]
	}
	return strings.Join(parts, " ")
}
