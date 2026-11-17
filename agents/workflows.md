# Writing iontk workflows

This is for an AI agent that has to write a workflow for iontk. Read it
before writing one. A working example is in `examples/workflows/site-report/`,
and [README.md](README.md) covers the repo itself.

## What a workflow is

A folder with a `workflow.yaml` and some Lua next to it:

```
~/.config/iontk/workflows/<name>/
  workflow.yaml     title, inputs, the workflows it uses, its own endpoints
  main.lua          the entry point: return function(ctx) ... end
  *.lua             modules main.lua can require()
  dev/              tests and notes; iontk ignores subfolders
```

iontk loads every folder under `~/.config/iontk/workflows/` at startup, and
`reload` loads them again. Each becomes a runtime command, `workflow.<name>`:

```
iontk workflow <name> --site "Store 0003" --tenant "Acme Co"          # dry run
iontk workflow <name> --site "Store 0003" --tenant "Acme Co" --doit   # sends the writes
iontk workflow ./path/to/folder --site "Store 0003"                   # one not installed
iontk workflow <name> --inputs sites.csv                              # one run per row
iontk workflow <name> ... --doit --exit-on-wait                       # exits at a wait, see below
iontk workflow <name> ... --doit --resume                             # picks up after an earlier run
```

In the TUI it's under Workflows, and `e` on a workflow opens its
`workflow.yaml` in the editor.

How the work splits up, and this matters:

- **YAML is the what.** The inputs, the endpoints, and which other workflows
  this one builds on.
- **Lua is the why.** Which records to read, what to decide, what to write
  and in what order.
- **Go is the how.** Retries and backoff, the rate limit, dry run versus
  `--doit`, the write log, which tenant a call goes to, ending a run at a
  wait, `--resume`. Lua does none of that, and a workflow needs no Go of its
  own. If you think you need a Go change for a workflow, you've got the split
  wrong; tell the user and don't make it.

Lua can only talk to the runtime, through `ctx.run`. There's no HTTP, no
files, no shell.

## workflow.yaml

```yaml
title: NAT IPs from a store's routers
description: >-
  Reads vlan96 and vlan2 from each router on a site, prints and exports their
  addresses as CSV. No writes go to the portal.
inputs:
  - name: site
    prompt: Site name
    required: true
    pattern: '[^/\\]+'
budget: 1h
```

- `title` shows in the menus and `--help`. `description` is the long help.
- `inputs`: each has a `name`, and optionally `prompt` (the label and flag
  help), `default`, `required` and `pattern`. `pattern` is a regexp that must
  match the full value. Every value gets to Lua as a string.
- `budget`: how long the run may take before it's ended, as a Go duration
  (`90m`, `3h`). The default is 30m.
- `entry`: a different entry file than `main`. You rarely need it.
- `uses`: other workflows this one builds on. See Reusing another workflow.
- `steps`: see A workflow made of steps.
- `endpoints`: calls this workflow defines itself. See the next section.

A key iontk doesn't know, anywhere in the file, makes it fail to load.

Two input names are reserved: `inputs` (the runs file) and `tenant`. Every
workflow gets a `tenant` input without declaring it. Don't declare either
one. A workflow that declares `tenant` can't take `--tenant` more than once,
and one that declares `inputs` can't take `--inputs`.

## Endpoints

iontk has a master list of Prisma SD-WAN endpoints built in, taken from the
`prisma_sase` SDK. A workflow can call any of them by name without listing
it in `workflow.yaml`:

```lua
local _, sites = ctx.run("endpoint.sites", {})
```

To find one:

```
iontk endpoints list --q interfaces
- name: interfaces
  method: GET
  path: /sdwan/v4.21/api/sites/{site_id}/elements/{element_id}/interfaces
  from: iontk
- name: interfaces.one
  ...
```

The names follow the SDK's function names:

