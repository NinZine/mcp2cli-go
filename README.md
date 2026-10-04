# mcp

Call [Model Context Protocol](https://modelcontextprotocol.io) (MCP) servers from the terminal: JSON in, JSON out, no daemon.

`mcp` connects to a configured MCP server over stdio, Streamable HTTP, or SSE, lists its tools, and calls them. Output is human-readable text when attached to a terminal and machine-parseable JSON when piped.

## Features

- **Transports**: stdio (spawns a child process), Streamable HTTP, and SSE (selected automatically when the URL path ends in `/sse`).
- **Commands**: list configured servers, list a server's tools, show tool input schemas, check health, and call tools.
- **Config**: a single JSON document (`{"mcpServers": {...}}`) with `${VAR}` environment substitution and `//` / `/* */` comment stripping.
- **Output**: text on a TTY, JSON when piped; `--json` forces JSON anywhere.
- **Shell completions**: static scripts for bash, zsh, and fish.
- **Zero runtime services**: no daemon, no database, no background process.

## Installation / build

The binary is named `mcp`.

```sh
# Build into the current directory
go build -o mcp ./cmd/mcp

# Install into $GOBIN / $GOPATH/bin
go install ./cmd/mcp
```

The version is injected at build time via `-ldflags "-X main.version=..."`. The variable is `main.version` (see `cmd/mcp/main.go`); its default is `dev`, so `mcp --version` always prints two fields.

```sh
go build -ldflags "-X main.version=1.2.3" -o mcp ./cmd/mcp
./mcp --version   # mcp 1.2.3
```

## Quick start

Point `mcp` at a config with `MCP_SERVERS_CONFIG` (inline JSON) or a config file.

```sh
# Inline stdio server
export MCP_SERVERS_CONFIG='{
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": { "GITHUB_TOKEN": "${GITHUB_TOKEN}" }
    }
  }
}'

mcp --list                       # list configured servers
mcp github --list                # list the server's tools
mcp github --info                # tools with input schemas
mcp github --health              # connect and report protocol version
mcp github search_issues '{"query":"is:open"}'   # call a tool
```

An HTTP server config:

```json
{
  "mcpServers": {
    "sentry": {
      "url": "https://mcp.sentry.dev/mcp",
      "headers": { "Authorization": "Bearer ${SENTRY_TOKEN}" }
    }
  }
}
```

## Configuration

### Resolution order

The config location is resolved in this order (first match wins):

1. `MCP_SERVERS_CONFIG` — inline JSON content (no file read).
2. `MCP_CONFIG_PATH` — explicit file path.
3. `MCP_CONFIG_DIR` — directory; the file is `<dir>/servers.json`.
4. `$HOME/.config/mcp/servers.json`.
5. `/tmp/mcp/servers.json` if the home directory cannot be determined (a warning is emitted).

A missing file is an empty server set, not an error. A malformed file is an error.

### Format

```json
{
  "mcpServers": {
    "<name>": { "...server..." }
  }
}
```

Before parsing, the raw text is processed in order:

1. **Environment substitution** — every `${VAR_NAME}` is replaced with the value of that environment variable, or the empty string if unset. Applies to all string values (URLs, args, env values, headers).
2. **Comment removal** — `//` line comments and `/* ... */` block comments are stripped, while those sequences inside JSON string literals are preserved (so `"https://host"` is untouched).

Unknown top-level keys and unknown per-server keys are ignored. A top-level value that is not an object, or `mcpServers` that is not an object, is an error.

### Server types

The type is inferred from the fields present.

**stdio** — has `command` (and not `"cli": true`):

```json
{
  "command": "npx",
  "args": ["-y", "@modelcontextprotocol/server-github"],
  "env": { "GITHUB_TOKEN": "${GITHUB_TOKEN}" }
}
```

| Field | Type | Default | Meaning |
|---|---|---|---|
| `command` | string | required | executable to spawn |
| `args` | string[] | `[]` | arguments to the executable |
| `env` | object | `{}` | extra environment variables for the process |

**http** — has `url`:

```json
{
  "url": "https://mcp.example.com/mcp",
  "headers": { "Authorization": "Bearer ${EXAMPLE_TOKEN}" }
}
```

| Field | Type | Default | Meaning |
|---|---|---|---|
| `url` | string | required | MCP endpoint URL |
| `headers` | object | `{}` | headers sent on every request |

An entry with neither `command` nor `url` is an error. If `env` or `headers` is present but not an object, it is an error. A URL whose path ends in `/sse` uses the SSE transport; all other HTTP URLs use Streamable HTTP.

### Reserved server names

The following names are reserved and are skipped with a warning if present in config:

```
search, add, remove, update, list, help, version, serve, logs, acl, config,
completions, healthcheck
```

## Commands reference

| Command | Description |
|---|---|
| `mcp --list` | List configured servers |
| `mcp <server>` | Same as `mcp <server> --list` |
| `mcp <server> --list` | List the server's tools |
| `mcp <server> --info` | List tools with input schemas |
| `mcp <server> --health` | Connect, confirm reachability, print protocol version |
| `mcp <server> <tool> [json]` | Call a tool |
| `mcp config path` | Print the resolved config file path |
| `mcp config edit` | Open the config file in `$EDITOR` |
| `mcp completions <bash\|zsh\|fish>` | Print a shell completion script |

Any other first argument is treated as a server name. If it is not found, `mcp` prints `server "<name>" not found in config` to stderr and exits 1.

### Global flags

| Flag | Effect |
|---|---|
| `--json` | Force JSON output (may appear anywhere) |
| `--version`, `-V`, `-v` | Print `mcp <version>`, exit 0 |
| `--help`, `-h` | Print usage to stderr, exit 0 |

## Output

- All command data goes to **stdout**; errors, warnings, and progress go to **stderr**.
- Text output is used only when stdout is a terminal and `--json` is absent. Otherwise JSON is used.
- `--json` is global: every occurrence is removed from the arguments before dispatch, so it works in any position.
- JSON is pretty-printed with two-space indentation.
- In text mode, tool-level errors are printed to stderr with an `error:` prefix.

If a tool result is flagged `isError`, the exit code is still `0` for a successful protocol exchange (the tool ran and reported an error). Only transport/protocol failures exit non-zero.

## Tool arguments

Arguments for `mcp <server> <tool>` are resolved in this order:

1. A third positional argument, parsed as JSON.
2. Otherwise, if stdin is **not** a terminal, all of stdin is read; if non-empty it is parsed as JSON, if empty `{}` is used.
3. Otherwise (terminal, no argument), `{}` is used.

The argument must be a JSON object. A parse error or a non-object value is fatal (exit 1).

```sh
mcp github search_issues '{"query":"is:open"}'
echo '{"query":"is:open"}' | mcp github search_issues
mcp github search_issues
```

## Environment variables

| Variable | Default | Meaning |
|---|---|---|
| `MCP_SERVERS_CONFIG` | — | inline config JSON; overrides all file locations |
| `MCP_CONFIG_PATH` | `~/.config/mcp/servers.json` | config file path |
| `MCP_CONFIG_DIR` | `~/.config/mcp` | config directory |
| `MCP_TIMEOUT` | `60` | per-request timeout in seconds |
| `MCP_LOG_LEVEL` | `info` | stderr log level: `debug`, `info`, `warn`, `error` |
| `EDITOR`, `VISUAL` | `vi` (`notepad` on Windows) | editor for `config edit` |

`MCP_TIMEOUT` values that are invalid, non-positive, or overflow fall back to the 60-second default. An unrecognized `MCP_LOG_LEVEL` falls back to `info`. Warnings (such as reserved server names) are shown at `debug`/`info`/`warn` and suppressed at `error`; errors are always shown.

## Shell completions

```sh
mcp completions bash
mcp completions zsh
mcp completions fish
```

Examples:

```sh
# bash
source <(mcp completions bash)

# zsh
mcp completions zsh > "${fpath[1]}/_mcp"

# fish
mcp completions fish > ~/.config/fish/completions/mcp.fish
```

An unsupported shell prints `unsupported shell: <x> (use bash, zsh, or fish)` to stderr and exits 1.

## Development

Run the unit tests and the black-box acceptance suite:

```sh
go test ./...
go test ./acceptance/ -v
```

The acceptance suite builds the `mcp` binary and a fake stdio MCP server (`./internal/testserver`) once in `TestMain`, then runs the CLI as a subprocess. HTTP tests use an in-process `httptest.Server`; no external network access is required.

### Package layout

```
cmd/mcp/main.go          // arg preprocessing, dispatch
cmd/mcp/log.go           // MCP_LOG_LEVEL-gated stderr logger
internal/config/         // locations, parse, substitution, comments, types
internal/client/         // config.Server -> mcp-go client, initialize, calls
internal/output/         // JSON/text rendering
internal/preflight/      // PATH check and exact reason strings
internal/testserver/     // fake stdio MCP server used by acceptance tests
acceptance/              // black-box CLI tests
```

The MCP protocol library is confined to `internal/client`, which exposes a small adapter (`Conn`, `Tool`, `Result`) to the rest of the program.

## Go version / dependencies

- Go **1.26.8** (see `go.mod`).
- [`github.com/mark3labs/mcp-go`](https://pkg.go.dev/github.com/mark3labs/mcp-go) `v1.1.1` — MCP clients (stdio, Streamable HTTP, SSE), `Initialize`, `ListTools`, `CallTool`, pagination, and stderr capture.
- [`golang.org/x/term`](https://pkg.go.dev/golang.org/x/term) `v0.46.0` — TTY detection.
- [`github.com/creack/pty`](https://pkg.go.dev/github.com/creack/pty) `v1.1.24` — used by the acceptance tests to exercise text output under a pseudo-terminal.
