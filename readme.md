# iontk

CLI for opening a toolkit session on a Prisma SD-WAN ION without going through the SCM UI. It hits the same SASE API the browser does: a REST call to look up the element, then a websocket into the toolkit session.

The SCM UI is the only supported way to reach an ION's toolkit. That works for one device. It doesn't script and it doesn't scale. This does.

## Requirements

- Network access to `api.sase.paloaltonetworks.com` over 443 (HTTPS and WSS)
- A Prisma SD-WAN bearer token
- Toolkit credentials for the element if you're using `-cmd`

## Environment variables

```
SCM_TOKEN   Bearer token for the Prisma SD-WAN API. Required.
ION_USER    Toolkit login. Required for -cmd.
ION_PASS    Toolkit password. Required for -cmd.
```

The token comes from an SCM browser session and expires after about 15 minutes. That's the current limitation. A service account fixes this and is the right way to run it in production.

## Flags

```
-element   Element ID of the target ION. Required.
-cmd       Run one toolkit command and exit.
-timeout   HTTP timeout for the element lookup. Default 15s.
-v         Verbose. Prints request details and raw websocket frames.
```

## One-shot mode

Runs a single command, strips the ANSI codes and the prompt, prints only the output. Use this for scripting.

```
export SCM_TOKEN=<token>
export ION_USER=<user>
export ION_PASS=<pass>
iontk -element 1234567890 -cmd "dump interface status"
```

Output goes to stdout, errors go to stderr. Safe to pipe.

## Interactive mode

Leave off `-cmd` and you get a live toolkit shell, same as SCM. Log in at the prompt like normal.

```
export SCM_TOKEN=<token>
iontk -element 1234567890
```

Press `Ctrl-]` to disconnect.

## Confirming the target

Before connecting, the tool prints the element's name, hardware ID, model, software version, and connected state. Check it before you start typing.

## Roadmap

- Run one command across multiple elements
- Service account auth to replace the UI token