| name | call |
|---|---|
| `sites` | GET the collection |
| `sites.one` | GET one, by id |
| `sites.post` | POST |
| `sites.put` | PUT |
| `sites.delete` | DELETE |

A few resources have a `.patch` too; `sites` doesn't. A name the SDK has
under one method only has no suffix, like `spokeclusters_status_query` (a
POST), and some names with no suffix are a GET of one record. Check the
method and the path in `endpoints list`. Don't work a name out from the
table. The `{placeholders}` in the path are the argument names the call
takes, and each one needs a value.

Use the master list when it has what you need. Declare an endpoint in the
workflow only when the master list doesn't have it, when you need a
different API version, or when the call needs a query parameter:

```yaml
endpoints:
  - name: sites
    path: /sdwan/v4.9/api/sites
```

- `name`: what Lua calls it by, as `endpoint.<name>`. A workflow's own
  endpoint takes the place of the master list's entry with that name, for
  that workflow and the ones that use it.
- `method`: `GET` by default, otherwise `POST`, `PUT`, `PATCH` or `DELETE`.
- `path`: must start with `/sdwan/`. No `host:`, since the tenant's own
  controller answers. No websocket (`WS`), because a workflow calls, it
  doesn't dial. `{name}` placeholders are filled from the call's arguments,
  and a query string after `?` can use them too. A value that has `/`, `?`,
  `#`, `%` or `\` in it, or is `.` or `..`, is rejected.
- `in`: see Tenants. Only a workflow that works in two tenants uses it.

The master list has no query parameters. To send one, declare the endpoint
with the query in its path, like `path: /sdwan/v4.13/api/sites?limit={limit}`.
Every placeholder is required, so a parameter that's sometimes left out
needs a second endpoint without it.

**A POST is a write to iontk, even one that only reads.** On a dry run a
`*_query` endpoint isn't sent, and the call returns the filter you passed
in. Read through GET endpoints. If a query POST is the only way to read
something the plan depends on, tell the user. Don't work around it.

If you're porting a script that used older API versions than the master
list, declare those endpoints with the versions the script used. If you don't
know which version something needs, find out from the user; don't pick one.

## Reusing another workflow: uses

Never copy another workflow's endpoints or Lua. Put it in `uses:` instead:

```yaml
title: Copy one router
inputs: [...]
budget: 3h
uses: [site-copy]
```

- **Endpoints.** The workflow can call every endpoint the used workflows
  declare, including the ones they get from their own `uses`, plus the ones
  it lists itself and the master list.
- **One definition per name.** If one name shows up with a different
  method, path or `in` (listed here and in a used workflow, or in two used
  workflows), the workflow fails to load and the error names both places.
  Fix it in the workflow that owns the endpoint; never paste a second version.
  An identical copy loads, but delete it anyway.
- **Lua.** `require("x")` looks in the workflow's own folder first, then in
  the used workflows. A module that comes from a used workflow resolves its
  own `require`s in its own folder. If two used workflows each have a
  different `x.lua`, `require("x")` fails and tells you to pick one, as
  `require("<workflow>/x")`. The order in `uses` doesn't pick for you. The
  entry file (`main.lua`) must be in the workflow's own folder.
- **Where.** A name in `uses` is a folder next to this one under the
  workflows folder. A name with a `/` is a path relative to this workflow's
  folder.
- **No symlinks.** Use `uses`, not links to another folder's Lua.
- A `uses` that names a missing workflow, or that leads back to itself,
  fails to load.

To see what a workflow declares, and where each endpoint comes from:

```
iontk endpoints list --workflow <name>
- name: sites
  path: /sdwan/v4.9/api/sites
  from: site-copy
```

`--workflow` also takes a path to a folder. It lists declared endpoints
only, so a workflow that calls just the master list prints `[]`.

## A workflow made of steps

A long job is a list of small workflows, not one big one. Give the list in
`steps:` and leave out `main.lua`:

```yaml
title: Replace one router
inputs:
  - name: site
    required: true
  - name: router
    required: true
  - name: serial
    required: true
