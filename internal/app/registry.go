package app

import (
	"fmt"
	"strings"
	"sync"

	"tyrtop.com/iontk/internal/runtime"
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
	for _, c := range siteCommands() {
		rt.Register(c)
	}
	for _, c := range authCommands() {
		rt.Register(c)
	}
	ReloadCommands(rt)
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
		Args: args, Writes: true, Each: each,
		Run: func(rt *runtime.Runtime, c *runtime.Call) error {
			if each != "" && c.Arg(each) != "" {
				return runEach(rt, c, w, each)
			}
			return runWorkflow(rt, c, w, c.Args)
		},
	}
}

func runWorkflow(rt *runtime.Runtime, c *runtime.Call, w workflow.Workflow, input map[string]string) error {
	host := workflow.Host{
		Exec:     scriptExec(rt, c, true),
		Export:   func(text, slug string) (string, error) { return Export(rt.Store(), slug, "txt", text) },
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
		inputs[i] = input
		if len(w.Inputs) == 0 || !c.DoIt {
			continue
		}
		v := strings.TrimSpace(input[w.Inputs[0].Name])
		if v == "" {
			v = strings.TrimSpace(w.Inputs[0].Default)
		}
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
	Spec  write.Spec
	Plans []write.ElementPlans
	Sent  int
	Fail  int
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

func runWrite(rt *runtime.Runtime, c *runtime.Call) error {
	specs, err := LoadWrites(rt.Store(), WritesFile)
	if err != nil {
		return err
	}
	spec, ok := specs.Spec(c.Arg("what"))
	if !ok {
		return fmt.Errorf("nothing called %q can be written; there is %s", c.Arg("what"), strings.Join(specs.Names(), ", "))
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

	p, tenant, err := rt.Portal(c.Ctx)
	if err != nil {
		return err
	}
	guard := guardFor(rt, tenant)
	if c.DoIt {
		if err := guard.Ready(spec); err != nil {
			return err
		}
	}

	all := write.PlanElements(c.Ctx, p, spec, splitList(c.Arg("elements"), ","), splitList(c.Arg("match"), ";"), set)
	run := &WriteRun{Spec: spec, Plans: all}
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
	run.Sent, run.Fail, err = guard.ApplyAll(c.Ctx, p, spec, all, func(element string, p write.Plan, err error) {
		if err != nil {
			c.Say("failed  %s %s: %s", element, p.Label, err)
			return
		}
		c.Say("sent    %s %s", element, p.Label)
	})
	c.Say("")
	c.Say("%d sent, %d failed", run.Sent, run.Fail)
	return err
}

func splitList(s, sep string) []string {
	var out []string
	for _, part := range strings.Split(s, sep) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}
