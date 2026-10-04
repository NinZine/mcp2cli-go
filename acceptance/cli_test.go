package acceptance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// SPEC §5.4 / §5.1.5 — --help and no-args print usage to stderr, exit 0.
// ---------------------------------------------------------------------------

func TestCLI_HelpAndNoArgs(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"no_args", nil},
		{"help_long", []string{"--help"}},
		{"help_short", []string{"-h"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := run(t, runOpts{Env: baseEnv()}, tc.args...)
			mustExit(t, r, 0)
			if strings.TrimSpace(r.Stdout) != "" {
				t.Fatalf("usage must go to stderr, stdout was: %q", r.Stdout)
			}
			mustContain(t, r.Stderr, "Usage:", "usage text")
			mustContain(t, r.Stderr, "mcp --list", "usage text")
			mustContain(t, r.Stderr, "mcp completions", "usage text")
		})
	}
}

// ---------------------------------------------------------------------------
// SPEC §5.2 / §9.6 — `mcp config path`.
// ---------------------------------------------------------------------------

func TestCLI_ConfigPath(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "servers.json")
		r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)}, "--json", "config", "path")
		mustExit(t, r, 0)
		var out map[string]any
		decodeJSON(t, r.Stdout, &out)
		if out["path"] != path {
			t.Fatalf("path = %v, want %q", out["path"], path)
		}
		if out["exists"] != false {
			t.Fatalf("exists = %v, want false (file not created)", out["exists"])
		}
		if out["dir"] != dir {
			t.Fatalf("dir = %v, want %q", out["dir"], dir)
		}
	})

	t.Run("text", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "servers.json")
		r := runPTY(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)}, "config", "path")
		mustExit(t, r, 0)
		mustContain(t, r.Stdout, path, "config path text")
	})

	t.Run("exists_true", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "servers.json")
		if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)}, "--json", "config", "path")
		mustExit(t, r, 0)
		var out map[string]any
		decodeJSON(t, r.Stdout, &out)
		if out["exists"] != true {
			t.Fatalf("exists = %v, want true", out["exists"])
		}
	})
}

// ---------------------------------------------------------------------------
// SPEC §9.8 — `mcp completions <shell>`.
// ---------------------------------------------------------------------------

func TestCLI_Completions(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		t.Run(shell, func(t *testing.T) {
			r := run(t, runOpts{Env: baseEnv()}, "completions", shell)
			mustExit(t, r, 0)
			if strings.TrimSpace(r.Stdout) == "" {
				t.Fatalf("expected a completion script on stdout for %s", shell)
			}
			mustContain(t, r.Stdout, "mcp", "completion script")
		})
	}

	t.Run("unsupported", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv()}, "completions", "powershell")
		mustExit(t, r, 1)
		mustContain(t, r.Stderr, "unsupported shell: powershell (use bash, zsh, or fish)", "stderr")
		if strings.TrimSpace(r.Stdout) != "" {
			t.Fatalf("stdout must be empty on error, got: %q", r.Stdout)
		}
	})
}

// ---------------------------------------------------------------------------
// SPEC §9.4 — `mcp <server> --health` JSON shape.
// ---------------------------------------------------------------------------

func TestCLI_Health(t *testing.T) {
	cfg := testServerConfig("fake")

	t.Run("json", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "--health")
		mustExit(t, r, 0)
		var out map[string]any
		decodeJSON(t, r.Stdout, &out)
		if out["server"] != "fake" {
			t.Fatalf("server = %v, want fake", out["server"])
		}
		if out["status"] != "ok" {
			t.Fatalf("status = %v, want ok", out["status"])
		}
		if _, ok := out["protocolVersion"].(string); !ok {
			t.Fatalf("protocolVersion missing/not string: %v", out["protocolVersion"])
		}
	})

	t.Run("text", func(t *testing.T) {
		r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "fake", "--health")
		mustExit(t, r, 0)
		mustContain(t, r.Stdout, "fake: ok", "health text")
	})
}

