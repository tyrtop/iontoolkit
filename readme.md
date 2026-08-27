# iontk

API-driven ION CLI management tooling built for command execution and retrieval and tested at [230 CLI sessions a minute](https://github.com/tyrtop/iontoolkit/pull/8). 

There is no officially supported path to perform scripted command execution on the Prisma SD-WAN IONs. ION appliances do not support programmatic management protocols, and are only meant to be managed by the Prisma SD-WAN controller, which has tenant-side limitations. This tool uses an existing API path in order to work within the architecture that is provided by Palo Alto while giving operators extended capabilities to perform programmatic command execution.

## Requirements

1. Go 1.26 or newer
2. Network access to `api.sase.paloaltonetworks.com` on 443 for both HTTPS and WSS 
3. A Prisma SD-WAN bearer token

## Installation

1. `git clone https://github.com/tyrtop/iontoolkit.git`
2. `cd iontoolkit`
3. `go build -o iontk .`

## Credentials

Credentials are read from the environment via `export SCM_TOKEN=...`, or from a `.env` in the working directory. See `.env.example`.

```
SCM_TOKEN   Bearer token for the Prisma SD-WAN API. Required.
ION_USER    CLI login. Required for -cmd.
ION_PASS    CLI password. Required for -cmd.
```

Currently, the token comes from an SCM browser session and expires after about 15 minutes. [Issue #10](https://github.com/tyrtop/iontoolkit/issues/10) is created to address the design and implementation of the service account flow. 

## Flags

```
-element           Element ID of the target ION.
-elements-path     File of element IDs, one per line. "-" reads stdin.
-cmd               Comma separated commands to run, then exit.
-http-timeout      Timeout for the element lookup. Default 15s.
-element-timeout   Per-element deadline including the CLI session. Default 60s.
-session-timeout   Deadline for a single login attempt. Default 20s.
-attempts          Login attempts before the element is dropped. Default 1.
-concurrency       Elements worked in parallel. Default 10.
-rps               Request rate ceiling against the Prisma SD-WAN API. Default 5.
-burst             Burst allowance on top of -rps. Default 10.
-v                 Verbose. Request details, rate limit headers, attempt errors.
```

## Interactive mode

Omitting `-cmd` flag drops the user into an interactive shell. `Ctrl-]` disconnects.

```
iontk -element 1234567890
```

## Batch mode

With `-cmd`, the tool logs into the element, runs the commands in order, and then returns only the ordered output as JSON. 

```
iontk -element 1234567890 -cmd "dump interface status 1, dump vpn summary"
```

To run across multiple elements, provide a .txt file with one element per line. Lines beginning with the `#` symbol will be skipped. 

```
iontk -elements-path sites.txt -cmd "dump interface status 1, dump device status" -concurrency 25
```

Results go to stdout as a JSON array, one object per element, carrying the element metadata and a `commands` array of command and output pairs. Elements that failed carry an `error` instead. Errors are also written to stderr. 

## Retries

This tool is designed to only be used with read-only commands:

1. Prisma SD-WAN IONs are designed as cloud managed devices. Mass CLI local *updates* are directly fighting the architecture as it is designed. Local updates should only happen for single devices and edge cases. 
2. The websocket session can suffer from intermittency. The `-attempts` flag is a way to alleviate connection issues and hung CLI sessions, but this tool will resend write commands to the CLI if you are not careful.

`-attempts` defaults to 1. Raise this ***only*** while using read-only commands to gain more consistent output.  

## Roadmap

See [issues #1-14, addressing bugs, typos, usability, basic functionality, and extended functionality](https://github.com/tyrtop/iontoolkit/issues).
