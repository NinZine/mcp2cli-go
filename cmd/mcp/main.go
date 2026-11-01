// Command mcp calls Model Context Protocol servers from the terminal.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/NinZine/mcp2cli-go/internal/client"
	"github.com/NinZine/mcp2cli-go/internal/config"
	"github.com/NinZine/mcp2cli-go/internal/output"
	"github.com/NinZine/mcp2cli-go/internal/preflight"
)

// version is injected at build time via -ldflags "-X main.version=...".
// The default is non-empty so `mcp --version` always prints two fields.
var version = "dev"

const usage = `mcp — call MCP servers from the terminal

Usage:
  mcp --list                          List configured servers
  mcp <server> --list                 List tools from a server
  mcp <server> --info                 List tools with input schemas
  mcp <server> --health               Check if server is reachable
  mcp <server> <tool> [json]          Call a tool (reads JSON from stdin if omitted)
  mcp add --url <url> <name>          Add an HTTP server to the config
  mcp remove <name>                    Remove a server from the config
  mcp config path                     Show config file path
  mcp config edit                     Open config in $EDITOR
  mcp completions <shell>             Generate shell completions (bash, zsh, fish)

Flags:
  --version, -V                       Print the version
  --help, -h                          Show this help
  --json                              Force JSON output

Output defaults to human-readable text on a terminal and JSON when piped.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	jsonFlag, args := extractJSONFlag(args)

	// Version and help are handled before any config access so that a broken
	// config cannot prevent them.
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-V", "-v":
			fmt.Fprintf(stdout, "mcp %s\n", version)
			return 0
		case "--help", "-h":
			fmt.Fprint(stderr, usage)
			return 0
		}
	}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 0
	}

	renderer := &output.Renderer{
		JSON: jsonFlag || !isTerminal(stdout),
		Out:  stdout,
		Err:  stderr,
	}

	fail := func(err error) int {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return 1
	}

	switch args[0] {
	case "--list":
		return runListServers(renderer, stderr)
	case "add":
		return runAdd(stderr, args[1:])
	case "remove":
		return runRemove(stderr, args[1:])
	case "config":
		return runConfig(renderer, stderr, args[1:], stdout)
	case "completions":
		return runCompletions(stdout, stderr, args[1:])
	}

	return runServer(renderer, stderr, stdin, args, fail)
}

// extractJSONFlag removes every --json occurrence from args and reports whether
// any were present.
func extractJSONFlag(args []string) (bool, []string) {
	found := false
	out := make([]string, 0, len(args))
	for _, a := range args {
		if a == "--json" {
			found = true
			continue
		}
		out = append(out, a)
	}
	return found, out
}

func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// ---------------------------------------------------------------------------
// Server listing
// ---------------------------------------------------------------------------

func runListServers(r *output.Renderer, stderr io.Writer) int {
	set, err := config.Load()
	if err != nil {
		return writeConfigError(stderr, err)
	}
	writeWarnings(stderr, set.Warnings)

	entries := make([]output.ServerEntry, 0, len(set.Servers))
	for _, srv := range set.Servers {
		entry := output.ServerEntry{Name: srv.Name, Type: string(srv.Type)}
		if srv.Type == config.TypeHTTP {
			entry.URL = srv.URL
			entries = append(entries, entry)
			continue
		}
		entry.Command = srv.Command
		entry.Args = srv.Args
		ok, reason := preflight.Available(srv.Command, srv.Env)
		available := ok
		entry.Available = &available
		entry.UnavailableReason = reason
		entries = append(entries, entry)
	}

	if err := r.ServerList(entries); err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// add / remove (explicit config-file edits)
// ---------------------------------------------------------------------------

const addUsage = `Usage:
  mcp add --url <url> <name>          Add an HTTP server to the config
`

const removeUsage = `Usage:
  mcp remove <name>                    Remove a server from the config
