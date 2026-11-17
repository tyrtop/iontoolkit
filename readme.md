# iontk

<p align="center"><img width="800" alt="iontk demo" src="https://media.tyrtop.com/iontk/tui-shell-v1.gif" /></p>

<p align="center">
    <a href="https://github.com/tyrtop/iontoolkit/releases"><img src="https://img.shields.io/github/release/tyrtop/iontoolkit" alt="Latest Release"></a>
    <a href="https://github.com/tyrtop/iontoolkit/actions"><img src="https://github.com/tyrtop/iontoolkit/actions/workflows/build.yml/badge.svg" alt="Build Status"></a>
    <a href="https://pkg.go.dev/tyrtop.com/iontk"><img src="https://pkg.go.dev/badge/tyrtop.com/iontk.svg" alt="Go Reference"></a>
</p>

<p align="center">Anything Prisma SD-WAN can do, from your terminal.<br />Your tenants, your IONs and your workflows, set up the way you want them.</p>

## Features

- **Multi-tenant:** Sign in once, and list all of your tenants in one place
- **Shells:** Select IONs, hit enter, and start a shell
- **Batch:** Run commands on any number of IONs and get structured output
- **Configurable API calls:** Give a Prisma SD-WAN endpoint a name, then call it
- **Workflows:** Write Lua scripts to automate anything iontk can do
- **Read-only by default:** No writes unless you explicitly pass `--doit`
- **Customizable:** Change everything! Columns, pages, keybindings, menus, themes, and API endpoints
- **Used in production:** Running across a fleet of 6000 devices, tested at [230 sessions a minute](https://github.com/tyrtop/iontoolkit/pull/8)

## Installation

### Download a Binary

Linux and Windows binaries are on the
[releases page](https://github.com/tyrtop/iontoolkit/releases).

### Go install

`go install tyrtop.com/iontk/cmd/iontk@latest`

### Build from Source

1. `git clone https://github.com/tyrtop/iontoolkit.git`
2. `cd iontoolkit`
3. `go build -o iontk ./cmd/iontk`

## Getting Started

The quickest way to see iontk is to run the demo fleet. 

`iontk --demo`

When you want the real thing, just run it.

`iontk`

## Authentication

You can sign in with your browser, with a service account, or with both, even across different tenants.

### Browser Authentication

Sign in through the TUI or with `iontk auth login`. iontk saves your browser token for eight hours. 

    iontk auth login

### Service Accounts

You can sign in with a service account via the TUI or via `iontk tenant add`. 

    iontk tenant add --name "Acme Co" --tsg 1234567890 --client_id sa@1234567890.iam.panserviceaccount.com --secret -

`--secret -` reads the secret from standard input, so it stays out of your
shell history.

You can also configure one using environment variables or a .env file in the working directory.

    SCM_CLIENT_ID      Service account client ID.
    SCM_CLIENT_SECRET  Service account client secret.
    SCM_TSG_ID         Tenant Service Group ID.
    SCM_TOKEN          Bearer token. Overrides the service account.
    ION_USER           CLI login. Required for batch.
    ION_PASS           CLI password. Required for batch.

Use SCM_TOKEN only when a service account isn't available.

## Using iontk

Select IONs with space and press enter to open them in the current mode. Switch modes with alt+a or alt+m.

### Element Search

Press `/` and start typing to search. Press `enter` when you're done. 

![list](https://media.tyrtop.com/iontk/tui-filter-v1.gif)

You can search from your terminal.

    iontk fleet search "ion 1200 offline"
    iontk fleet search "store 0003" --format json

![search](https://media.tyrtop.com/iontk/cli-search-v3.gif)

### Shells

By default, iontk starts in **Shell Mode**. Use **Shell Mode** to open interactive shells for selected IONs.

    alt+h / alt+left     previous tab
    alt+l / alt+right    next tab
    alt+w                detach
    alt+t                reattach
    alt+k                kill
    /commands            search the ION command reference

![shells](https://media.tyrtop.com/iontk/tui-shells-v2.gif)

### Rich Data

Switch to **Rich Data Mode** to view Strata Cloud data for the selected IONs.

![data](https://media.tyrtop.com/iontk/tui-data-v1.gif)

### Batch

Switch to **Batch Mode** to run CLI commands against selected IONs.

![batch](https://media.tyrtop.com/iontk/tui-batch-v3.gif)

Run batch commands from your terminal:

    iontk batch run --search "acme store 0003" -c "dump spoke-ha status"

![cli batch](https://media.tyrtop.com/iontk/cli-batch-v2.gif)

You get JSON back by default. Change it with `--format`. Use `json`, `jsonl`, `text`,
`table` or `csv`.

You can pipe commands together. 

    iontk fleet search "dc east" --format ids --no-progress | iontk batch run -f - -c "dump software status" --format text --no-progress

![pipe](https://media.tyrtop.com/iontk/cli-pipe-v3.gif)

## Automation

### Commands

Every action in iontk is a command with consistent names. 

    iontk fleet search "store 0003"                      # terminal
    :fleet.search query="store 0003"                     # TUI
    ctx.run("fleet.search", { query = "store 0003" })    -- Lua

### Endpoints

iontk can call anything under `/sdwan/` on the controller. It already has
names for 1,146 endpoints:

    iontk endpoints list --q sites

You can call endpoints from Lua.

    local _, sites = ctx.run("endpoint.sites", {})

If the one you want isn't there, or you want a different version, give it a
name in your `workflow.yaml`. Your endpoints win over iontk endpoints. 

    endpoints:
    - name: sites
      path: /sdwan/v4.9/api/sites

GET, POST, PUT, PATCH, and DELETE are supported. Writes are dry runs by default. Use the `--doit` flag to execute them.

### Workflows

A workflow is a folder with a `workflow.yaml` and a `main.lua`. Longer jobs can be split into steps, each one a workflow of its own. A workflow can run any command iontk has and call any endpoint under `/sdwan/`.

`workflow.yaml` defines what the workflow does, its inputs, and the endpoints.

    title: Site report
    description: Lists a site's routers and what dump spoke-ha status returns on each.
    inputs:
      - name: site
        prompt: Site name
        required: true

`main.lua` handles logic and decisions.

    return function(ctx)
      local _, sites = ctx.run("endpoint.sites", {})
      local site = ctx.lookup(sites.items, "name", ctx.input.site)
      if not site then ctx.fail("no site is called " .. ctx.input.site) end

      local _, els = ctx.run("endpoint.elements", {})
      local ids = {}
      for _, el in ipairs(els.items) do
        if el.site_id == site.id then
          ctx.status(el.name .. "  " .. el.model_name .. "  " .. el.software_version)
          ids[#ids + 1] = el.id
        end
      end

      local _, res = ctx.run("batch.run", { elements = table.concat(ids, ","), cmd = "dump spoke-ha status" })
      for _, r in ipairs(res.Results or {}) do
        if r.error then
          ctx.status((r.name or r.element_id) .. "  failed: " .. r.error)
        else
          local active = r.commands[1].output:match("Active%s*:%s*(%a+)")
          ctx.status(r.name .. "  active " .. tostring(active))
        end
      end

      return { site = site.name, routers = #ids }
    end

Run it.

    iontk workflow examples/workflows/site-report --site "Store 0003" --tenant "Acme Co"

![workflow](https://media.tyrtop.com/iontk/cli-workflow-v3.gif)

You can run a workflow once per CSV row.

    iontk workflow examples/workflows/site-report --inputs rows.csv

You can also run it from the TUI. 

![tui workflow](https://media.tyrtop.com/iontk/tui-workflow-v2.gif)

If a run stops partway, use --resume to continue from where the workflow stopped.

There's a longer guide in [agents/workflows.md](agents/workflows.md).

### Writes

Everything is read-only until you say so.

    iontk batch run -e 1000000000000000001 -c "config interface 1 shutdown"

That one prints what it would run and stops. 

    iontk write interface -e 1000000000000000001 --match 1 --set description="uplink to core"

That one prints the old and new values and stops. 

![safety](https://media.tyrtop.com/iontk/cli-safety-v4.gif)

## Customization

### Screens

Every screen is configurable and defined in YAML. Change columns, data pages, keybindings, menus, and actions by pressing `alt+e` on any screen.

You can also load your own files with `--columns`, `--views`, and `--keybinds`.

### Themes

There are eleven themes built in, and they preview as you move through the
list. If you don't like any of them, there's an editor with a color wheel.

![themes](https://media.tyrtop.com/iontk/tui-theme-v1.gif)

## AI Agents

Point your favorite agent at [agents/](agents/) and let it figure out the rest.

## By the Way

There's more to discover! Macros, shell favorites on `alt+1` to `alt+9`, `iontk ha sweep` for HA pairs, several ION users per tenant, and `--fixtures` for a fake fleet of your own. The flag reference is in `docs/`.

Want to know how it works? I wrote about it:

- [Building iontoolkit](https://tyrtop.com/creating-iontk.html)
- [Synthetic Monitoring with iontoolkit](https://tyrtop.com/synthetic-monitoring.html)

## Feedback

Found a bug?
[Open an issue](https://github.com/tyrtop/iontoolkit/issues).

## License

[Apache-2.0](LICENSE)
