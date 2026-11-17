# Architecture

iontk is one runtime with three front ends. It is easy to read the code and
come away thinking it's a TUI with a CLI attached. It isn't. The TUI, the CLI
and Lua workflows are three ways to run the same commands on the same
runtime.

    TUI keys and menus ─┐
    CLI  iontk a b      ├─>  command registry  ─>  runtime  ─>  Strata Cloud Manager
    Lua  ctx.run("a.b") ─┘                                  ─>  IONs (CLI sessions)

## The runtime

`internal/runtime` has a registry of named commands. A command has a name
like `fleet.search`, a list of arguments, and three flags that matter:

- `Writes`: it changes something. It's a dry run unless the call has `--doit`.
- `Slow`: it takes a while, so the front end shows progress.
- `FrontOnly`: a workflow can't run it.

`Runtime.Exec` checks the arguments and runs the command. If a command isn't
marked `Writes`, `Exec` clears `--doit` before the command gets the call, so
a read can't turn into a write by accident.

The runtime also has the state the front ends share: the tenants, the
element list, the open CLI sessions, the options, the loaded workflows.

## The front ends

- **CLI.** `iontk fleet search` is `fleet.search`. The CLI builds its command
  tree from the registry, so a new command gets a CLI form without CLI code.
  A command's arguments are its flags.
- **TUI.** Keys and menu entries are bound to command names in YAML. The `:`
  line runs a command by name.
- **Lua.** `ctx.run("fleet.search", { query = "store 0003" })` runs the same
  command and gives back its result as a table.

A workflow is itself a command: `iontk workflow site-report` on the CLI, a
form in the TUI.

## What the runtime knows about Strata

This is the part to not rebuild. It's there because the controller has rules
you only learn by getting them wrong.

- **Sign-in.** Service account, bearer token and browser sign-in. One browser
  sign-in is turned into a credential per tenant so it lasts. Tokens are
  renewed before they expire. Secrets are in the OS keyring, or in a
  file in the config folder when there's no keyring.
- **Many tenants.** Every saved tenant is loaded into one element list. A
  command is routed to the right tenant by the element it's for.
- **Pacing.** Requests are limited to `--rps` a second with `--burst` on top.
  A 429 slows every later request down, and `Retry-After` is obeyed.
- **Retries.** A GET, PUT or DELETE is tried again on 413, 502, 503, 504 or
  no answer, up to eight times with a growing wait. A POST or PATCH is tried
  again only when the connection was never made. That's because a POST that
  got no answer may still have reached the controller. A 429 is tried again
  for any method, and doesn't count toward the eight.
- **CLI sessions.** A shell on an ION goes through the controller over a
  websocket. iontk dials it, retries the dial, logs in, turns paging off,
  answers pager prompts, and knows when a command's output has ended.
- **Read-only device commands.** `dump`, `inspect`, `ping`, `ping6`,
  `tcpping`, `arping`, `traceroute`, `traceroute6`, `dig`, `dig6`,
  `nslookup` and `help` are read-only. A device command that starts with any
  other word is a dry run unless `--doit` is given.
- **The write log.** Every write sent to the controller is added to
  `writes.log` in the config folder.
- **The element cache.** The element list is kept for 15 minutes.
  `--refresh` fetches it again.

## Endpoints

Controller paths are in YAML, never in Go strings. `internal/endpoint/sdwan.yaml`
is the master list, built from the Prisma SASE SDK. A workflow or a built-in
feature declares an endpoint itself only when the master list doesn't have
it, or to use a different version. A local declaration takes the place of
the master list's. See [endpoints.md](endpoints.md).

## YAML over Go

The element list's columns, the data pages, the writes, the keys, the menu
and the themes are YAML files with a built-in default. A copy in the user's
config folder takes the built-in one's place.

## Where things are

| path | what's there |
|---|---|
| `cmd/iontk` | the CLI: flags, and the few commands written by hand |
| `internal/runtime` | the command registry, the options and the shared state |
| `internal/app` | builds the runtime: workflows, endpoints, writes, tenants, `--resume` |
| `internal/tui` | the TUI |
| `internal/sase` | the API client, batch runs, the ION CLI session, writes |
| `internal/saseauth` | sign-in |
| `internal/endpoint` | named endpoints and the master list |
| `internal/workflow`, `internal/script` | workflows and the Lua they run |
| `internal/fixture` | the fake fleet behind `--demo` and `--fixtures` |
| `examples/` | Lua actions, key bindings, a fixtures file, a workflow |

## Adding to it

A new action is a `runtime.Command`. Register it and the CLI, the TUI and Lua
all have it. Don't add a second way to do something a command already does,
and don't call the controller from a front end.
