package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"tyrtop.com/iontk/internal/runtime"
	"tyrtop.com/iontk/internal/sase/api"
	"tyrtop.com/iontk/internal/sase/write"
	"tyrtop.com/iontk/internal/script"
	"tyrtop.com/iontk/internal/workflow"
)

var (
	WorkflowsFolder string
	WritesFile      string
	WorkflowPaths   []string
)

const WorkflowPrefix = "workflow."

func registerCommands(rt *runtime.Runtime) {
	rt.Register(writeCommand())
	rt.Register(writeRecordsCommand())
	for _, c := range authCommands() {
		rt.Register(c)
	}
	for _, c := range resetCommands() {
		rt.Register(c)
	}
	rt.Register(reloadCommand())
	rt.Register(saveCommand())
}

func ReloadCommands(rt *runtime.Runtime) {
	rt.Forget(WorkflowPrefix)
	rt.Forget(ActionPrefix)
	rt.ClearWarnings()
	all, err := LoadWorkflows(rt.Store(), WorkflowsFolder)
	if err != nil {
		rt.Warn(err.Error())
	}
	for _, p := range WorkflowPaths {
		w, err := workflow.LoadOne(p)
		if err != nil {
			rt.Warn(err.Error())
			continue
		}
		all = append(all, w)
	}
	for _, w := range all {
		rt.Register(workflowCommand(w))
	}
	actions, err := LoadActions(rt.Store())
	if err != nil {
		rt.Warn(ActionsRefused + err.Error())
	}
	for _, name := range actions.Names() {
		rt.Register(actionCommand(actions, name))
	}
}

func guardFor(rt *runtime.Runtime, t runtime.Tenant) write.Guard {
	name := t.Name
	if name == "" {
		name = t.TSG
	}
	return WriteGuard(rt.Store(), rt.Config(), name)
}

const EachInput = "inputs"

func workflowCommand(w workflow.Workflow) runtime.Command {
	args := make([]runtime.Arg, 0, len(w.Inputs)+1)
	each := EachInput
	for _, in := range w.Inputs {
		args = append(args, runtime.Arg{Name: in.Name, Help: in.Prompt, Required: in.Required, Default: in.Default})
		if in.Name == EachInput {
			each = ""
		}
	}
	if each != "" {
		args = append(args, runtime.Arg{Name: each,
			Help: "file of runs, one per entry: YAML or JSON, a list of maps of the inputs, or CSV, a header row naming them; the inputs given here fill what an entry leaves out; runs go at once, up to --concurrency"})
	}
	return runtime.Command{
		Name: WorkflowPrefix + w.Name, Help: w.Title, Long: w.Description, Slow: true,
		Source: filepath.Join(w.Dir, workflow.ManifestName),
		Args:   args, Writes: true, Each: each,
		Run: func(rt *runtime.Runtime, c *runtime.Call) error {
			if each != "" && c.Arg(each) != "" {
				return runEach(rt, c, w, each)
			}
			return runWorkflow(rt, c, w, c.Args)
		},
	}
}

func runWorkflow(rt *runtime.Runtime, c *runtime.Call, w workflow.Workflow, input map[string]string) error {
	exec := scriptExec(rt, c, true)
	host := workflow.Host{
		Exec: func(sc script.Call) ([]string, any, error) {
			e, ok := w.Endpoint(strings.TrimPrefix(sc.Name, EndpointPrefix))
			if sc.Line != "" || !strings.HasPrefix(sc.Name, EndpointPrefix) || !ok {
				return exec(sc)
			}
			var said []string
			call := &runtime.Call{Ctx: c.Ctx, Args: sc.Args, DoIt: c.DoIt, Out: func(l string) {
				said = append(said, l)
				c.Say("%s", l)
			}}
			err := callEndpoint(rt, call, WorkflowPrefix+w.Name, e)
			return said, call.Result, err
		},
		Export:   func(text, slug, ext string) (string, error) { return rt.Export(slug, ext, text) },
		OnStatus: func(line string) { c.Say("%s", line) },
	}
	res, err := w.Run(c.Ctx, input, host, c.DoIt)
	c.Result = res
	return err
}

