# Safety

## Never run against a real tenant yourself

Not a workflow, not a batch, not a dry run. A dry run still sends real GETs
with the user's credentials. Use `--demo` or `--fixtures FILE` to try
something, then give the user the command for the real thing.

## Writes are dry runs

Anything that changes something lists what it would send and ends:

    iontk batch run -e ID -c "config interface 1 shutdown"
    plan config interface 1 shutdown on store-0003-rtr-01
    dry run: 1 element(s), none sent — add --doit to send

`--doit` on that command sends it. This is the same for a device command, a
`write`, an endpoint that isn't a GET, and a workflow.

- Show the user the dry run before you suggest `--doit`.
- `--doit` is the user's call. Don't add it to a command they didn't mean
  to send.
- `--allow-writes` is deprecated. Don't use it.

## A dry run can't read through a POST

Only GETs go out on a dry run. A `*_query` endpoint is a POST, so on a dry
run it returns the filter you passed and no records. A plan built on a query
is built on no data. Read through GET endpoints, and tell the user when
there isn't one.

## Don't send a write twice

A POST that timed out may have reached the controller. If you send it again
you get two.

iontk already handles this. It doesn't retry a POST or a PATCH that got no
answer, and `--resume` checks the controller for what's there before it
sends anything.

So when a `--doit` run ends early:

1. Don't run it again as is.
2. Don't write your own loop around it.
3. Run it with the same inputs and `--resume`.

That goes for a run from an `--inputs` file too. Run the same file again
with `--resume`.

For a single write outside a workflow, GET the record and look before you
send it again.

## Don't go around iontk

- No sign-in code of your own, and no tokens copied out of iontk.
- No direct HTTP calls to the controller when iontk has the operation. You'd
  skip the pacing, the retries, the dry run and the write log.
- If iontk doesn't have an endpoint, declare it in a workflow. See
  [endpoints.md](endpoints.md).

## Don't guess IDs

Look tenants, sites and elements up each time. See
[discovery.md](discovery.md). Check the `tenant` field on what you find: the
same site name can be in two tenants.

## In a workflow

- Send the same writes in the same order on a dry run as under `--doit`.
  `--resume` depends on it.
- A made-up ID starts with `dry-run-`, and must not reach the controller
  under `--doit`.
- GET a record before you PUT it, and send its `_etag` back. Don't make one
  up.
- Catch a 404 only where a missing record is a normal answer. Raise every
  other error.

## Secrets and customer details

- Secrets go in with `--secret -` or `--secret @file`, not on the command
  line.
- This repo is public. No customer names, site names, serials or tenant IDs
  in anything you write here. Use Acme Co and Store 0003 from the demo fleet.
