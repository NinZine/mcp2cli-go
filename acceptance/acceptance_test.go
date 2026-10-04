package acceptance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// SPEC §13.1 — inline config with one stdio server; --list prints
// name/type/command/args (JSON parsing + text table substrings).
// ---------------------------------------------------------------------------

func TestAcceptance01_InlineConfigListStdioServer(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"myserver": stdioServerJSON("npx", "-y", "some-mcp-server@latest"),
	})

	t.Run("json", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
		mustExit(t, r, 0)

		var servers []map[string]any
		decodeJSON(t, r.Stdout, &servers)
		if len(servers) != 1 {
			t.Fatalf("want 1 server, got %d: %s", len(servers), r.Stdout)
		}
		s := servers[0]
		if s["name"] != "myserver" {
			t.Errorf("name = %v, want myserver", s["name"])
		}
		if s["type"] != "stdio" {
			t.Errorf("type = %v, want stdio", s["type"])
		}
		if s["command"] != "npx" {
			t.Errorf("command = %v, want npx", s["command"])
		}
		args, ok := s["args"].([]any)
		if !ok || len(args) != 2 || args[0] != "-y" || args[1] != "some-mcp-server@latest" {
			t.Errorf("args = %v, want [-y some-mcp-server@latest]", s["args"])
		}
	})

	t.Run("text", func(t *testing.T) {
		r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--list")
		mustExit(t, r, 0)
		mustContain(t, r.Stdout, "myserver", "text list")
		mustContain(t, r.Stdout, "stdio", "text list")
		mustContain(t, r.Stdout, "npx", "text list")
		mustContain(t, r.Stdout, "some-mcp-server@latest", "text list")
	})
}

// ---------------------------------------------------------------------------
// SPEC §13.2 — ${VAR} substitution in env/headers; missing var -> "".
// ---------------------------------------------------------------------------

func TestAcceptance02_EnvVarSubstitution(t *testing.T) {
	t.Run("present", func(t *testing.T) {
		cfg := configWithServers(map[string]string{
			"sub": `{"command":"npx","env":{"TOKEN":"${MY_TOKEN}"}}`,
		})
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg), "MY_TOKEN=secret-value")}, "--json", "--list")
		mustExit(t, r, 0)
		// The env value is not part of the --list output, so assert the
		// substitution indirectly: the config must parse and the server list.
		var servers []map[string]any
		decodeJSON(t, r.Stdout, &servers)
		if len(servers) != 1 || servers[0]["name"] != "sub" {
			t.Fatalf("expected server 'sub', got: %s", r.Stdout)
		}
	})

	t.Run("missing_becomes_empty", func(t *testing.T) {
		// A missing var must not be an error; it becomes "". We assert the
		// config still loads and lists the server.
		cfg := configWithServers(map[string]string{
			"sub": `{"command":"npx","env":{"TOKEN":"${DEFINITELY_UNSET_VAR_XYZ}"}}`,
		})
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
		mustExit(t, r, 0)
		var servers []map[string]any
		decodeJSON(t, r.Stdout, &servers)
		if len(servers) != 1 {
			t.Fatalf("expected 1 server, got: %s", r.Stdout)
		}
	})

	t.Run("headers_substitution", func(t *testing.T) {
		cfg := configWithServers(map[string]string{
			"sub": `{"url":"https://example.invalid/mcp","headers":{"Authorization":"Bearer ${MY_TOKEN}"}}`,
		})
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg), "MY_TOKEN=abc123")}, "--json", "--list")
		mustExit(t, r, 0)
		var servers []map[string]any
		decodeJSON(t, r.Stdout, &servers)
		if len(servers) != 1 || servers[0]["type"] != "http" {
			t.Fatalf("expected http server, got: %s", r.Stdout)
		}
	})
}

// ---------------------------------------------------------------------------
// SPEC §13.3 — comments stripped; https:// inside strings preserved.
// ---------------------------------------------------------------------------

func TestAcceptance03_CommentStripping(t *testing.T) {
	cfg := `{
  // line comment before mcpServers
  "mcpServers": {
    /* block comment */
    "commented": {
      "url": "https://example.invalid/mcp" // trailing line comment
    }
  }
}`
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
	mustExit(t, r, 0)

	var servers []map[string]any
	decodeJSON(t, r.Stdout, &servers)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got %d: %s", len(servers), r.Stdout)
	}
	if got := servers[0]["url"]; got != "https://example.invalid/mcp" {
		t.Fatalf("url = %v, want https://example.invalid/mcp (https:// must survive comment stripping)", got)
	}
}

