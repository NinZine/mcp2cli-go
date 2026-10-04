package acceptance_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// SPEC §9.7 — when $EDITOR is unset, $VISUAL is used to open the config.
func TestGap_ConfigEditVisualFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "servers.json")

	editor := filepath.Join(t.TempDir(), "visual-editor.sh")
	script := "#!/bin/sh\necho \"$1\" > \"$EDITOR_ARGS_FILE\"\nexit 0\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args.txt")

	// baseEnv deliberately omits EDITOR, so only VISUAL is available.
	r := run(t, runOpts{
		Env: baseEnv(
			"MCP_CONFIG_PATH="+path,
			"VISUAL="+editor,
			"EDITOR_ARGS_FILE="+argsFile,
		),
	}, "config", "edit")
	mustExit(t, r, 0)

	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("$VISUAL editor was not invoked: %v", err)
	}
	if strings.TrimSpace(string(got)) != path {
		t.Fatalf("editor received %q, want %q", strings.TrimSpace(string(got)), path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
}

// SPEC §9.7 — a newly created config file has exactly the bytes "{}\n".
func TestGap_ConfigEditExactContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")

	editor := filepath.Join(t.TempDir(), "noop.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := run(t, runOpts{
		Env: baseEnv("MCP_CONFIG_PATH="+path, "EDITOR="+editor),
	}, "config", "edit")
	mustExit(t, r, 0)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{}\n" {
		t.Fatalf("created content = %q, want %q", string(data), "{}\n")
	}
}

// SPEC §8.3 — when stdin is a terminal and no positional JSON argument is
// given, an empty object is used.
func TestGap_TerminalStdinUsesEmptyObject(t *testing.T) {
	cfg := testServerConfig("fake")
	// runPTY leaves the child's stdin attached to the PTY, so stdin is a TTY.
	r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "echo")
	mustExit(t, r, 0)

	var res map[string]any
	decodeJSON(t, r.Stdout, &res)
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("content missing: %s", r.Stdout)
	}
	first, _ := content[0].(map[string]any)
	text, _ := first["text"].(string)
	if strings.TrimSpace(text) != "{}" {
		t.Fatalf("terminal stdin should yield {}, got %q", text)
	}
}

// SPEC §5.1.2/§5.1.4 — --json is stripped before the version flag is examined.
func TestGap_JSONFlagBeforeVersion(t *testing.T) {
	r := run(t, runOpts{Env: baseEnv(inlineConfig(`{ this is broken `))}, "--json", "--version")
	mustExit(t, r, 0)
	if got := strings.TrimSpace(r.Stdout); !strings.HasPrefix(got, "mcp ") || strings.Contains(got, "\n") {
		t.Fatalf("version output = %q, want single line `mcp <version>`", r.Stdout)
	}
}

// SPEC §8.1/§10 — when a stdio child fails, its captured stderr is attached to
// the error message.
func TestGap_StdioChildStderrIncluded(t *testing.T) {
	script := filepath.Join(t.TempDir(), "badserver.sh")
	if err := os.WriteFile(script,
		[]byte("#!/bin/sh\necho 'badserver: boom' >&2\nsleep 0.3\nexit 7\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := configWithServers(map[string]string{
		"s": stdioServerJSON(script),
	})
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "s", "--list")
	mustExit(t, r, 1)
	mustContain(t, r.Stderr, "badserver: boom", "captured child stderr")
	if strings.TrimSpace(r.Stdout) != "" {
		t.Fatalf("stdout must be empty on connect failure, got: %q", r.Stdout)
	}
}

