package workflow

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
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

	TenantInput = "tenant"
)

type Workflow struct {
	Name        string  `yaml:"-"`
	Title       string  `yaml:"title"`
	Description string  `yaml:"description,omitempty"`
	Inputs      []Input `yaml:"inputs,omitempty"`
	Entry       string  `yaml:"entry,omitempty"`
	Budget      string  `yaml:"budget,omitempty"`

	Uses      []string `yaml:"uses,omitempty"`
	Steps     []string `yaml:"steps,omitempty"`
	Endpoints []Call   `yaml:"endpoints,omitempty"`

	Dir    string            `yaml:"-"`
	budget time.Duration     `yaml:"-"`
	set    *script.Set       `yaml:"-"`
	calls  *endpoint.Set     `yaml:"-"`
	all    []Call            `yaml:"-"`
	from   map[string]string `yaml:"-"`
	steps  []Workflow        `yaml:"-"`
}

type Call struct {
	endpoint.Endpoint `yaml:",inline"`
	In                string `yaml:"in,omitempty"`
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
	l := newLoader()
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		w, err := l.loadOne(filepath.Join(dir, e.Name()))
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

type loader struct {
	done    map[string]Workflow
	loading map[string]bool
}

func newLoader() *loader {
	return &loader{done: map[string]Workflow{}, loading: map[string]bool{}}
}

func LoadOne(dir string) (Workflow, error) { return newLoader().loadOne(dir) }

func Parse(dir string, b []byte) (Workflow, error) { return newLoader().parse(dir, b) }

func (l *loader) loadOne(dir string) (Workflow, error) {
	key := absDir(dir)
	if w, ok := l.done[key]; ok {
		return w, nil
	}
	b, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return Workflow{}, err
	}
	w, err := l.parse(dir, b)
	if err == nil {
		l.done[key] = w
	}
	return w, err
}

