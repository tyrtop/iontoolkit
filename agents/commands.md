# Commands

Every action in iontk is a command with one name everywhere:

    iontk fleet refresh           # shell
    :fleet.refresh                # TUI
    ctx.run("fleet.refresh", {})  -- Lua

## Finding one

    iontk --help               every group and what's in it
    iontk fleet --help         the commands in a group
    iontk fleet search --help  one command's flags

## Arguments

A command's arguments are its flags. Any flag's value can be kept off the
command line:

    --secret -        read it from the next line of standard input
    --secret @file    read it from a file

`--secret` is `tenant add`'s flag. `--password -` on `user add` works the
same way.

## The ones you'll use most

| command | what it does |
|---|---|
| `iontk tenant list` | the saved tenants |
| `iontk fleet search QUERY` | the elements a query matches |
| `iontk element inspect --id ID` | what the controller knows about one element, as JSON |
| `iontk batch run` | device commands on many elements |
| `iontk session open ID` | a shell on one element. It takes over the terminal, so it's for the user |
| `iontk endpoints list --q TEXT` | the named API endpoints |
| `iontk ha list`, `iontk ha sweep` | read HA state; both print a count |
| `iontk write` | change records across a list of elements |
| `iontk workflow NAME` | run a workflow |
| `iontk reload` | read the YAML and the workflows again, and print what didn't load |

## Output

Pass `--format` and read standard output:

    iontk fleet search "store 0003" --format json
    iontk fleet search "store 0003" --format ids
    iontk batch run -e ID -c "dump device status" --format jsonl
    iontk workflow NAME --format json

What each command prints when you leave `--format` out:

| command | no `--format` | formats |
|---|---|---|
| `fleet search` | a table for people | `json`, `ids` |
| `batch run` | JSON | `json`, `jsonl`, `text`, `table`, `csv` |
| `workflow` | its status lines as text, on standard output | `json`, `jsonl` |

With `--format json` or `jsonl` a workflow's status lines move to standard
error, and standard output is the JSON only. Progress and notes always go to
standard error. Add `--no-progress` when you're not on a terminal.

Commands like `tenant list` and `ha list` print lines for people. Don't
build on their wording.

## When there's no terminal

`batch run --search` lists the elements it matched and waits for a yes
before it runs. With no terminal it ends with an error. Pass `--no-validate`
(or `--yes`) once you've checked the list with `fleet search`, or pass the
IDs with `-e` or `-f`.

    iontk fleet search "dc east" --format ids --no-progress |
      iontk batch run -f - -c "dump software status" --no-progress

## Exit codes

| code | meaning |
|---|---|
| 0 | done |
| 75 | a workflow run is at a wait. See [execution.md](execution.md) |
| 1 | failed. The reason is the last line on standard error |

`batch run` exits 1 when any element failed. The JSON is still complete, and
each failed element has an `error`.

## Writes

A command that writes is a dry run. It lists what it would send and ends.
`--doit` on that command sends it. There is no global switch. See
[safety.md](safety.md).

`iontk write` sends only the kinds of record marked `verified` in
`writes.yaml`. For any other kind it shows the change and won't send it,
even with `--doit`.