// ---------------------------------------------------------------------------
// SPEC §13.4 — global --json forces JSON regardless of position/TTY.
// ---------------------------------------------------------------------------

func TestAcceptance04_GlobalJSONFlagAnyPosition(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"myserver": stdioServerJSON("npx", "-y", "x"),
	})

	for _, args := range [][]string{
		{"--json", "--list"},
		{"--list", "--json"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			// Run under a PTY so that, absent --json, text would be chosen.
			r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, args...)
			mustExit(t, r, 0)
			var servers []map[string]any
			decodeJSON(t, r.Stdout, &servers)
			if len(servers) != 1 {
				t.Fatalf("want 1 server, got: %s", r.Stdout)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// SPEC §13.5 — --version works with broken/missing config.
// ---------------------------------------------------------------------------

func TestAcceptance05_VersionWithBrokenConfig(t *testing.T) {
	broken := inlineConfig(`{ this is not valid json `)

	for _, flag := range []string{"--version", "-V", "-v"} {
		t.Run(flag, func(t *testing.T) {
			r := run(t, runOpts{Env: baseEnv(broken)}, flag)
			mustExit(t, r, 0)
			got := strings.TrimSpace(r.Stdout)
			if !strings.HasPrefix(got, "mcp ") {
				t.Fatalf("version output = %q, want prefix %q", got, "mcp ")
			}
			if strings.Contains(got, "\n") {
				t.Fatalf("version output must be a single line, got %q", got)
			}
		})
	}

	t.Run("missing_config", func(t *testing.T) {
		dir := t.TempDir()
		r := run(t, runOpts{
			Env: baseEnv("MCP_CONFIG_PATH=" + filepath.Join(dir, "does-not-exist.json")),
		}, "--version")
		mustExit(t, r, 0)
		mustContain(t, r.Stdout, "mcp ", "version output")
	})
}

// ---------------------------------------------------------------------------
// SPEC §13.6 — fake stdio server: --list and --info render tools;
// `mcp <server> echo '{"x":1}'` renders the result.
// ---------------------------------------------------------------------------

func TestAcceptance06_StdioServerTools(t *testing.T) {
	cfg := testServerConfig("fake")

	t.Run("list_json", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "--list")
		mustExit(t, r, 0)
		var tools []map[string]any
		decodeJSON(t, r.Stdout, &tools)
		if !hasTool(tools, "search_issues", "Search for issues") {
			t.Fatalf("search_issues not found in tools: %s", r.Stdout)
		}
	})

	t.Run("list_text", func(t *testing.T) {
		r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "fake", "--list")
		mustExit(t, r, 0)
		mustContain(t, r.Stdout, "search_issues", "text tool list")
		mustContain(t, r.Stdout, "Search for issues", "text tool list")
	})

	t.Run("info_json", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "--info")
		mustExit(t, r, 0)
		var tools []map[string]any
		decodeJSON(t, r.Stdout, &tools)
		var found map[string]any
		for _, tl := range tools {
			if tl["name"] == "search_issues" {
				found = tl
			}
		}
		if found == nil {
			t.Fatalf("search_issues not found: %s", r.Stdout)
		}
		schema, ok := found["inputSchema"].(map[string]any)
		if !ok {
			t.Fatalf("inputSchema missing/not object: %v", found["inputSchema"])
		}
		props, ok := schema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("inputSchema.properties missing: %v", schema)
		}
		if _, ok := props["query"]; !ok {
			t.Fatalf("query property missing: %v", props)
		}
		req, _ := schema["required"].([]any)
		if !containsString(req, "query") {
			t.Fatalf("query not required: %v", schema["required"])
		}
	})

	t.Run("call_echo", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "echo", `{"x":1}`)
		mustExit(t, r, 0)
		var res map[string]any
		decodeJSON(t, r.Stdout, &res)
		content, ok := res["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("content missing: %s", r.Stdout)
		}
		first, _ := content[0].(map[string]any)
		if first["type"] != "text" {
			t.Fatalf("content[0].type = %v, want text", first["type"])
		}
		text, _ := first["text"].(string)
		// The echo tool marshals its arguments; {"x":1} must round-trip.
		var echoed map[string]any
		if err := json.Unmarshal([]byte(text), &echoed); err != nil {
			t.Fatalf("echo text is not JSON: %q", text)
		}
		if echoed["x"] != float64(1) {
			t.Fatalf("echoed x = %v, want 1", echoed["x"])
		}
	})

	t.Run("call_error_content", func(t *testing.T) {
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "fail")
		// A tool that reports isError is still a successful protocol exchange.
		mustExit(t, r, 0)
		var res map[string]any
		decodeJSON(t, r.Stdout, &res)
		if res["isError"] != true {
			t.Fatalf("isError = %v, want true: %s", res["isError"], r.Stdout)
		}
	})
}