func scriptExec(rt *runtime.Runtime, c *runtime.Call, echo bool) func(script.Call) ([]string, any, error) {
	return func(sc script.Call) ([]string, any, error) {
		name, args, rest := sc.Name, sc.Args, []string(nil)
		if sc.Line != "" {
			cmd, a, r, err := rt.Parse(sc.Line)
			if err != nil {
				return nil, nil, err
			}
			name, args, rest = cmd.Name, a, r
		}
		if cmd, ok := rt.Command(name); ok && cmd.FrontOnly {
			return nil, nil, fmt.Errorf("%s is not open to scripts", name)
		}
		var said []string
		call := &runtime.Call{Ctx: c.Ctx, Args: args, Rest: rest, DoIt: c.DoIt, Out: func(l string) {
			said = append(said, l)
			if echo {
				c.Say("%s", l)
			}
		}}
		err := rt.Exec(call, name)
		return said, call.Result, err
	}
}

func runEach(rt *runtime.Runtime, c *runtime.Call, w workflow.Workflow, each string) error {
	runs, err := workflow.ReadRuns(c.Arg(each))
	if err != nil {
		return err
	}
	tags := runTags(w, runs)
	var mu sync.Mutex
	say := func(tag, line string) {
		mu.Lock()
		defer mu.Unlock()
		c.Say("%s | %s", tag, line)
	}
	inputs := make([]map[string]string, len(runs))
	seen := map[string]int{}
	for i, run := range runs {
		input := map[string]string{}
		for k, v := range c.Args {
			if k != each {
				input[k] = v
			}
		}
		for k, v := range run {
			if strings.TrimSpace(v) != "" {
				input[k] = v
			}
		}
		in, err := w.CheckInputs(input)
		if err != nil {
			return fmt.Errorf("run %d: %w; no run was started", i+1, err)
		}
		inputs[i] = in
		if len(w.Inputs) == 0 || !c.DoIt {
			continue
		}
		v := in[w.Inputs[0].Name]
		if at, dup := seen[v]; dup && v != "" {
			return fmt.Errorf("runs %d and %d both have %s %s, and two runs at once would act on the same one",
				at, i+1, w.Inputs[0].Name, v)
		}
		seen[v] = i + 1
	}
	at := max(1, Defaults.Concurrency)
	c.Say("%d run(s), up to %d at a time", len(runs), at)
	slots := make(chan struct{}, at)
	errs := make([]error, len(runs))
	var wg sync.WaitGroup
	for i, run := range runs {
		input := inputs[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-c.Ctx.Done():
				errs[i] = c.Ctx.Err()
				return
			}
			defer func() { <-slots }()
			say(tags[i], workflow.Describe(run))
			one := &runtime.Call{Ctx: c.Ctx, DoIt: c.DoIt, Out: func(l string) { say(tags[i], l) }}
			if errs[i] = runWorkflow(rt, one, w, input); errs[i] != nil {
				say(tags[i], "failed: "+errs[i].Error())
			}
		}()
	}
	wg.Wait()
	var failed []string
	for i, err := range errs {
		if err != nil {
			failed = append(failed, tags[i])
		}
	}
	c.Say("")
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d runs failed: %s", len(failed), len(runs), strings.Join(failed, ", "))
	}
	c.Say("%d run(s) done", len(runs))
	return nil
}

func runTags(w workflow.Workflow, runs []map[string]string) []string {
	first := ""
	if len(w.Inputs) > 0 {
		first = w.Inputs[0].Name
	}
	tags := make([]string, len(runs))
	seen := map[string]int{}
	for i, run := range runs {
		tag := strings.TrimSpace(run[first])
		if tag == "" {
			tag = fmt.Sprintf("run %d", i+1)
		}
		if seen[tag]++; seen[tag] > 1 {
			tag = fmt.Sprintf("%s#%d", tag, seen[tag])
		}
		tags[i] = tag
	}
	return tags
}

type WriteRun struct {
	Spec    write.Spec
	Plans   []write.ElementPlans
	Sent    int
	Fail    int
	Blocked string
}

func (r *WriteRun) PlannedWrites() []script.Write {
	var out []script.Write
	for _, ep := range r.Plans {
		for _, p := range ep.Plans {
			if !p.Empty() {
				out = append(out, script.Write{Method: p.Method, Path: p.Path, Body: p.After})
			}
		}
	}
	return out
}

func writeCommand() runtime.Command {
	return runtime.Command{
		Name: "write", Slow: true,
		Help: "change records in the portal on each element: what=interface elements=ID,ID match=1 field=value…",
		Args: []runtime.Arg{
			{Name: "what", Required: true, Help: "which kind of record, a name in writes.yaml"},
			{Name: "elements", Required: true, Help: "element IDs, comma separated"},
			{Name: "match", Required: true, Help: `the records to change: a name, field=value, or "*"; several with ;`},
		},
		Writes: true,
		Run:    runWrite,
	}
}

