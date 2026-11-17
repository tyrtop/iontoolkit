# Running a workflow

    iontk workflow NAME --input value            dry run
    iontk workflow NAME --input value --doit     send the writes
    iontk workflow ./folder --input value        one that isn't installed

A workflow's inputs are its flags. `iontk workflow NAME --help` lists them.

A name that isn't installed fails with `no workflow is called NAME`, and the
error lists any workflow that didn't load and why. With `--demo` or
`--fixtures` the installed workflows aren't loaded, so run one by its
folder.

## Read the dry run, not the Lua

A workflow can use other workflows, run steps, and pick endpoint names and
bodies from what it fetched. Reading `main.lua` won't tell you every call it
will make on this tenant right now. The dry run will.

On a dry run the GETs go to the controller, so the plan is built from real
data. Every other call is listed and not sent. A POST that only reads, like
a `*_query`, isn't sent either. See [endpoints.md](endpoints.md).

    iontk workflow NAME --input value --format json

    [
      {
        "workflow": "new-vlan",
        "tenant": "Acme Co",
        "tsg": "999000999",
        "inputs": { "site": "Store 0003" },
        "doit": false,
        "started": "2026-11-16T18:15:28-07:00",
        "finished": "2026-11-16T18:15:29-07:00",
        "status": [ "store-0003-rtr-01 has 4 interfaces" ],
        "writes": [
          {
            "method": "PUT",
            "path": "/sdwan/v4.21/api/sites/185.../elements/100.../interfaces/163...",
            "body": { ... }
          }
        ],
        "data": { ... }
      }
    ]

The output is always a list, one object per run, even for one run.
`--format jsonl` prints one run a line.

- `status` is the lines the workflow printed with `ctx.status`.
- `writes` is every write, in order, with its method, path and body. On a
  dry run that's the plan. A workflow that only reads has no `writes`.
- `data` is what the workflow returned.
- A failed run has `error`. A run that ended at a wait has `waiting`.

The `plan PUT /sdwan/...` lines are printed for people, next to the status
lines. They aren't in `status`. Read `writes`.

Without `--format`, the status lines go to standard output as text and there
is no JSON.

## Tenants

`--tenant NAME` is needed when more than one tenant is loaded. Give it more
than once to run once per tenant. `--tenant` more than once can't be used
with `--inputs`; put a `tenant` column in the file.

## Many runs

    iontk workflow NAME --inputs rows.csv

One run per row of a CSV, YAML or JSON file. Flags fill in what a row is
missing. `-n` sets how many run at once. Each status line starts with the
run's first input, like `Store 0003 | ...`.

## Waits

Some workflows wait on the controller: a claim, an upgrade, a router coming
online. By default the run sleeps until it's done.

With `--exit-on-wait`, the run ends at the first wait that isn't done yet.
It prints `waiting on <what>`, and exits with code 75. That's "come back
later", not a failure. Run it again later with the same inputs and
`--resume`.

## --resume

    iontk workflow NAME --input value --doit --resume

`--resume` finds the last write an earlier run got onto the controller and
picks up after it. It works that out from the controller, not from a file an
earlier run left, so it's right even if that run was on another machine or
was killed. Writes that are already there are printed as `skip`.

Use it whenever a `--doit` run ended early: an error, a timeout, exit 75, a
lost connection. Without `--resume`, a second run sends its creates again.

`--resume` and `--exit-on-wait` work the same with `--inputs` and with more
than one `--tenant`. Each run resumes by itself.

## Exit codes

| code | meaning |
|---|---|
| 0 | done |
| 75 | at a wait; run again with `--resume` |
| 1 | failed; read the error |

With many runs, the exit code is 1 if any run failed, and 75 if none failed
and at least one is at a wait. Read each run's `error` and `waiting` in the
JSON to tell which is which.

## Knobs

    --rps, --burst          API requests a second
    -n, --concurrency       elements or runs at once
    --element-timeout       time limit per element
    --session-timeout       time limit for one CLI session
    --dial-attempts         tries to reach an element
    -v                      request details and the error from each attempt

The defaults are safe. Don't raise `--rps` to make a run faster unless the
user wants that.
