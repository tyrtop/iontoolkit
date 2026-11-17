# Endpoints

iontk calls the controller by endpoint name, not by URL. Don't build a URL
and don't make the HTTP call yourself: you'd lose the sign-in, the pacing,
the retries and the dry run.

Endpoints are called from a workflow, as `ctx.run("endpoint.<name>", {...})`.
There's no CLI command that calls one.

## The master list

iontk ships with names for the Prisma SD-WAN API, 1,146 endpoints built from
the Prisma SASE SDK.

    iontk endpoints list --q spokeclusters

    - name: site_spokeclusters_query
      method: POST
      path: /sdwan/v2.0/api/sites/{site_id}/spokeclusters/query
      from: iontk
    - name: spokeclusters
      method: GET
      path: /sdwan/v2.0/api/sites/{site_id}/spokeclusters
      from: iontk
    - name: spokeclusters.one
      ...

`--q` matches the name and the path.

## Names

The names follow the SDK's function names.

| name | what it is |
|---|---|
| `sites` | GET the collection |
| `sites.one` | GET one by ID |
| `sites.post` | create |
| `sites.put` | replace |
| `sites.delete` | delete |

- A few resources also have a `.patch`. `sites` isn't one of them.
- A name the SDK has under one method only is left without a suffix,
  whatever the method is. `spokeclusters_status_query` is a POST.
- A name without a suffix isn't always a collection. Some are a GET of one
  record by ID.

So look at the `method` and `path` that `endpoints list` prints. Don't work a
name out from the table.

## Calling one

    local _, sites = ctx.run("endpoint.sites", {})
    local _, ifs = ctx.run("endpoint.interfaces", { site_id = s.id, element_id = el.id })
    ctx.run("endpoint.interfaces.put", { site_id = s.id, element_id = el.id, interface_id = i.id, body = body })

- Always pass a table as the second argument, even an empty one.
- The keys fill the `{placeholders}` in the path, and every placeholder
  needs a value. `body` is the request body.
- A list comes back as `{ items = { ... } }`.
- A name iontk doesn't know fails with
  `no command is called "endpoint.<name>"`. So does a call with no table.

## Only a GET runs on a dry run

A dry run sends GETs and no other method. The others are listed as
`plan POST /sdwan/...` and not sent, and the call's answer is the body you
passed in.

That includes the `*_query` endpoints. They only read, but they're POSTs, so
on a dry run a query returns your own filter and no `items`. `--resume` also
takes a POST for a create, and looks for the object it made.

- Read through the GET endpoints. Most resources have one.
- If the only way to read something is a query POST, the workflow can't plan
  from it. Tell the user. Don't work around it.

## Query parameters

The master list has paths and methods. It has no query parameters and no
body schemas. Get those from the API reference on pan.dev.

To send a query parameter, declare the endpoint yourself with the query in
the path. The parameter here is only to show the form:

    endpoints:
      - name: sites_page
        path: /sdwan/v4.13/api/sites?limit={limit}

Every placeholder is required, in the query too. For a parameter that's
sometimes left out, declare a second endpoint without it.

## Your own

If the master list doesn't have it, or you need a different version, declare
it in the workflow's `workflow.yaml`. Yours takes the place of the master
list's entry with that name.

    endpoints:
      - name: sites
        path: /sdwan/v4.9/api/sites

To see what a workflow declares:

    iontk endpoints list --workflow ./my-workflow

That lists the workflow's own endpoints and the ones it gets through `uses`.
It prints `[]` for a workflow that only calls the master list.

## Limits

- The path has to start with `/sdwan/`. The tenant's controller answers it;
  an endpoint can't name its own host.
- A placeholder's value can't have `/`, `?`, `#`, `%` or `\` in it, or be
  `.` or `..`.
- The versions in the master list are the SDK's. A tenant can be ahead of or
  behind them.

More in [workflows.md](workflows.md#endpoints).