// ---------------------------------------------------------------------------
// SPEC §8.3 — tool argument parsing errors are fatal (exit 1).
// ---------------------------------------------------------------------------

func TestCLI_ToolArgumentErrors(t *testing.T) {
	cfg := testServerConfig("fake")

	t.Run("invalid_json_arg", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "fake", "echo", "{not json")
		mustExit(t, r, 1)
		if strings.TrimSpace(r.Stdout) != "" {
			t.Fatalf("stdout must be empty on parse error, got: %q", r.Stdout)
		}
		if strings.TrimSpace(r.Stderr) == "" {
			t.Fatalf("expected parse error on stderr")
		}
	})

	t.Run("non_object_arg", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "fake", "echo", `[1,2,3]`)
		mustExit(t, r, 1)
	})
}

// ---------------------------------------------------------------------------
// SPEC §10 — malformed config is an error (exit 1).
// ---------------------------------------------------------------------------

func TestCLI_MalformedConfig(t *testing.T) {
	r := run(t, runOpts{Env: baseEnv(inlineConfig(`{ not valid `))}, "--list")
	mustExit(t, r, 1)
	if strings.TrimSpace(r.Stdout) != "" {
		t.Fatalf("stdout must be empty on config error, got: %q", r.Stdout)
	}
	if strings.TrimSpace(r.Stderr) == "" {
		t.Fatalf("expected config error on stderr")
	}
}

// ---------------------------------------------------------------------------
// SPEC §6.1 — missing config file is an empty server set, not an error.
// ---------------------------------------------------------------------------