func absDir(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

func usedDir(dir, name string) string {
	if strings.ContainsAny(name, `/\`) {
		return filepath.Join(dir, name)
	}
	return filepath.Join(filepath.Dir(absDir(dir)), name)
}

func (l *loader) parse(dir string, b []byte) (Workflow, error) {
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
	own := make([]endpoint.Endpoint, len(w.Endpoints))
	for i, e := range w.Endpoints {
		own[i] = e.Endpoint
	}
	if _, err := endpoint.Build(name, "", nil, own); err != nil {
		return Workflow{}, fmt.Errorf("%s/%s: %w", name, ManifestName, err)
	}

	key := absDir(dir)
	if l.loading[key] {
		return Workflow{}, fmt.Errorf("%s: its uses lead back to itself", name)
	}
	l.loading[key] = true
	defer delete(l.loading, key)
	var used []Workflow
	for _, u := range w.Uses {
		uw, err := l.loadOne(usedDir(dir, u))
		if os.IsNotExist(err) {
			return Workflow{}, fmt.Errorf("%s/%s: uses %s, and there is no workflow there", name, ManifestName, u)
		}
		if err != nil {
			return Workflow{}, fmt.Errorf("%s: uses %s: %w", name, u, err)
		}
		used = append(used, uw)
	}
	for _, s := range w.Steps {
		sw, err := l.loadOne(usedDir(dir, s))
		if os.IsNotExist(err) {
			return Workflow{}, fmt.Errorf("%s/%s: step %s: there is no workflow there", name, ManifestName, s)
		}
		if err != nil {
			return Workflow{}, fmt.Errorf("%s: step %s: %w", name, s, err)
		}
		for _, in := range sw.Inputs {
			if in.Required && in.Default == "" && !w.hasInput(in.Name) {
				return Workflow{}, fmt.Errorf("%s/%s: step %s needs %s, which is not an input here", name, ManifestName, s, in.Name)
			}
		}
		w.steps = append(w.steps, sw)
	}

	if err := w.resolve(append(used, w.steps...)); err != nil {
		return Workflow{}, fmt.Errorf("%s/%s: %w", name, ManifestName, err)
	}
	eps := make([]endpoint.Endpoint, len(w.all))
	for i, e := range w.all {
		eps[i] = e.Endpoint
	}
	if w.calls, err = endpoint.Build(name, "", nil, eps); err != nil {
		return Workflow{}, fmt.Errorf("%s/%s: %w", name, ManifestName, err)
	}
	for _, e := range w.all {
		err := e.SDWAN()
		if err == nil && e.In != "" && !w.hasInput(e.In) {
			err = fmt.Errorf("%s goes in: %s, which is not an input%s", e.Name, e.In, w.fromNote(e.Name))
		}
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
	switch {
	case len(w.Steps) > 0 && set.Has(w.Entry):
		return Workflow{}, fmt.Errorf("%s: lists steps and has a %s.lua; it runs one or the other", name, w.Entry)
	case len(w.Steps) == 0 && !set.Has(w.Entry):
		return Workflow{}, fmt.Errorf("%s: no %s.lua to start from", name, w.Entry)
	}
	for _, u := range used {
		set.Use(u.Name, u.set)
	}
	w.set = set
	return w, nil
}

func (w *Workflow) resolve(used []Workflow) error {
	w.all, w.from = nil, map[string]string{}
	add := func(c Call, owner string) error {
		for _, have := range w.all {
			if have.Name != c.Name {
				continue
			}
			if sameCall(have, c) {
				return nil
			}
			return fmt.Errorf("%s is %s in %s and %s in %s; give it one definition",
				c.Name, describeCall(have), w.from[c.Name], describeCall(c), owner)
		}
		w.all = append(w.all, c)
		w.from[c.Name] = owner
		return nil
	}
	for _, c := range w.Endpoints {
		if err := add(c, w.Name); err != nil {
			return err
		}
	}
	for _, u := range used {
		for _, c := range u.all {
			if err := add(c, u.from[c.Name]); err != nil {
				return err
			}
		}
	}
	return nil
}

func method(c Call) string {
	if c.Method == "" {
		return "GET"
	}
	return strings.ToUpper(c.Method)
}

func sameCall(a, b Call) bool {
	return method(a) == method(b) && a.Host == b.Host && a.Path == b.Path && a.In == b.In
}

func describeCall(c Call) string {
	s := method(c) + " " + c.Host + c.Path
	if c.In != "" {
		s += " in " + c.In
	}
	return s
}

func (w Workflow) fromNote(name string) string {
	if from := w.from[name]; from != "" && from != w.Name {
		return " (it comes from " + from + ")"
	}
	return ""
}

func (w Workflow) Calls() []Call { return w.all }

func (w Workflow) StepFlows() []Workflow { return w.steps }

func (w Workflow) Takes(given map[string]string) map[string]string {
	out := map[string]string{}
	for _, in := range w.Inputs {
		if v, ok := given[in.Name]; ok {
			out[in.Name] = v
		}
	}
	return out
}

func (w Workflow) From(name string) string { return w.from[name] }

func (w Workflow) Endpoint(name string) (endpoint.Endpoint, bool) {
	if w.calls == nil {
		return endpoint.Endpoint{}, false
	}
	e, err := w.calls.Get(name)
	return e, err == nil
}

func (w Workflow) In(name string) string {
	for _, e := range w.all {
		if e.Name == name {
			return e.In
		}
	}
	return ""
}

func (w Workflow) Declares(name string) bool {
	for _, in := range w.Inputs {
		if in.Name == name {
			return true
		}
	}
	return false
}

func (w Workflow) hasInput(name string) bool {
	return name == TenantInput || w.Declares(name)
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
	Exec       func(script.Call) ([]string, any, error)
	Export     func(text, slug, ext string) (string, error)
	Keep       func(name, text string) (string, error)
	Kept       func(name string) (text, where string, err error)
	OnStatus   func(string)
	ExitOnWait bool
	Resume     bool
}

type Result struct {
	DoIt    bool
	Inputs  map[string]string
	Status  []string
	Writes  []script.Write
	Data    any
	Waiting string
}

func (w Workflow) Run(ctx context.Context, input map[string]string, host Host, doit bool) (Result, error) {
	in, err := w.CheckInputs(input)
	if err != nil {
		return Result{}, err
	}

	res := Result{DoIt: doit, Inputs: in}
	run := script.Run{
		Name:       w.Entry,
		Input:      in,
		DoIt:       doit,
		ExitOnWait: host.ExitOnWait,
		Resume:     host.Resume,
		Budget:     w.budget,
		Export:     host.Export,
		Keep:       host.Keep,
		Kept:       host.Kept,
		OnStatus: func(line string) {
			res.Status = append(res.Status, line)
			if host.OnStatus != nil {
				host.OnStatus(line)
			}
		},
		OnReturn: func(v any) { res.Data = v },
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
		var waiting *script.Waiting
		if errors.As(err, &waiting) {
			res.Waiting = waiting.What
		}
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