steps:
  - router-backup
  - router-claim
  - router-upgrade
  - router-assign
  - router-interfaces
```

- Each step is a workflow of its own, in a folder next to this one like a
  `uses` name, and runs by itself too: `iontk workflow router-assign --site ...`.
- iontk runs them in order and prints `step 3 of 5: router-upgrade`. A step
  gets the inputs it declares, by name. If a step has a required input with
  no default that the list doesn't declare, the list fails to load.
- A step calls its own endpoints. It doesn't get the list's.
- A workflow has `steps` or a `main.lua`, not both.
- **Each step looks up what it needs right before it writes.** It finds its
  own site, router and ids on the portal by name. It takes from the step
  before it only what is on the portal and what is kept (below). That is
  what lets iontk start at any step.
- Keep a step to one stage of the job: one wait, or one group of writes that
  belong together.
- The run's `data` is what the last step returned.

### Keeping something between steps

Something every step needs but none should fetch again, like a full pull of
the source site, is kept once:

```lua
local model, where = ctx.kept("copy-" .. site)
if not model then
  model = P.pull(ctx, site)
  where = ctx.keep("copy-" .. site, model)
end
```

`ctx.keep(name, table)` writes it to `kept/<TSG ID>/<name>.json` in the
config folder and returns the path. It raises an error when one is already
there, so check `ctx.kept` first. `ctx.kept(name)` returns the table and its
path, or no value at all. A kept table comes back as it was saved: nulls,
empty objects and all. The name can't start with `.` or have `/`, `\` or `:`
in it.

Under `--resume`, a step that would keep something new counts as not done.

## main.lua

```lua
local U = require("util")

return function(ctx)
  local name = ctx.input.site
  ...
end
```

The file must end by returning a function, and that function gets `ctx`.
What that function returns is the run's `data`. Only `base`, `table`,
`string` and `math` are open: no `io`, `os`, `print`, `load` or `dofile`. A
module loads once per run.

The Lua is gopher-lua, and it has a bug: a `for ... in` whose iterator is
built on an expression that contains a multi-value call crashes the run with
`runtime error: invalid memory address or nil pointer dereference`. `gsub`
returns two values, so this is the usual way to hit it:

```lua
for v in (line:gsub("%s%s+", "\1") .. "\1"):gmatch("([^\1]*)\1") do  -- crashes
```

Put the string in a local first, and then loop over the local:

```lua
local cells = line:gsub("%s%s+", "\1")
cells = cells .. "\1"
for v in cells:gmatch("([^\1]*)\1") do
```

If a run fails with that error and no line number, look for this before
anything else.

### ctx

| call | what it does |
|---|---|
| `ctx.input.<name>` | an input's value, always a string (empty when not given and no default) |
| `ctx.doit` | true under `--doit`, false on a dry run |
| `ctx.exit_on_wait` | true under `--exit-on-wait` |
| `ctx.resume` | true under `--resume` |
| `ctx.keep(name, table)`, `ctx.kept(name)` | keep a table between steps and runs, and get it back |
| `ctx.run(name, args)` | call an endpoint or a runtime command; returns `said, answer` |
| `ctx.status(text)` | a line of output, shown as the run goes and kept in the run's `status` |
| `ctx.fail(text)` | end the run with that message |
| `ctx.export(text_or_table, slug, ext)` | write a file under the config's `exports/`; returns its path. A table is written as indented JSON (`ext` default `json`), a string as is (`txt`) |
| `ctx.budget(d)` | a timer for waiting: `b:tick(every, what)` sleeps `every` and returns true, or false once `d` is spent. `what` names the wait, see Waits. `b:spent()` is the time since the run started |
| `ctx.null` | JSON null, for a body that has to have a null in it |
| `ctx.object()` | an empty table that encodes as `{}`, not `[]` |
| `ctx.utc(t)`, `ctx.ago(t)` | format a portal timestamp |
| `ctx.pick(list, fn)` | the first item `fn` returns true for |
| `ctx.lookup(list, field, value)` | the first record whose field equals value, or whose field is a list with value in it |

`ctx.lookup` compares the full value. `"Store 000"` doesn't find
`"Store 0003"`. It returns the first match and doesn't tell you about a
second, so count the matches yourself where two would be a mistake.

`said` is the list of lines the call printed. `answer` is its result.

`ctx.run` also takes one string, a command and its arguments as
`name=value`: `ctx.run('fleet.search query="store 0003"')`. It isn't the
CLI's form, so `--query` doesn't work there. Use the table form.

`ctx.set`, `ctx.open` and `ctx.state` are for Lua actions, not workflows.
Don't use them here.

### Calling an endpoint

```lua
local function call(ctx, name, args)
  local _, answer = ctx.run("endpoint." .. name, args or {})
  return answer