func TestCLI_MissingConfigIsEmpty(t *testing.T) {
	dir := t.TempDir()
	r := run(t, runOpts{
		Env: baseEnv("MCP_CONFIG_PATH=" + filepath.Join(dir, "absent.json")),
	}, "--json", "--list")
	mustExit(t, r, 0)
	var servers []any
	decodeJSON(t, r.Stdout, &servers)
	if len(servers) != 0 {
		t.Fatalf("want empty server list, got: %s", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §6.4 — reserved server names are warned about and skipped.
// ---------------------------------------------------------------------------

func TestCLI_ReservedServerNamesSkipped(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"list": stdioServerJSON("npx"),
		"ok":   stdioServerJSON("npx"),
	})
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
	mustExit(t, r, 0)
	var servers []map[string]any
	decodeJSON(t, r.Stdout, &servers)
	for _, s := range servers {
		if s["name"] == "list" {
			t.Fatalf("reserved server 'list' must be skipped: %s", r.Stdout)
		}
	}
	if len(servers) != 1 || servers[0]["name"] != "ok" {
		t.Fatalf("expected only 'ok' server, got: %s", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §5.2 — `mcp <server>` is equivalent to `mcp <server> --list`.
// ---------------------------------------------------------------------------

func TestCLI_ServerAloneEqualsList(t *testing.T) {
	cfg := testServerConfig("fake")

	alone := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake")
	explicit := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "--list")
	mustExit(t, alone, 0)
	mustExit(t, explicit, 0)
	if alone.Stdout != explicit.Stdout {
		t.Fatalf("`mcp <server>` must equal `mcp <server> --list`:\n%s\n---\n%s",
			alone.Stdout, explicit.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §9.5 — text-mode tool result rendering (TTY).
// ---------------------------------------------------------------------------

func TestCLI_TextToolResult(t *testing.T) {
	cfg := testServerConfig("fake")

	t.Run("success_text", func(t *testing.T) {
		r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "fake", "echo", `{"x":1}`)
		mustExit(t, r, 0)
		mustContain(t, r.Stdout, `{"x":1}`, "text tool result")
	})

	t.Run("error_text_goes_to_stderr", func(t *testing.T) {
		r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "fake", "fail")
		// isError is a successful protocol exchange -> exit 0.
		mustExit(t, r, 0)
		mustContain(t, r.Stderr, "error:", "error prefix")
		mustContain(t, r.Stderr, "boom", "error text")
	})
}

// ---------------------------------------------------------------------------
// SPEC §4 — version output is exactly `mcp <version>`.
// ---------------------------------------------------------------------------

func TestCLI_VersionExactFormat(t *testing.T) {
	r := run(t, runOpts{Env: baseEnv()}, "--version")
	mustExit(t, r, 0)
	got := strings.TrimRight(r.Stdout, "\n")
	fields := strings.Fields(got)
	if len(fields) != 2 || fields[0] != "mcp" {
		t.Fatalf("version output = %q, want exactly `mcp <version>`", got)
	}
	if strings.TrimSpace(r.Stderr) != "" {
		t.Fatalf("version must not write to stderr, got: %q", r.Stderr)
	}
}

// ---------------------------------------------------------------------------
// SPEC §9.1 — text table has a Status column when a stdio command is
// unavailable.
// ---------------------------------------------------------------------------

func TestCLI_TextListStatusColumn(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"missing": `{"command":"definitely-not-a-real-command-xyz"}`,
	})
	r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--list")
	mustExit(t, r, 0)
	mustContain(t, r.Stdout, "Status", "text list header")
	mustContain(t, r.Stdout, "command not found in PATH: definitely-not-a-real-command-xyz", "text list reason")
}

// ---------------------------------------------------------------------------
// SPEC §6.3 — an entry with neither command nor url is an error.
// ---------------------------------------------------------------------------

func TestCLI_ServerWithoutCommandOrURL(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"broken": `{"foo":"bar"}`,
	})
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--list")
	mustExit(t, r, 1)
	if strings.TrimSpace(r.Stdout) != "" {
		t.Fatalf("stdout must be empty on config error, got: %q", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §6.2 — unknown keys are ignored (forward compatibility).
// ---------------------------------------------------------------------------

func TestCLI_UnknownKeysIgnored(t *testing.T) {
	cfg := `{"mcpServers":{"s":{"command":"npx","futureField":123}},"unknownTop":true}`
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
	mustExit(t, r, 0)
	var servers []map[string]any
	decodeJSON(t, r.Stdout, &servers)
	if len(servers) != 1 || servers[0]["name"] != "s" {
		t.Fatalf("unknown keys should be ignored, got: %s", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §6.1 — MCP_CONFIG_DIR resolves to <dir>/servers.json.
// ---------------------------------------------------------------------------

func TestCLI_ConfigDirResolution(t *testing.T) {
	dir := t.TempDir()
	r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_DIR=" + dir)}, "--json", "config", "path")
	mustExit(t, r, 0)
	var out map[string]any
	decodeJSON(t, r.Stdout, &out)
	want := filepath.Join(dir, "servers.json")
	if out["path"] != want {
		t.Fatalf("path = %v, want %q", out["path"], want)
	}
}

// ---------------------------------------------------------------------------
// SPEC §6.1 — MCP_SERVERS_CONFIG overrides file locations.
// ---------------------------------------------------------------------------

func TestCLI_InlineConfigOverridesFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "servers.json")
	if err := os.WriteFile(filePath, []byte(`{"mcpServers":{"fromfile":{"command":"npx"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	inline := configWithServers(map[string]string{
		"frominline": stdioServerJSON("npx"),
	})
	r := run(t, runOpts{
		Env: baseEnv("MCP_CONFIG_PATH="+filePath, inlineConfig(inline)),
	}, "--json", "--list")
	mustExit(t, r, 0)
	var servers []map[string]any
	decodeJSON(t, r.Stdout, &servers)
	if len(servers) != 1 || servers[0]["name"] != "frominline" {
		t.Fatalf("inline config must override file, got: %s", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §9.2 — `--list` JSON shape for tools.
// ---------------------------------------------------------------------------

func TestCLI_ToolListJSONShape(t *testing.T) {
	cfg := testServerConfig("fake")
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "--list")
	mustExit(t, r, 0)
	var tools []map[string]any
	decodeJSON(t, r.Stdout, &tools)
	if len(tools) == 0 {
		t.Fatalf("expected tools, got: %s", r.Stdout)
	}
	for _, tl := range tools {
		if _, ok := tl["name"].(string); !ok {
			t.Fatalf("tool missing name: %v", tl)
		}
		if _, ok := tl["description"].(string); !ok {
			t.Fatalf("tool missing description: %v", tl)
		}
	}
}

// ---------------------------------------------------------------------------
// SPEC §9.3 — `--info` text rendering includes Parameters.
// ---------------------------------------------------------------------------

func TestCLI_InfoTextRendering(t *testing.T) {
	cfg := testServerConfig("fake")
	r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "fake", "--info")
	mustExit(t, r, 0)
	mustContain(t, r.Stdout, "search_issues", "info text")
	mustContain(t, r.Stdout, "Parameters", "info text")
	mustContain(t, r.Stdout, "query", "info text")
}

// ---------------------------------------------------------------------------
// SPEC §9.5 — JSON tool result shape.
// ---------------------------------------------------------------------------

func TestCLI_ToolResultJSONShape(t *testing.T) {
	cfg := testServerConfig("fake")
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "echo", `{"x":1}`)
	mustExit(t, r, 0)
	var res map[string]any
	decodeJSON(t, r.Stdout, &res)
	if _, ok := res["content"].([]any); !ok {
		t.Fatalf("content missing/not array: %s", r.Stdout)
	}
	if _, ok := res["isError"]; !ok {
		t.Fatalf("isError missing: %s", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §8.4 — direct call to an unavailable command exits 1 with the reason.
// ---------------------------------------------------------------------------

func TestCLI_DirectCallPreflightFailure(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"missing": `{"command":"definitely-not-a-real-command-xyz"}`,
	})
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "missing", "echo", `{}`)
	mustExit(t, r, 1)
	mustContain(t, r.Stderr, "command not found in PATH: definitely-not-a-real-command-xyz", "stderr")
	if strings.TrimSpace(r.Stdout) != "" {
		t.Fatalf("stdout must be empty on preflight failure, got: %q", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §6.3 — env/headers present but not an object is an error.
// ---------------------------------------------------------------------------

func TestCLI_EnvNotObjectIsError(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"bad": `{"command":"npx","env":"not-an-object"}`,
	})
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--list")
	mustExit(t, r, 1)
}

// ---------------------------------------------------------------------------
// SPEC §6.2 — mcpServers not an object is an error.
// ---------------------------------------------------------------------------

func TestCLI_McpServersNotObjectIsError(t *testing.T) {
	r := run(t, runOpts{Env: baseEnv(inlineConfig(`{"mcpServers":[]}`))}, "--list")
	mustExit(t, r, 1)
}

// ---------------------------------------------------------------------------
// SPEC §6.3 — http server type inference and JSON shape.
// ---------------------------------------------------------------------------

func TestCLI_HTTPServerListShape(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"remote": `{"url":"https://example.invalid/mcp"}`,
	})
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
	mustExit(t, r, 0)
	var servers []map[string]any
	decodeJSON(t, r.Stdout, &servers)
	if len(servers) != 1 {
		t.Fatalf("want 1 server: %s", r.Stdout)
	}
	s := servers[0]
	if s["type"] != "http" {
		t.Fatalf("type = %v, want http", s["type"])
	}
	if s["url"] != "https://example.invalid/mcp" {
		t.Fatalf("url = %v", s["url"])
	}
	if _, ok := s["command"]; ok {
		t.Fatalf("http server must not carry command: %s", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §9.1 — text list summary line.
// ---------------------------------------------------------------------------

func TestCLI_TextListSummary(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"a": stdioServerJSON("npx"),
		"b": `{"url":"https://example.invalid/mcp"}`,
	})
	r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--list")
	mustExit(t, r, 0)
	mustContain(t, r.Stdout, "2 server(s) configured", "summary line")
}

// ---------------------------------------------------------------------------
// SPEC §9.2 — text tool list summary line.
// ---------------------------------------------------------------------------

func TestCLI_TextToolListSummary(t *testing.T) {
	cfg := testServerConfig("fake")
	r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "fake", "--list")
	mustExit(t, r, 0)
	mustContain(t, r.Stdout, "tool(s) available", "tool summary line")
}

// ---------------------------------------------------------------------------
// SPEC §5.1.2 — all --json occurrences are removed, even repeated.
// ---------------------------------------------------------------------------

func TestCLI_RepeatedJSONFlag(t *testing.T) {
	cfg := testServerConfig("fake")
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--json", "fake", "--list")
	mustExit(t, r, 0)
	var tools []any
	decodeJSON(t, r.Stdout, &tools)
}

// ---------------------------------------------------------------------------
// SPEC §9.7 — config edit creates the file and runs $EDITOR.
// ---------------------------------------------------------------------------

func TestCLI_ConfigEditCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "servers.json")

	// Use a fake editor that records it was invoked and exits 0.
	editor := filepath.Join(t.TempDir(), "fake-editor.sh")
	script := "#!/bin/sh\necho \"$1\" > \"$EDITOR_ARGS_FILE\"\nexit 0\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	argsFile := filepath.Join(t.TempDir(), "args.txt")

	r := run(t, runOpts{
		Env: baseEnv(
			"MCP_CONFIG_PATH="+path,
			"EDITOR="+editor,
			"EDITOR_ARGS_FILE="+argsFile,
		),
	}, "config", "edit")
	mustExit(t, r, 0)

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config edit must create the file: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(data)) != "{}" {
		t.Fatalf("created file = %q, want {}", string(data))
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("editor was not invoked: %v", err)
	}
	if strings.TrimSpace(string(got)) != path {
		t.Fatalf("editor received %q, want %q", strings.TrimSpace(string(got)), path)
	}
}

// ---------------------------------------------------------------------------
// SPEC §9.7 — non-zero editor exit is an error.
// ---------------------------------------------------------------------------

func TestCLI_ConfigEditEditorFailure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	editor := filepath.Join(t.TempDir(), "fail-editor.sh")
	if err := os.WriteFile(editor, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := run(t, runOpts{
		Env: baseEnv("MCP_CONFIG_PATH="+path, "EDITOR="+editor),
	}, "config", "edit")
	mustExit(t, r, 1)
}

// ---------------------------------------------------------------------------
// SPEC §5.2 — unknown first argument is treated as a server name.
// ---------------------------------------------------------------------------

func TestCLI_UnknownCommandTreatedAsServer(t *testing.T) {
	r := run(t, runOpts{Env: baseEnv(inlineConfig(`{"mcpServers":{}}`))}, "frobnicate")
	mustExit(t, r, 1)
	mustContain(t, r.Stderr, `server "frobnicate" not found in config`, "stderr")
}

// ---------------------------------------------------------------------------
// SPEC §9.5 — JSON output is pretty-printed with two-space indentation.
// ---------------------------------------------------------------------------

func TestCLI_JSONPrettyPrinted(t *testing.T) {
	cfg := testServerConfig("fake")
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "--list")
	mustExit(t, r, 0)
	if !strings.Contains(r.Stdout, "\n  ") {
		t.Fatalf("JSON should be pretty-printed with two-space indent:\n%s", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §6.1 — MCP_CONFIG_PATH takes precedence over MCP_CONFIG_DIR.
// ---------------------------------------------------------------------------

func TestCLI_ConfigPathPrecedence(t *testing.T) {
	dir := t.TempDir()
	explicit := filepath.Join(dir, "explicit.json")
	r := run(t, runOpts{
		Env: baseEnv("MCP_CONFIG_PATH="+explicit, "MCP_CONFIG_DIR="+dir),
	}, "--json", "config", "path")
	mustExit(t, r, 0)
	var out map[string]any
	decodeJSON(t, r.Stdout, &out)
	if out["path"] != explicit {
		t.Fatalf("path = %v, want %q", out["path"], explicit)
	}
}
