# Finding tenants, sites and elements

Look IDs up. Don't reuse one from an earlier run, a ticket or a guess.
An element ID from one tenant is no use in another.

## Tenants

    iontk tenant list

    > Acme Co  tsg 999000999 · service account · 1 users · in the fleet
      Globex  tsg 999000222 · service account · 1 users · in the fleet

`>` marks the tenant in use. `--tenant NAME` picks one for a single command.

## Elements

    iontk fleet search "store 0003" --format json

    [
      {
        "id": "7081372949379741162",
        "name": "glob-0003-rtr-01",
        "site": "Store 0003",
        "model": "ion 3200",
        "software": "6.4.3",
        "connected": true,
        "tenant": "Globex",
        "tsg_id": "999000222"
      },
      ...

The query matches name, site, model, software, tenant and state, and every
word has to match. `"ion 1200 offline"` is the offline ION 1200s. The search
covers every loaded tenant, so check the `tenant` field: two tenants can each
have a "Store 0003", and the demo fleet does.

`--format ids` prints one ID a line, for `batch run -f -`.

The list is cached for 15 minutes. Add `--refresh` after something changed.

## One element

    iontk element inspect --id 1000000000000000001

JSON with the element, its status, its interfaces and its HA state. `--raw` gives
each response as the controller sent it.

## Sites and everything else

There's no `site list` command. Inside a workflow, call the endpoint:

    local _, sites = ctx.run("endpoint.sites", {})
    local site = ctx.lookup(sites.items, "name", ctx.input.site)
    if not site then ctx.fail("no site is called " .. ctx.input.site) end

`ctx.lookup` returns the first record whose field equals the value. It
doesn't match part of a name, and it doesn't tell you when a second record
matches too. Where two matches would be a mistake, count them yourself:

    local found = {}
    for _, s in ipairs(sites.items) do
      if s.name == ctx.input.site then found[#found + 1] = s end
    end
    if #found > 1 then ctx.fail("two sites are named " .. ctx.input.site) end

Take names as workflow inputs and look the IDs up at run time. A workflow
that takes a site ID as input only works in one tenant.

## HA pairs

    iontk ha list
    iontk ha sweep

Both read HA state and print a count for people. For one element's HA state
as JSON, use `element inspect`.

## Endpoints

    iontk endpoints list --q sites

See [endpoints.md](endpoints.md).