end

local sites = call(ctx, "sites").items or {}
local ifs = call(ctx, "interfaces", { site_id = s.id, element_id = el.id })
call(ctx, "interfaces.put", { site_id = s.id, element_id = el.id, interface_id = i.id, body = body })
```

- Always pass a table as the second argument, even an empty one.
  `ctx.run("endpoint.sites")` with no table is taken as a command line and
  fails with `no command is called "endpoint.sites"`. A name that iontk
  doesn't know fails with that message too, so check the name first.
- The argument keys fill the path's placeholders. `body` is the request body:
  pass a Lua table and it goes out as JSON.
- IDs are strings. Pass them on as you got them. An ID written as a Lua
  number loses its last digits, because a Lua number is a float.
- `answer` is the decoded JSON. A list comes back as `{ items = { ... } }`;
  write a small `items()` helper so a missing `items` doesn't crash the run.
- A record keeps the keys the portal sent as null, which plain Lua can't do.
  If you copy a record and send it back, copy it with the table's metatable
  so those nulls are kept (`setmetatable(out, getmetatable(v))`).

A failed call raises a Lua error, something like
`run endpoint.x: sdwan/...: status 404 Not Found: {...}`. When a missing
record is a normal answer, catch it with `pcall` and check the text:

```lua
local function list(ctx, name, args)
  local ok, v = pcall(call, ctx, name, args)
  if ok then return (type(v) == "table" and v.items) or {} end
  if tostring(v):find("status 404", 1, true) then return {} end
  error(v, 0)
end
```

Don't hide other errors. Raise them again.

### Running device commands

`batch.run` runs ION CLI commands on elements and gives back the results:

```lua
local _, res = ctx.run("batch.run", { elements = table.concat(ids, ","), cmd = "dump spoke-ha status" })
for _, r in ipairs(res.Results or {}) do
  if r.error then
    ctx.status((r.name or r.element_id) .. " failed: " .. r.error)
  else
    ctx.status(r.name .. ": " .. r.commands[1].output)
  end
end
```

`elements` is element IDs, comma separated, and `cmd` is one or more
commands, comma separated. `res.Results` has one entry per element, in the
order you gave the IDs. Each has `element_id`, `name`, `hw_id`,
`model_name`, `software_version`, `connected`, and `commands`, a list of
`command` and `output`.

The call raises an error only when every element failed. When some failed,
it returns, and each failed element has an `error` and may have no
`commands`. Check `r.error` before you index `r.commands`.

A command that is not read-only only runs under `--doit`. The read-only ones
start with `dump`, `inspect`, `ping`, `ping6`, `tcpping`, `arping`,
`traceroute`, `traceroute6`, `dig`, `dig6`, `nslookup` or `help`. On a dry
run any other command is listed as `plan <command> on <element>` and `res`
is nil.

### Dry run and --doit

A dry run is the default. On a dry run:

- GETs really go to the portal, so the plan is built from real data;
- every other method is planned and not sent. Each one is listed as
  `plan PUT /sdwan/...` as the run goes and in the run's `writes`, and the
  call returns the body you sent as its answer, so there's no `id`.

So any code that needs the id of something it just created has to cope with
a dry run. Use a placeholder that only exists inside the plan:

```lua
local function post(self, name, args)
  local answer = call(self.ctx, name, args)
  if type(answer) == "table" and answer.id ~= nil then return answer.id end
  if self.ctx.doit then self.ctx.fail(name .. ": the portal sent back no id") end
  self.seq = self.seq + 1
  return "dry-run-" .. self.seq