// ---------------------------------------------------------------------------
// SPEC §13.7 — omitting the JSON arg reads it from piped stdin.
// ---------------------------------------------------------------------------

func TestAcceptance07_StdinArguments(t *testing.T) {
	cfg := testServerConfig("fake")

	t.Run("piped_json", func(t *testing.T) {
		r := run(t, runOpts{
			Env:   baseEnv(inlineConfig(cfg)),
			Stdin: strings.NewReader(`{"x":1}`),
		}, "--json", "fake", "echo")
		mustExit(t, r, 0)
		var res map[string]any
		decodeJSON(t, r.Stdout, &res)
		content, _ := res["content"].([]any)
		if len(content) == 0 {
			t.Fatalf("content missing: %s", r.Stdout)
		}
		first, _ := content[0].(map[string]any)
		text, _ := first["text"].(string)
		var echoed map[string]any
		if err := json.Unmarshal([]byte(text), &echoed); err != nil {
			t.Fatalf("echo text is not JSON: %q", text)
		}
		if echoed["x"] != float64(1) {
			t.Fatalf("stdin args not used; echoed x = %v", echoed["x"])
		}
	})

	t.Run("empty_stdin_uses_empty_object", func(t *testing.T) {
		r := run(t, runOpts{
			Env:   baseEnv(inlineConfig(cfg)),
			Stdin: strings.NewReader(""),
		}, "--json", "fake", "echo")
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
			t.Fatalf("empty stdin should yield {}, got %q", text)
		}
	})
}

// ---------------------------------------------------------------------------
// SPEC §13.8 — --list for a stdio server whose command is missing emits
// available:false + the exact reason strings from §8.4.
// ---------------------------------------------------------------------------

func TestAcceptance08_PreflightUnavailableReasons(t *testing.T) {
	t.Run("bare_name_not_found", func(t *testing.T) {
		cfg := configWithServers(map[string]string{
			"missing": `{"command":"definitely-not-a-real-command-xyz"}`,
		})
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
		mustExit(t, r, 0)
		var servers []map[string]any
		decodeJSON(t, r.Stdout, &servers)
		if len(servers) != 1 {
			t.Fatalf("want 1 server: %s", r.Stdout)
		}
		s := servers[0]
		if s["available"] != false {
			t.Fatalf("available = %v, want false: %s", s["available"], r.Stdout)
		}
		want := "command not found in PATH: definitely-not-a-real-command-xyz"
		if s["unavailable_reason"] != want {
			t.Fatalf("unavailable_reason = %v, want %q", s["unavailable_reason"], want)
		}
	})

	t.Run("explicit_path_missing", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "nope-binary")
		cfg := configWithServers(map[string]string{
			"missing": stdioServerJSON(missing),
		})
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
		mustExit(t, r, 0)
		var servers []map[string]any
		decodeJSON(t, r.Stdout, &servers)
		s := servers[0]
		if s["available"] != false {
			t.Fatalf("available = %v, want false: %s", s["available"], r.Stdout)
		}
		want := "command does not exist: " + missing
		if s["unavailable_reason"] != want {
			t.Fatalf("unavailable_reason = %v, want %q", s["unavailable_reason"], want)
		}
	})

	t.Run("exists_not_executable", func(t *testing.T) {
		dir := t.TempDir()
		notExec := filepath.Join(dir, "not-executable")
		if err := os.WriteFile(notExec, []byte("#!/bin/sh\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		cfg := configWithServers(map[string]string{
			"missing": stdioServerJSON(notExec),
		})
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
		mustExit(t, r, 0)
		var servers []map[string]any
		decodeJSON(t, r.Stdout, &servers)
		s := servers[0]
		if s["available"] != false {
			t.Fatalf("available = %v, want false: %s", s["available"], r.Stdout)
		}
		want := "command is not executable: " + notExec
		if s["unavailable_reason"] != want {
			t.Fatalf("unavailable_reason = %v, want %q", s["unavailable_reason"], want)
		}
	})

	t.Run("http_never_unavailable", func(t *testing.T) {
		cfg := configWithServers(map[string]string{
			"remote": `{"url":"https://example.invalid/mcp"}`,
		})
		r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "--list")
		mustExit(t, r, 0)
		var servers []map[string]any
		decodeJSON(t, r.Stdout, &servers)
		if _, ok := servers[0]["available"]; ok {
			t.Fatalf("http server must not carry available field: %s", r.Stdout)
		}
	})
}