// SPEC §8.1 — a URL whose path ends in /sse must use the SSE transport. This is
// a black-box check: an SSE-only server is reachable only if SSE was selected
// (the CLI would otherwise POST Streamable-HTTP requests to /sse and fail).
func TestGap_SSEUrlSelectsSSETransport(t *testing.T) {
	mcpServer := server.NewMCPServer("sseserver", "0.0.1", server.WithToolCapabilities(false))
	mcpServer.AddTool(
		mcp.NewTool("search_issues", mcp.WithDescription("Search for issues")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("Found 23 issues."), nil
		},
	)
	sse := server.NewSSEServer(mcpServer)
	ts := httptest.NewServer(sse)
	defer ts.Close()

	cfg := configWithServers(map[string]string{
		"remote": `{"url":"` + ts.URL + `/sse"}`,
	})
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "remote", "--list")
	mustExit(t, r, 0)

	var tools []map[string]any
	decodeJSON(t, r.Stdout, &tools)
	if !hasTool(tools, "search_issues", "Search for issues") {
		t.Fatalf("SSE transport not selected/usable; tools: %s", r.Stdout)
	}
}

// SPEC §13.2 (end-to-end) — a ${VAR} substituted into an HTTP header is
// actually sent on the wire, not merely accepted by the config parser.
func TestGap_EnvVarSubstitutionIntoSentHTTPHeader(t *testing.T) {
	ts, rec := newHTTPTestServer()
	defer ts.Close()

	cfg := configWithServers(map[string]string{
		"remote": `{"url":"` + ts.URL + `/mcp","headers":{"Authorization":"Bearer ${MY_TOKEN}"}}`,
	})

	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg), "MY_TOKEN=abc123")}, "--json", "remote", "--list")
	mustExit(t, r, 0)

	if !rec.sawHeader("Authorization", "Bearer abc123") {
		t.Fatalf("Authorization header was not substituted/sent; recorded: %v", rec.recorded())
	}
}

// SPEC §9.7 — $EDITOR may contain arguments (e.g. "code --wait"); they are
// passed through as argv and the config path is appended last.
func TestGap_ConfigEditEditorWithArguments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")

	editor := filepath.Join(t.TempDir(), "arg-editor.sh")
	script := "#!/bin/sh\necho \"$@\" > \"$EDITOR_ARGS_FILE\"\nexit 0\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args.txt")

	r := run(t, runOpts{
		Env: baseEnv(
			"MCP_CONFIG_PATH="+path,
			"EDITOR="+editor+" --wait",
			"EDITOR_ARGS_FILE="+argsFile,
		),
	}, "config", "edit")
	mustExit(t, r, 0)

	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("editor was not invoked: %v", err)
	}
	fields := strings.Fields(strings.TrimSpace(string(got)))
	if len(fields) != 2 || fields[0] != "--wait" || fields[1] != path {
		t.Fatalf("editor argv = %v, want [--wait %s]", fields, path)
	}
}

// SPEC §7 — MCP_LOG_LEVEL=error suppresses warnings but never errors.
func TestGap_LogLevelControlsWarnings(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"list": stdioServerJSON("npx"),
		"ok":   stdioServerJSON("npx"),
	})

	t.Run("default_info_shows_warning", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
		mustExit(t, r, 0)
		mustContain(t, r.Stderr, "reserved name", "default warning")
	})

	t.Run("invalid_defaults_to_info", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg), "MCP_LOG_LEVEL=bogus")}, "--json", "--list")
		mustExit(t, r, 0)
		mustContain(t, r.Stderr, "reserved name", "invalid log level falls back to info")
	})

	t.Run("error_suppresses_warning", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg), "MCP_LOG_LEVEL=error")}, "--json", "--list")
		mustExit(t, r, 0)
		if strings.TrimSpace(r.Stderr) != "" {
			t.Fatalf("warnings must be suppressed at MCP_LOG_LEVEL=error, got: %q", r.Stderr)
		}
	})

	t.Run("error_still_shows_errors", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(`{ not valid `), "MCP_LOG_LEVEL=error")}, "--list")
		mustExit(t, r, 1)
		if strings.TrimSpace(r.Stderr) == "" {
			t.Fatal("errors must still be shown at MCP_LOG_LEVEL=error")
		}
	})
}