end
```

Under `--doit`, a placeholder must not reach the portal. Fail instead.

When a PUT has to include the record's current `_etag`, GET the record first,
put your fields over it, and send that. Don't make up an `_etag`.

### Waits

Waiting for the portal (a claim, an upgrade, a router coming online) only
makes sense under `--doit`. Skip the wait on a dry run, check the condition
before each sleep, and give the wait a name:

```lua
local function wait(ctx, what, done)
  if not (ctx.doit or ctx.exit_on_wait) then return end
  local b = ctx.budget("1200s")
  while true do
    local ok, yes = pcall(done)
    if ok and yes then return end
    if not b:tick("10s", what) then ctx.fail(what .. " took longer than 1200s") end
  end
end
```

`what` is a phrase that fits after "waiting on": `"the claim of " .. serial`,
`serial .. "'s upgrade to " .. version`, `name .. " binding to the site"`.

Without `--exit-on-wait`, a named `tick` sleeps like any other. With it, the
first named `tick` ends the run right there: iontk prints
`waiting on <what>`, exits with code 75, and `--format json` puts it in the
run's `"waiting"` field instead of `"error"`. Whoever ran it runs the
workflow again later with the same inputs and `--resume`, and it picks up
from where the portal is. A `tick` with no name always sleeps; use that for
short retries, like a PUT that lost an `_etag` race, and for timers that
aren't waits.

### Running again: --resume

Picking up after an earlier run is iontk's job, not the Lua's. Don't write
code that looks for what an earlier run did, skips steps, or checks before a
create. `--resume` does it.

For a workflow made of steps:

1. iontk goes to the middle step and runs just that one as a dry run, with
   its output hidden, so only that step's GETs go out. The step is done when
   the dry run hit no wait, kept no new table, and its last write is on the
   portal, or it had no writes to make.
2. Done, and it looks in the later half; not done, the earlier half. Ten steps
   take three or four looks.
3. In the first step that isn't done, it halves over that step's writes the
   same way to find the last one the portal has.
4. It runs from there: that step with its finished writes printed as `skip`,
   then every later step as usual.

A workflow with a `main.lua` and no steps gets 3 and 4 only, over all of its
writes, after one hidden dry run of all of it. That is the reason to split a
long one into steps.

How a write is checked:

- A PUT is on the portal when the record at its path has the fields it sets,
  or what a later write to that path in the list sets.
- A POST is on the portal when its object is in the collection: an object
  with that name, or with those fields when it has no name.
- A DELETE is on the portal when a GET of its path is a 404.

In the hidden dry run a POST gets back a made-up id, which is looked up
only if a check or a GET needs it. A skipped POST gives the Lua the object
that's there.

For that to work, the Lua has to:

- **Send the same writes in the same order on a dry run as under `--doit`.**
- **Use real ids on a dry run when the thing exists.** If the router is
  already on the site, a dry run fetches its interfaces and records like a
  `--doit` run does, so the writes it lists have real paths. Gate those GETs
  on the thing existing, not on `ctx.doit`. Don't fetch from something that
  isn't there yet.
- **Start made-up ids with `dry-run-`.** iontk treats a value or path segment
  that starts with it as made up.
- **Let a dry run look at its waits.** Under `ctx.exit_on_wait`, a wait checks
  once even without `--doit`, and ticks if it isn't done. That is how iontk
  finds a step that is still waiting. Without `ctx.doit` or
  `ctx.exit_on_wait`, skip the wait.
- **Don't start over by accident.** A workflow that creates the thing
  everything else depends on, like a site, fails if it already exists unless
  `ctx.resume` is set. Without `--resume`, a second run sends its creates
  again.
- **Keep the plan fixed.** Build it from what is kept, not from something the
  workflow changes on the way.
- **Don't read through a POST.** A `*_query` call is a write to `--resume`.
  It looks for an object the query "created", doesn't find one, and takes
  every write after it as not done.

Wait limits like the 1200s above, and counters like claim tries, start over
on each run. Tracking the total time across runs is up to whoever runs it.
Put that in the workflow's description if it matters.

## Tenants

The tenant is an input like any other, given by whoever runs the workflow.
The runtime never works it out from a site name.

- Every workflow has a `tenant` input: a saved or loaded tenant's name or
  TSG ID. On the CLI it's `--tenant`, in a runs file a `tenant` column.
  `--tenant` given more than once runs the workflow once per tenant.
- Every endpoint call in the run goes to that tenant, and the run prints
  `tenant <name>` first. Lua never gets it and doesn't pass it.
- With no tenant given, a user with one tenant loaded is fine. With more than
  one, the run is rejected before any call.

For a workflow that works in two tenants, for example copying a site from
one tenant into another, add an input whose value is a tenant and point the
endpoints that go there at it with `in:`:

```yaml
inputs:
  - name: site
    required: true
  - name: into
    prompt: Tenant to build it in
    required: true