// ---------------------------------------------------------------------------
// SPEC §13.9 — nonexistent server prints exact message to stderr, exit 1.
// ---------------------------------------------------------------------------

func TestAcceptance09_NonexistentServer(t *testing.T) {
	cfg := configWithServers(map[string]string{
		"real": stdioServerJSON("npx"),
	})
	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "ghost", "--list")
	mustExit(t, r, 1)
	mustContain(t, r.Stderr, `server "ghost" not found in config`, "stderr")
	if strings.TrimSpace(r.Stdout) != "" {
		t.Fatalf("stdout must be empty on error, got: %q", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// SPEC §13.10 — HTTP server headers from config are sent.
// ---------------------------------------------------------------------------

func TestAcceptance10_HTTPHeadersSent(t *testing.T) {
	ts, rec := newHTTPTestServer()
	defer ts.Close()

	cfg := configWithServers(map[string]string{
		"remote": `{"url":"` + ts.URL + `/mcp","headers":{"X-Test-Header":"hello-world","Authorization":"Bearer tok"}}`,
	})

	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "remote", "--list")
	mustExit(t, r, 0)

	if !rec.sawHeader("X-Test-Header", "hello-world") {
		t.Fatalf("X-Test-Header not sent; recorded: %v", rec.recorded())
	}
	if !rec.sawHeader("Authorization", "Bearer tok") {
		t.Fatalf("Authorization not sent; recorded: %v", rec.recorded())
	}
}

// ---------------------------------------------------------------------------
// SPEC §13.11 — text to stdout, errors to stderr; JSON valid and stable.
// ---------------------------------------------------------------------------

func TestAcceptance11_StreamSeparationAndStableJSON(t *testing.T) {
	cfg := testServerConfig("fake")

	t.Run("text_stdout_errors_stderr", func(t *testing.T) {
		// Success: data on stdout, nothing on stderr.
		ok := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "fake", "--list")
		mustExit(t, ok, 0)
		if strings.TrimSpace(ok.Stdout) == "" {
			t.Fatalf("expected tool data on stdout")
		}
		if strings.TrimSpace(ok.Stderr) != "" {
			t.Fatalf("expected empty stderr on success, got: %q", ok.Stderr)
		}

		// Error: message on stderr, nothing on stdout.
		bad := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "ghost", "--list")
		mustExit(t, bad, 1)
		if strings.TrimSpace(bad.Stdout) != "" {
			t.Fatalf("error text must not go to stdout, got: %q", bad.Stdout)
		}
		if strings.TrimSpace(bad.Stderr) == "" {
			t.Fatalf("expected error text on stderr")
		}
	})

	t.Run("json_valid_and_stable", func(t *testing.T) {
		first := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "--list")
		second := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "fake", "--list")
		mustExit(t, first, 0)
		mustExit(t, second, 0)

		var a, b any
		decodeJSON(t, first.Stdout, &a)
		decodeJSON(t, second.Stdout, &b)
		if first.Stdout != second.Stdout {
			t.Fatalf("JSON output not stable across runs:\n%s\n---\n%s", first.Stdout, second.Stdout)
		}
	})
}

// ---------------------------------------------------------------------------
// helpers local to this file
// ---------------------------------------------------------------------------

func hasTool(tools []map[string]any, name, desc string) bool {
	for _, tl := range tools {
		if tl["name"] == name && tl["description"] == desc {
			return true
		}
	}
	return false
}

func containsString(xs []any, want string) bool {
	for _, x := range xs {
		if s, ok := x.(string); ok && s == want {
			return true
		}
	}
	return false
}