`

// runAdd edits the config *file* resolved by config.ResolvePath() (never the
// inline MCP_SERVERS_CONFIG) to add an HTTP server.
func runAdd(stderr io.Writer, args []string) int {
	rawURL := ""
	var names []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--url":
			if i+1 >= len(args) {
				fmt.Fprint(stderr, addUsage)
				return 1
			}
			rawURL = args[i+1]
			i++
		case strings.HasPrefix(a, "--url="):
			rawURL = strings.TrimPrefix(a, "--url=")
		case strings.HasPrefix(a, "-"):
			fmt.Fprint(stderr, addUsage)
			return 1
		default:
			names = append(names, a)
		}
	}
	if rawURL == "" || len(names) != 1 {
		fmt.Fprint(stderr, addUsage)
		return 1
	}

	name := names[0]
	if name == "" {
		fmt.Fprint(stderr, "mcp: server name must not be empty\n")
		return 1
	}
	if config.IsReserved(name) {
		fmt.Fprintf(stderr, "mcp: %q is a reserved server name\n", name)
		return 1
	}

	path, warnings := config.ResolvePath()
	writeWarnings(stderr, warnings)
	if err := config.AddHTTPServer(path, name, rawURL); err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "Server %q added to %s\n", name, path)
	return 0
}

// runRemove edits the config *file* resolved by config.ResolvePath() (never the
// inline MCP_SERVERS_CONFIG) to delete a server.
func runRemove(stderr io.Writer, args []string) int {
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(stderr, removeUsage)
		return 1
	}
	name := args[0]

	path, warnings := config.ResolvePath()
	writeWarnings(stderr, warnings)
	if err := config.RemoveServer(path, name); err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return 1
	}
	fmt.Fprintf(stderr, "Server %q removed from %s\n", name, path)
	return 0
}

// ---------------------------------------------------------------------------
// Server operations
// ---------------------------------------------------------------------------

func runServer(r *output.Renderer, stderr io.Writer, stdin io.Reader, args []string, fail func(error) int) int {
	name := args[0]

	set, err := config.Load()
	if err != nil {
		return writeConfigError(stderr, err)
	}
	writeWarnings(stderr, set.Warnings)

	srv := set.Find(name)
	if srv == nil {
		fmt.Fprintf(stderr, "mcp: server %q not found in config\n", name)
		return 1
	}

	mode := "--list"
	if len(args) >= 2 {
		switch args[1] {
		case "--list", "--info", "--health":
			mode = args[1]
		default:
			mode = "call"
		}
	}

	// PATH preflight for stdio servers before spawning.
	if srv.Type == config.TypeStdio {
		if ok, reason := preflight.Available(srv.Command, srv.Env); !ok {
			fmt.Fprintf(stderr, "mcp: %s\n", reason)
			return 1
		}
	}

	timeout := requestTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	conn, err := client.Connect(ctx, srv, version)
	if err != nil {
		return fail(err)
	}
	defer conn.Close()

	switch mode {
	case "--list":
		tools, err := conn.ListTools(ctx)
		if err != nil {
			return fail(err)
		}
		if err := r.ToolList(tools); err != nil {
			return fail(err)
		}
	case "--info":
		tools, err := conn.ListTools(ctx)
		if err != nil {
			return fail(err)
		}
		if err := r.ToolInfo(tools); err != nil {
			return fail(err)
		}
	case "--health":
		if err := r.Health(name, conn.ProtocolVersion()); err != nil {
			return fail(err)
		}
	default:
		toolName := args[1]
		argJSON := ""
		hasArg := false
		if len(args) >= 3 {
			argJSON = args[2]
			hasArg = true
		}
		parsed, err := parseToolArgs(argJSON, hasArg, stdin)
		if err != nil {
			return fail(err)
		}
		res, err := conn.CallTool(ctx, toolName, parsed)
		if err != nil {
			return fail(err)
		}
		if err := r.ToolResult(res); err != nil {
			return fail(err)
		}
	}
	return 0
}

// maxTimeoutSeconds is the largest whole-second duration that does not overflow
// time.Duration when multiplied by time.Second.
const maxTimeoutSeconds = int64(math.MaxInt64) / int64(time.Second)

func requestTimeout() time.Duration {
	raw := os.Getenv("MCP_TIMEOUT")
	if raw == "" {
		return 60 * time.Second
	}
	seconds, err := strconv.Atoi(raw)
	if err != nil || seconds <= 0 || int64(seconds) > maxTimeoutSeconds {
		// Invalid, non-positive, or overflowing values fall back to the
		// default so the derived context cannot expire immediately.
		return 60 * time.Second
	}
	return time.Duration(seconds) * time.Second
}

// parseToolArgs resolves tool arguments from the positional argument or, when
// absent and stdin is not a terminal, from stdin. The result must be a JSON
// object.
func parseToolArgs(positional string, hasPositional bool, stdin io.Reader) (map[string]any, error) {
	var data []byte
	switch {
	case hasPositional:
		data = []byte(positional)
	case !isTerminalReader(stdin):
		b, err := io.ReadAll(stdin)
		if err != nil {
			return nil, fmt.Errorf("reading stdin: %w", err)
		}
		if len(strings.TrimSpace(string(b))) == 0 {
			return map[string]any{}, nil
		}
		data = b
	default:
		return map[string]any{}, nil
	}

	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("invalid JSON arguments: %w", err)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, errors.New("arguments must be a JSON object")
	}
	return obj, nil
}

func isTerminalReader(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// ---------------------------------------------------------------------------
// config path / config edit
// ---------------------------------------------------------------------------

func runConfig(r *output.Renderer, stderr io.Writer, args []string, stdout io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1
	}
	path, warnings := config.ResolvePath()
	writeWarnings(stderr, warnings)

	switch args[0] {
	case "path":
		exists := false
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			exists = true
		}
		if err := r.ConfigPath(path, exists, filepath.Dir(path)); err != nil {
			fmt.Fprintf(stderr, "mcp: %v\n", err)
			return 1
		}
		return 0
	case "edit":
		return runConfigEdit(path, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "mcp: unknown config subcommand %q\n", args[0])
		return 1
	}
}

func runConfigEdit(path string, stdout, stderr io.Writer) int {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(stderr, "mcp: %v\n", err)
		return 1
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			fmt.Fprintf(stderr, "mcp: %v\n", err)
			return 1
		}
	}

	editor := os.Getenv("EDITOR")
	if strings.TrimSpace(editor) == "" {
		editor = os.Getenv("VISUAL")
	}
	if strings.TrimSpace(editor) == "" {
		if runtime.GOOS == "windows" {
			editor = "notepad"
		} else {
			editor = "vi"
		}
	}

	// Split the editor string so values like EDITOR="code --wait" work. The
	// config path is appended as the final argument; no shell is involved.
	argv := append(strings.Fields(editor), path)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(stderr, "mcp: editor %q failed: %v\n", editor, err)
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// completions
// ---------------------------------------------------------------------------

func runCompletions(stdout, stderr io.Writer, args []string) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, "unsupported shell:  (use bash, zsh, or fish)\n")
		return 1
	}
	script, ok := completionScripts[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "unsupported shell: %s (use bash, zsh, or fish)\n", args[0])
		return 1
	}
	fmt.Fprint(stdout, script)
	return 0
}

var completionScripts = map[string]string{
	"bash": `# bash completion for mcp