func writeSpec(rt *runtime.Runtime, what string) (write.Spec, error) {
	specs, problem := rt.Writes()
	if problem != "" {
		return write.Spec{}, errors.New(problem)
	}
	if specs == nil {
		return write.Spec{}, fmt.Errorf("no writes are loaded")
	}
	spec, ok := specs.Spec(what)
	if !ok {
		return write.Spec{}, fmt.Errorf("nothing called %q can be written; there is %s", what, strings.Join(specs.Names(), ", "))
	}
	return spec, nil
}

type writeGroup struct {
	portal runtime.Portal
	tenant runtime.Tenant
	els    []api.Element
	plans  []write.ElementPlans
}

func writeTargets(rt *runtime.Runtime, c *runtime.Call, arg string) ([]*writeGroup, error) {
	rows := rt.ElementsArg(c, arg)
	if len(rows) == 0 {
		return nil, fmt.Errorf("no elements")
	}
	var groups []*writeGroup
	at := map[string]*writeGroup{}
	for _, r := range rows {
		g := at[r.TSG]
		if g == nil {
			p, t, err := rt.PortalFor(c.Ctx, r.TSG)
			if err != nil {
				return nil, err
			}
			g = &writeGroup{portal: p, tenant: t}
			at[r.TSG] = g
			groups = append(groups, g)
		}
		g.els = append(g.els, r.Element)
	}
	return groups, nil
}

func writeRecordsCommand() runtime.Command {
	return runtime.Command{
		Name: "write.records", Slow: true,
		Help: "the records of one kind an element has, as writes.yaml reads them: what=interface element=ID",
		Args: []runtime.Arg{
			{Name: "what", Required: true, Help: "which kind of record, a name in writes.yaml"},
			{Name: "element", Required: true, Help: "the element's ID"},
		},
		Run: func(rt *runtime.Runtime, c *runtime.Call) error {
			spec, err := writeSpec(rt, c.Arg("what"))
			if err != nil {
				return err
			}
			groups, err := writeTargets(rt, c, "element")
			if err != nil {
				return err
			}
			el := groups[0].els[0]
			recs, err := spec.Records(c.Ctx, groups[0].portal, write.ElementVars(el))
			if err != nil {
				return err
			}
			c.Result = recs
			for _, r := range recs {
				c.Say("%s", spec.LabelOf(write.ElementVars(el), r))
			}
			return nil
		},
	}
}

func runWrite(rt *runtime.Runtime, c *runtime.Call) error {
	spec, err := writeSpec(rt, c.Arg("what"))
	if err != nil {
		return err
	}
	set := map[string]string{}
	for _, w := range c.Rest {
		k, v, ok := strings.Cut(w, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("write: %q is not field=value", w)
		}
		set[strings.TrimSpace(k)] = v
	}
	if err := spec.CheckSet(set); err != nil {
		return err
	}

	groups, err := writeTargets(rt, c, "elements")
	if err != nil {
		return err
	}
	var blocked error
	for _, g := range groups {
		if err := guardFor(rt, g.tenant).Ready(spec); err != nil && blocked == nil {
			blocked = err
		}
	}
	if c.DoIt && blocked != nil {
		return blocked
	}

	var all []write.ElementPlans
	for _, g := range groups {
		g.plans = write.PlanElements(c.Ctx, g.portal, spec, g.els, runtime.SplitList(c.Arg("match"), ";"), set)
		all = append(all, g.plans...)
	}
	run := &WriteRun{Spec: spec, Plans: all}
	if blocked != nil {
		run.Blocked = blocked.Error()
	}
	c.Result = run
	for _, l := range write.PlanLines(all) {
		c.Say("%s", l)
	}
	switch changes := write.Changes(all); {
	case changes == 0:
		c.Say("nothing to change")
		return write.Unplanned(all)
	case !c.DoIt:
		c.Say("")
		c.Say("dry run: %d change(s), nothing sent — add --doit to send", changes)
		return write.Unplanned(all)
	}

	c.Say("")
	var errs []error
	for _, g := range groups {
		sent, failed, err := guardFor(rt, g.tenant).ApplyAll(c.Ctx, g.portal, spec, g.plans, func(element string, p write.Plan, err error) {
			if err != nil {
				c.Say("failed  %s %s: %s", element, p.Label, err)
				return
			}
			c.Say("sent    %s %s", element, p.Label)
		})
		run.Sent, run.Fail = run.Sent+sent, run.Fail+failed
		errs = append(errs, err)
	}
	c.Say("")
	c.Say("%d sent, %d failed", run.Sent, run.Fail)
	return errors.Join(errs...)
}