endpoints:
  - name: site_post
    method: POST
    path: /sdwan/v4.13/api/sites
    in: into
```

`endpoint.sites` GETs from `tenant`, and `endpoint.site_post` writes to
`into`. When `into` is a different tenant, its plan and send lines end
`in <tenant>`. An `in:` that names no input fails to load, including one
that comes through `uses`. Lua still never handles a tenant.

## Running many: --inputs

`--inputs` takes a file of runs: a CSV with a header row naming the inputs,
or YAML/JSON as a list of maps. Each row is one run, and each row can have
its own `tenant`. The flags given on the command line fill in what a row
is missing.

```csv
tenant,site,serial
Acme Co,Store 0003,00-000001-0001
```

Every row is checked first, inputs and tenant, and one bad row means no run
starts. Runs go in parallel, up to `--concurrency`. Under `--doit`, two rows
with the same tenant and the same first input are rejected, since two runs
would act on one thing. Put the input that names the thing being changed
first.

`--doit`, `--resume` and `--exit-on-wait` apply to every row. The exit code
is 1 if any run failed, and 75 if none failed and at least one is at a wait.

## Testing without the tenant

**Never run a workflow against the user's real tenant yourself, not even a
dry run.** GETs are real calls. Write the workflow, test it against fakes,
then give the user the command to run.

`iontk --fixtures file.yaml` (or `--demo`) runs the CLI or TUI against a fake
fleet offline. Both use a temporary config folder, so the installed
workflows aren't loaded: run yours by its folder, `iontk --demo workflow
./<name>`. `examples/fixtures.yaml` is a small one. The fake portal
serves the calls iontk itself makes (elements, sites, interfaces, HA) and
404s the others with `fixture mode serves no <path>`. Use it to check the
command line, the inputs, the tenant handling and device commands.

For a workflow's own logic, write a Go test that runs it through iontk's
real runtime against a fake portal. The fake is a `runtime.Portal`:
`GetJSON(ctx, path)` returns JSON from a map of path to JSON, 404 for
anything else, and `Send(ctx, method, path, body)` records the write. The
test:

1. adds the workflow's folder to `app.WorkflowPaths`;
2. calls `rt.UsePortal` with a function that returns the fake and a
   `runtime.Tenant`;
3. calls `rt.Exec` with a `runtime.Call` and `app.WorkflowPrefix+"<name>"`.
   The call has the inputs in `Args`, plus `DoIt`, and `ExitOnWait` when
   testing waits.

A workflow with `uses` finds its used workflows in the folders next to that
folder, so point at the real folder, not a copy by itself.

It's a test of iontk's `app` package, so keep it in the workflow's `dev/`
folder and copy it in to run it:

```
cp dev/<name>_test.go <repo>/internal/app/zz_<name>_test.go
(cd <repo> && go test ./internal/app -run <TestName> -count=1)
rm <repo>/internal/app/zz_<name>_test.go
```

Remove the copy afterwards; it must not stay in the repo. `-count=1`
matters: Go's test cache doesn't know the Lua changed. Test:

- a dry run with no `Send`s, whose planned writes (`workflow.Result.Writes`)
  come in the right order with the right bodies;
- a `--doit` run against a fake that gives out ids on POST, and changes state
  where the workflow waits on it, with a check that no `dry-run-` string
  got to it;
- if it waits: `--doit --exit-on-wait` runs against a fake that keeps each
  wait pending, then lets it finish. Each run should end `waiting on` the
  right thing (`errors.As` with `*script.Waiting`) and never send a write
  twice. Set `Resume` on the `runtime.Call` for every run after the first.
  Keep the fake's lists in step with its single-record answers, or the
  workflow works from stale data;
- picking up: wrap the fake so its `Send` fails from write N on, for each N,
  run again with `Resume`, and check that the two runs together sent what one
  uninterrupted run sends. The fake has to return on a GET of a record's own
  path what was PUT there, or every PUT looks undone;
- when you split or rewrite a workflow: save what the old one sends first,
  and check the new one sends the same bytes;
- the rejections: a missing site, a bad input, two matches where one was
  expected.

If you're porting a script (an SDK script, a shell script), go through its
source and copy what it does: order of writes, which fields it maps by name,
which errors it steps over. When the original is Python, comparing its output
with the workflow's plan finds what a read-through misses.

## Rules

- Call endpoints by name: from the master list, the workflow's own
  `workflow.yaml`, or a workflow it uses. Lua never builds a path or a URL.
- Never copy another workflow's endpoints or Lua, and never symlink them.
  Use `uses`. If the version you need differs from the owner's, raise it with
  the user; don't define a second one.
- No Go changes for a workflow. If the runtime can't do something a workflow
  needs, tell the user what's missing and leave the Go as it is.
- Don't add features the user didn't want. A workflow that only looks things
  up ("get the IPs, and that's all") is left that way.
- A workflow that writes must list every write in order on a dry run, and
  send those same writes under `--doit`.
- Read with GETs. A POST isn't sent on a dry run, even a query.
- A workflow that waits names every wait and works under `--resume`: no code
  of its own for picking up.
- Fail early and give the reason, in a plain sentence naming the thing: "no
  site is named Store 0003", "two sites are named Store 0003", "RTR-01 has no
  VLAN 5". `ctx.lookup` won't find the second site for you; count the
  matches. Check everything you can before the first write, because a run
  that ends halfway through has built half a site.
- Comments, in Lua and YAML both: almost none. Keep one only if it notes
  something from outside the repo, like the portal's behavior, an
  appliance's, or the script or SDK rule being copied. One line, about ten
  words. Never explain the format, the keys, or what the code below does.
- Status, wait names and error text in plain words, no polish.
- Don't commit or push, in the repo or in the workflows folder, unless the
  user tells you to.

## Before you give it to the user

- `iontk workflow <name> --help` lists its inputs. A workflow that doesn't
  load isn't a command at all; `iontk workflow <name>` and `iontk reload`
  both print why, naming the file.
- `iontk endpoints list --workflow <name>` shows every endpoint it declares,
  and each one comes from where you expect.
- The dev test passes in every mode it needs, and its copy is removed from
  `internal/app`.
- `gofmt -l` and `go vet ./internal/app` are clean if you copied a test in.
- Give the user the dry-run command first, with `--tenant`, and tell them what
  to look for in the `writes` before they run `--doit`.
