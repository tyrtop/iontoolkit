# iontk for AI agents

Start here if you're an AI agent using iontk to do something on Prisma
SD-WAN, or working in this repo.

iontk is a runtime for Prisma SD-WAN, written in Go. It signs in to Strata
Cloud Manager, loads the elements from every tenant, opens CLI sessions on
IONs and calls the controller's API. Every action is a named command, and the
TUI, the CLI and Lua workflows all run the same commands.

## Read these

| file | read it when |
|---|---|
| [architecture.md](architecture.md) | first. What iontk is and what it already does for you |
| [commands.md](commands.md) | you need to run something |
| [discovery.md](discovery.md) | you need a tenant, a site or an element ID |
| [endpoints.md](endpoints.md) | you need an API call |
| [workflows.md](workflows.md) | you're writing a workflow |
| [execution.md](execution.md) | you're running a workflow: dry run, `--doit`, `--resume`, waits |
| [safety.md](safety.md) | before anything that writes |
| [examples/](examples/) | you want to see it done end to end |

## The short version

- Don't write your own sign-in or your own HTTP calls to the controller when
  iontk has the operation.
- Look up tenant, site and element IDs. Don't assume them.
- Pass `--format json` and parse standard output. Don't parse the text meant
  for people.
- Put several operations together in a Lua workflow.
- Reading a workflow's Lua doesn't tell you everything it will do. Dry-run it
  and read the `writes` in its JSON.
- A dry run sends GETs only. A POST is planned and not sent, and that
  includes the `*_query` endpoints that only read.
- Use `--resume` after a `--doit` run that ended early. Don't send a write
  again until you know whether the first one got there.

## Try it without a tenant

    go build -o iontk ./cmd/iontk
    ./iontk --demo fleet search "store 0003" --format json

`--demo` and `--fixtures FILE` use a fake fleet with no network access. Never
run anything against a real tenant yourself, not even a dry run. Give the
user the command.

## Working in this repo

    go build ./...
    go test -short -p 1 -timeout 5m ./...
    gofmt -l cmd internal
    go vet ./...

`-short` skips the browser sign-in test, which needs Chrome.

- Comments: almost none, in Go, Lua and YAML. Keep one only when it notes
  something from outside the repo that the code can't show, like the
  controller's or an appliance's behavior. One line, about ten words.
- Status lines, errors and flag help in plain words.
- Don't commit or push unless the user tells you to.
- Check the diff for comments before you report work as done.
- This repo is public. No customer names, site names, serials or tenant IDs,
  in code, tests or docs.