_mcp() {
  local cur prev
  COMPREPLY=()
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  local commands="--list add remove config completions --json --version --help -V -v -h"
  if [ "$prev" = "completions" ]; then
    COMPREPLY=( $(compgen -W "bash zsh fish" -- "$cur") )
    return 0
  fi
  if [ "$prev" = "config" ]; then
    COMPREPLY=( $(compgen -W "path edit" -- "$cur") )
    return 0
  fi
  COMPREPLY=( $(compgen -W "$commands" -- "$cur") )
  return 0
}
complete -F _mcp mcp
`,
	"zsh": `#compdef mcp
_mcp() {
  local -a commands
  commands=(--list add remove config completions --json --version --help -V -v -h)
  _arguments '1: :($commands)' '*::arg:->args'
}
compdef _mcp mcp
`,
	"fish": `# fish completion for mcp
complete -c mcp -f
complete -c mcp -n '__fish_use_subcommand' -a '--list' -d 'List configured servers'
complete -c mcp -n '__fish_use_subcommand' -a 'add' -d 'Add an HTTP server to the config'
complete -c mcp -n '__fish_use_subcommand' -a 'remove' -d 'Remove a server from the config'
complete -c mcp -n '__fish_use_subcommand' -a 'config' -d 'Config commands'
complete -c mcp -n '__fish_use_subcommand' -a 'completions' -d 'Shell completions'
complete -c mcp -n '__fish_seen_subcommand_from completions' -a 'bash zsh fish'
complete -c mcp -n '__fish_seen_subcommand_from config' -a 'path edit'
complete -c mcp -l json -d 'Force JSON output'
complete -c mcp -l version -s V -d 'Print the version'
complete -c mcp -l help -s h -d 'Show help'
`,
}

// ---------------------------------------------------------------------------
// shared error helpers
// ---------------------------------------------------------------------------

func writeConfigError(stderr io.Writer, err error) int {
	fmt.Fprintf(stderr, "mcp: %v\n", err)
	return 1
}

func writeWarnings(stderr io.Writer, warnings []string) {
	if len(warnings) == 0 {
		return
	}
	// Reserved-name and path warnings are warn level (SPEC §7): shown at
	// debug/info/warn, suppressed at error.
	log := newLogger(stderr)
	for _, w := range warnings {
		log.warnf("%s", w)
	}
}
