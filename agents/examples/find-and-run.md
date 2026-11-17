# Find elements and run a command on them

Build iontk and use the fake fleet:

    go build -o iontk ./cmd/iontk

## 1. Find the elements

    ./iontk --demo fleet search "acme store 0003" --format json

Check the `tenant` and `connected` fields. "store 0003" without "acme"
matches a Store 0003 in each of the two demo tenants. Don't run on an
element that isn't connected.

## 2. Run the command

    ./iontk --demo fleet search "acme store 0003" --format ids --no-progress |
      ./iontk --demo batch run -f - -c "dump device status" --no-progress

## 3. Read the result

    [
      {
        "element_id": "1000000000000000001",
        "name": "store-0003-rtr-01",
        "hw_id": "5C260A1F0003",
        "model_name": "ion 1200",
        "software_version": "6.5.1",
        "connected": true,
        "commands": [
          { "command": "dump device status", "output": "Fake shell! Testing Mode!" }
        ]
      },
      ...
    ]

One object per element, one entry in `commands` per command, in the order
you gave them. An element that failed has an `error`. The exit code is 1
when any element failed, and the JSON is still complete.

The fake fleet answers a device command with `Fake shell! Testing Mode!`
unless the fixtures file has an answer for it.

`--format jsonl` gives one element a line.

## A command that changes something

    ./iontk --demo batch run -e 1000000000000000001 -c "config interface 1 shutdown"

    plan config interface 1 shutdown on store-0003-rtr-01
    dry run: 1 element(s), none sent — add --doit to send

End there and show the user.
