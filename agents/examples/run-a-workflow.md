# Run a workflow

    go build -o iontk ./cmd/iontk

## 1. Look at its inputs

    ./iontk --demo workflow examples/workflows/site-report --help

    Flags:
          --doit                 Send the writes instead of only listing them
          --exit-on-wait         Exit with code 75 at the first wait that isn't done yet ...
          --inputs file          Run once for each row of a CSV, YAML or JSON file ...
          --resume               Find the last write an earlier run got onto the portal ...
          --site string          Site name (required)
          --tenant stringArray   Tenant to run against, by name or TSG ID ...

`--site` is the workflow's own input. Every workflow has the other five.

## 2. Dry-run it

    ./iontk --demo workflow examples/workflows/site-report \
      --site "Store 0003" --tenant "Acme Co" --format json

    [
      {
        "workflow": "site-report",
        "tenant": "Acme Co",
        "tsg": "999000999",
        "inputs": { "site": "Store 0003" },
        "doit": false,
        "started": "2026-11-16T18:13:16-07:00",
        "finished": "2026-11-16T18:13:18-07:00",
        "status": [
          "store-0003-rtr-01  ion 1200  6.5.1",
          "store-0003-rtr-02  ion 1200  6.5.1",
          "store-0003-rtr-01  active nil",
          "store-0003-rtr-02  active nil"
        ],
        "data": { "routers": 2, "site": "Store 0003" }
      }
    ]

`active nil` is the fake fleet: its fake shell has no HA status to print.

Leave `--tenant` out and the run is rejected, because the demo has two
tenants loaded:

    2 tenants are loaded (Acme Co, Globex): say which with tenant

`site-report` only reads, so it has no `writes`. A workflow that writes adds
a `writes` list to the JSON, one entry per write, in order:

    "writes": [
      { "method": "PUT", "path": "/sdwan/v4.13/api/sites/185...", "body": { ... } }
    ]

Read those. They're what `--doit` will send. The same writes are printed
for people as `plan PUT /sdwan/...` lines, which aren't in `status`.

## 3. Give it to the user

For a workflow that writes, give the user the same command with `--doit`.
Don't run it.

    iontk workflow NAME --site "Store 0003" --tenant "Acme Co" --doit

## 4. If it ends early

Same inputs, plus `--resume`:

    iontk workflow NAME --site "Store 0003" --tenant "Acme Co" --doit --resume

Exit code 75 with `--exit-on-wait` means it's waiting on the controller.
Run the `--resume` form again later.
