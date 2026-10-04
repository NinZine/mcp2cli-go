---
name: mcp
description: Add, inspect, and call arbitrary Model Context Protocol servers on demand with the generic `mcp` CLI. Use when the user wants to explore an MCP server, list or call its tools, connect a new MCP server over stdio or HTTP, or work with an MCP endpoint that is not yet configured. Read-only by default; mutate only on explicit intent.
tags:
  - mcp
  - model-context-protocol
  - mcp-cli
  - tools
  - tool-calling
  - server
  - discover
  - explore
  - stdio
  - http
  - add
metadata:
  openclaw:
    requires:
      bins:
        - mcp
---

# MCP servers on demand

The `mcp` CLI connects to any [Model Context Protocol](https://modelcontextprotocol.io)
server, lists its tools, and calls them. Use it to explore and use MCP servers
that are not already wired into the agent.

- One binary, any server. No SDKs, no wrapper code.
- Output is JSON when piped (the usual case for an agent); `--json` forces it.
- Read-only by default. Call mutating tools only on explicit user intent.

## Quick reference

```bash
mcp --list                                   # servers already configured
mcp <server>                                 # same as: mcp <server> --list
mcp <server> --list                          # tool names
mcp <server> --info                          # tools + input schemas
mcp <server> --health                        # reachability + protocol version
mcp <server> <tool> '<json_args>'            # call a tool
mcp add --url <url> <name>                   # persist an HTTP server
mcp remove <name>                            # remove a server
mcp config path                              # config file location
mcp config edit                              # open the config in $EDITOR (interactive)
```

Global flags: `--json` (force JSON, anywhere), `--help`/`-h`, `--version`/`-V`/`-v`.

## Step 1 — is it already configured?

```bash
mcp --list --json
```

If the server is listed, skip to "Explore" or "Call a tool". If not, add it.

## Step 2 — add a server

Two connection types:

- **HTTP** — a remote URL (`url`, optional `headers`).
- **stdio** — a local process (`command`, `args`, `env`).

### HTTP, persistent (recommended)

```bash
mcp add --url https://mcp.example.com/mcp <name>
```

`--url` must be absolute with an `http` or `https` scheme. The command edits the
config file, preserves your other servers, and refuses reserved names
(`add`, `remove`, `config`, …) and duplicates. A URL whose path ends in `/sse`
uses the SSE transport; anything else uses Streamable HTTP.

### stdio, ephemeral (best for one-off exploration)

No file edit, no cleanup. Pass the whole config inline for the single command:

```bash
MCP_SERVERS_CONFIG='{"mcpServers":{"<name>":{"command":"npx","args":["-y","@scope/server"]}}}' \
  mcp <name> --list
```

Add `"env": {"API_KEY": "${API_KEY}"}` for servers that need credentials, and
export the variable in the same shell.

### stdio, persistent

`mcp add` only handles HTTP. To persist a stdio server, read the config
(`mcp config path`), add the entry under `mcpServers`, and write the JSON back:

```json
{
  "mcpServers": {
    "<name>": {
      "command": "npx",
      "args": ["-y", "@scope/server"],
      "env": { "API_KEY": "${API_KEY}" }
    }
  }
}
```

`mcp config edit` opens an interactive `$EDITOR` and is not usable from an
agent — edit the file directly instead. The config accepts `//` and `/* */`
comments on read, but any rewrite through the CLI (or by hand as JSON) drops
them. Prefer the inline form for experiments and only persist servers the user
wants to keep.

### Authentication

HTTP servers take an `Authorization` header. Put it in the server entry and
reference an environment variable — never hardcode a token and never echo it:

```json
{ "url": "https://mcp.example.com/mcp", "headers": { "Authorization": "Bearer ${EXAMPLE_TOKEN}" } }
```

If a call returns `401`, ask the user for a token; this CLI does not run an
interactive OAuth flow.

## Step 3 — explore the server

```bash
mcp <server> --health --json      # {"server":"…","status":"ok","protocolVersion":"…"}
mcp <server> --list --json        # [{ "name": "…", "description": "…" }, …]
mcp <server> --info --json        # same, plus each tool's full inputSchema
```

Always prefer `--info` before calling a tool: the `inputSchema` is the
authoritative list of argument names and types. It is passed through verbatim
from the server.

## Step 4 — call a tool

```bash
mcp <server> <tool> '{"query":"is:open"}'
# or pipe the JSON:
echo '{"query":"is:open"}' | mcp <server> <tool>
# no arguments:
mcp <server> <tool>
```

Arguments are a JSON **object**. A malformed or non-object argument is an error
(exit 1) and the tool is not called.

### Reading the response

The result is the MCP envelope:

```json
{
  "content": [ { "type": "text", "text": "<result>" } ],
  "isError": false
}
```

- The tool's real payload is often a JSON **string** in `content[0].text` — parse
  it a second time.
- `isError: true` means the tool ran and reported a failure. **The CLI still
  exits 0**, so check `isError`, not just the exit code.
- `content` may also contain `image`, `resource`, or other block types.
- Unknown tools, invalid arguments, and transport/protocol failures exit
  non-zero; a tool that merely reports `isError` does not.

Pipe through `jq` for readability:

```bash
mcp <server> <tool> '{}' | jq -r '.content[0].text'
```

## Step 5 — clean up

```bash
mcp remove <name>
```

Removes the server from the config. Ephemeral inline servers need no cleanup.

## Config and environment

| Thing | Default |
|---|---|
| Config file | `~/.config/mcp/servers.json` (override with `MCP_CONFIG_PATH`) |
| `MCP_SERVERS_CONFIG` | Inline config JSON; overrides all file locations |
| `MCP_CONFIG_PATH` / `MCP_CONFIG_DIR` | Explicit config file / directory |
| `MCP_TIMEOUT` | Per-request timeout in seconds (default `60`) |
| `MCP_LOG_LEVEL` | `debug`, `info` (default), `warn`, `error` |

`${VAR}` in any config string is replaced at load time; a missing variable
becomes an empty string.

## Guardrails

1. **Default to read.** Inspect (`--list`, `--info`) before calling. Call a
   create/update/delete/send tool only when the user explicitly asks for that
   change.
2. **Echo the target first.** Before a destructive call, state the exact
   server, tool, and IDs you are about to act on.
3. **Never print secrets.** Tokens live in config headers or environment
   variables; do not echo them or paste them into commands.
4. **Resolve names, don't invent identifiers.** If a tool returns a list, read
   the real IDs from it; never fabricate IDs, names, or status values. An empty
   result is a valid finding — report it.
5. **Prefer an existing configured server** over adding a duplicate.

## Troubleshooting

| Symptom | Cause / fix |
|---|---|
| `server "x" not found in config` | Not added yet (check `mcp --list`), or a typo. |
| `command not found in PATH: npx` | The stdio command isn't installed. Install it (`node`/`npx`, `uvx`, `docker`) or use an absolute path. `mcp --list` marks such servers `"available": false`. |
| Call returns `isError: true` | Tool-level failure. Read `content[0].text` for the reason; often a missing/invalid argument — re-check `--info`. |
| HTTP `401` | Auth required. Add an `Authorization` header from an env var (ask the user for a token). |
| Request hangs / times out | Raise `MCP_TIMEOUT` (e.g. `MCP_TIMEOUT=120`); some servers are slow on first start. |
| `arguments must be a JSON object` | Pass an object (`{}`), not an array or scalar. |
| Config edits lost comments | Rewrites go through a JSON parser; comments are dropped by design. |

## Examples

```bash
# Explore an already-configured server
mcp sentry --list --json | jq -r '.[].name'
mcp sentry --info --json | jq '.[] | select(.name=="search_issues") | .inputSchema'

# Add and use a remote server
mcp add --url https://mcp.sentry.dev/mcp sentry
mcp sentry search_issues '{"query":"is:unresolved level:error"}'

# One-off local server with no config change
MCP_SERVERS_CONFIG='{"mcpServers":{"fs":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}}}' \
  mcp fs --list --json

# One-off tool call, reading the payload
MCP_SERVERS_CONFIG='{"mcpServers":{"fs":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp"]}}}' \
  mcp fs read_file '{"path":"/tmp/example.txt"}' | jq -r '.content[0].text'
```
