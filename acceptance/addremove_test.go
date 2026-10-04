package acceptance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mcp/internal/config"
)

// ---------------------------------------------------------------------------
// Improvement 1 — `mcp add` / `mcp remove` (P1).
//
// Black-box coverage: the commands edit the config *file* resolved by
// config.ResolvePath() (never the inline MCP_SERVERS_CONFIG), create parent
// directories, report success on stderr with exit 0, and validate input.
// ---------------------------------------------------------------------------

// mustBeDispatched guards the negative tests below against passing vacuously
// during the RED phase: before `add`/`remove` exist, their names are treated
// as unknown *servers*, which also exits 1. The command must be recognized.
func mustBeDispatched(t *testing.T, r result, command string) {
	t.Helper()
	mustNotContain(t, r.Stderr, "not found in config", command+" must be a recognized command")
}

// configFilePath returns a fresh, not-yet-existing config path.
func configFilePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "nested", "servers.json")
}

// readFileString reads path or fails the test.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// readServers decodes mcpServers from a config file.
func readServers(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFileString(t, path)), &top); err != nil {
		t.Fatalf("config is not valid JSON: %v\n%s", err, readFileString(t, path))
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(top["mcpServers"], &servers); err != nil {
		t.Fatalf("mcpServers is not an object: %v", err)
	}
	return servers
}

// ---------------------------------------------------------------------------
// Happy path
// ---------------------------------------------------------------------------

func TestCLI_AddHTTPServerCreatesFile(t *testing.T) {
	path := configFilePath(t)
	const rawURL = "http://localhost:38429/mcp"

	r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)},
		"add", "--url", rawURL, "linear")
	mustExit(t, r, 0)

	// Success status goes to stderr; stdout stays clean.
	if strings.TrimSpace(r.Stdout) != "" {
		t.Fatalf("add must not write data to stdout, got: %q", r.Stdout)
	}
	mustContain(t, r.Stderr, `Server "linear" added to `, "add status")
	mustContain(t, r.Stderr, path, "add status path")

	// Parent directories are created.
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("parent dir not created: %v", err)
	}

	// Exact on-disk JSON: two-space indent, trailing newline, 0644.
	want := `{
  "mcpServers": {
    "linear": {
      "url": "http://localhost:38429/mcp"
    }
  }
}
`
	if got := readFileString(t, path); got != want {
		t.Fatalf("file content:\n%q\nwant:\n%q", got, want)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("file mode = %o, want 644", perm)
	}

	// A subsequent --list sees the server.
	lr := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)}, "--json", "--list")
	mustExit(t, lr, 0)
	var servers []map[string]any
	decodeJSON(t, lr.Stdout, &servers)
	if len(servers) != 1 {
		t.Fatalf("want 1 server, got: %s", lr.Stdout)
	}
	s := servers[0]
	if s["name"] != "linear" || s["type"] != "http" || s["url"] != rawURL {
		t.Fatalf("added server not listed correctly: %s", lr.Stdout)
	}
}

// ---------------------------------------------------------------------------
// `add` validation
// ---------------------------------------------------------------------------

func TestCLI_AddWithoutURLPrintsUsage(t *testing.T) {
	r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + configFilePath(t))}, "add", "linear")
	mustExit(t, r, 1)
	mustBeDispatched(t, r, "add")
	if strings.TrimSpace(r.Stdout) != "" {
		t.Fatalf("stdout must be empty, got: %q", r.Stdout)
	}
	mustContain(t, r.Stderr, "Usage:", "add usage")
	mustContain(t, r.Stderr, "mcp add --url", "add usage")
}

func TestCLI_AddRejectsBadURL(t *testing.T) {
	cases := map[string]string{
		"ftp_scheme": "ftp://example.invalid/mcp",
		"relative":   "not-a-url",
		"no_scheme":  "//example.invalid/mcp",
		"file":       "file:///tmp/mcp.sock",
	}
	for name, rawURL := range cases {
		t.Run(name, func(t *testing.T) {
			path := configFilePath(t)
			r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)},
				"add", "--url", rawURL, "s")
			mustExit(t, r, 1)
			mustBeDispatched(t, r, "add")
			if strings.TrimSpace(r.Stdout) != "" {
				t.Fatalf("stdout must be empty, got: %q", r.Stdout)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("file must not be created for bad URL %q", rawURL)
			}
		})
	}
}

func TestCLI_AddRejectsReservedName(t *testing.T) {
	for _, name := range []string{"config", "add", "remove", "list"} {
		t.Run(name, func(t *testing.T) {
			path := configFilePath(t)
			r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)},
				"add", "--url", "http://host/mcp", name)
			mustExit(t, r, 1)
			mustBeDispatched(t, r, "add")
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("reserved name %q must not create a config", name)
			}
		})
	}
}

func TestCLI_AddRejectsDuplicateName(t *testing.T) {
	path := configFilePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"dup":{"url":"http://a/mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)},
		"add", "--url", "http://b/mcp", "dup")
	mustExit(t, r, 1)
	mustBeDispatched(t, r, "add")

	servers := readServers(t, path)
	if servers["dup"]["url"] != "http://a/mcp" {
		t.Fatalf("duplicate add must not overwrite: %#v", servers["dup"])
	}
}

// ---------------------------------------------------------------------------
// `remove`
// ---------------------------------------------------------------------------

func TestCLI_RemoveServer(t *testing.T) {
	path := configFilePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := `{"mcpServers":{"one":{"url":"http://a/mcp"},"two":{"url":"http://b/mcp"}}}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)}, "remove", "one")
	mustExit(t, r, 0)
	if strings.TrimSpace(r.Stdout) != "" {
		t.Fatalf("remove must not write data to stdout, got: %q", r.Stdout)
	}
	mustContain(t, r.Stderr, `Server "one" removed from `, "remove status")
	mustContain(t, r.Stderr, path, "remove status path")

	servers := readServers(t, path)
	if _, ok := servers["one"]; ok {
		t.Fatalf("server not removed: %#v", servers)
	}
	if servers["two"]["url"] != "http://b/mcp" {
		t.Fatalf("unrelated server lost: %#v", servers)
	}
}

func TestCLI_RemoveMissingNameErrors(t *testing.T) {
	path := configFilePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"one":{"url":"http://a/mcp"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)}, "remove", "ghost")
	mustExit(t, r, 1)
	mustBeDispatched(t, r, "remove")
	if strings.TrimSpace(r.Stdout) != "" {
		t.Fatalf("stdout must be empty on error, got: %q", r.Stdout)
	}
	if strings.TrimSpace(r.Stderr) == "" {
		t.Fatal("expected an error on stderr")
	}
	if servers := readServers(t, path); servers["one"]["url"] != "http://a/mcp" {
		t.Fatalf("file must be unchanged on error: %#v", servers)
	}
}

func TestCLI_RemoveMissingFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)}, "remove", "anything")
	mustExit(t, r, 1)
	mustBeDispatched(t, r, "remove")
	if strings.TrimSpace(r.Stdout) != "" {
		t.Fatalf("stdout must be empty on error, got: %q", r.Stdout)
	}
}

// ---------------------------------------------------------------------------
// Round-trip: unrelated servers and top-level keys survive.
// ---------------------------------------------------------------------------

func TestCLI_AddRemovePreserveUnrelated(t *testing.T) {
	path := configFilePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := `{"extra":{"keep":true},"mcpServers":{"other":{"url":"http://other/mcp"}}}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)},
		"add", "--url", "http://temp/mcp", "temp"); r.ExitCode != 0 {
		t.Fatalf("add failed: %s\n%s", r.Stdout, r.Stderr)
	}
	if r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)}, "remove", "temp"); r.ExitCode != 0 {
		t.Fatalf("remove failed: %s\n%s", r.Stdout, r.Stderr)
	}

	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFileString(t, path)), &top); err != nil {
		t.Fatal(err)
	}
	var extra map[string]any
	if err := json.Unmarshal(top["extra"], &extra); err != nil {
		t.Fatalf("unknown top-level key not preserved: %v", err)
	}
	if extra["keep"] != true {
		t.Fatalf("extra.keep = %v, want true", extra["keep"])
	}
	servers := readServers(t, path)
	if _, ok := servers["temp"]; ok {
		t.Fatalf("temp server should be gone: %#v", servers)
	}
	if servers["other"]["url"] != "http://other/mcp" {
		t.Fatalf("unrelated server not preserved: %#v", servers)
	}
}

// ---------------------------------------------------------------------------
// add/remove always edit the file, even when MCP_SERVERS_CONFIG is set.
// ---------------------------------------------------------------------------

func TestCLI_AddRemoveIgnoreInlineConfig(t *testing.T) {
	path := configFilePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inline := inlineConfig(configWithServers(map[string]string{
		"frominline": stdioServerJSON("npx"),
	}))
	env := baseEnv("MCP_CONFIG_PATH="+path, inline)

	r := run(t, runOpts{Env: env}, "add", "--url", "http://file/mcp", "fileadd")
	mustExit(t, r, 0)
	if servers := readServers(t, path); servers["fileadd"]["url"] != "http://file/mcp" {
		t.Fatalf("add must write the file even with inline config set: %#v", servers)
	}

	r = run(t, runOpts{Env: env}, "remove", "fileadd")
	mustExit(t, r, 0)
	if servers := readServers(t, path); len(servers) != 0 {
		t.Fatalf("remove must delete from the file even with inline config set: %#v", servers)
	}
}

// ---------------------------------------------------------------------------
// Comment-bearing configs (regression).
//
// `mcp config edit` can leave // and /* */ comments in the file, and
// `mcp --list` already accepts such a file. `add`/`remove` must parse it too,
// dropping comments on the rewrite rather than failing.
// ---------------------------------------------------------------------------

// assertCommentFreeJSON fails if path still contains comments or is not valid
// JSON. StripComments is the authority on comments, so it also detects
// leftovers (a `//` inside a URL string is not a comment).
func assertCommentFreeJSON(t *testing.T, path string) {
	t.Helper()
	got := readFileString(t, path)
	if stripped := config.StripComments(got); stripped != got {
		t.Fatalf("config still contains comments after rewrite:\n%s", got)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &top); err != nil {
		t.Fatalf("rewritten config is not valid JSON: %v\n%s", err, got)
	}
}

func TestCLI_AddRemoveCommentBearingConfig(t *testing.T) {
	path := configFilePath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := `{
  // comment before the object
  "mcpServers": {
    "other": {"url": "http://other/mcp"} /* keep this one */
  }
}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	r := run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)},
		"add", "--url", "http://new/mcp", "new")
	mustExit(t, r, 0)
	servers := readServers(t, path)
	if servers["new"]["url"] != "http://new/mcp" {
		t.Fatalf("add on comment-bearing config lost the new server: %#v", servers)
	}
	if servers["other"]["url"] != "http://other/mcp" {
		t.Fatalf("add on comment-bearing config lost the unrelated server: %#v", servers)
	}
	assertCommentFreeJSON(t, path)

	r = run(t, runOpts{Env: baseEnv("MCP_CONFIG_PATH=" + path)}, "remove", "new")
	mustExit(t, r, 0)
	servers = readServers(t, path)
	if _, ok := servers["new"]; ok {
		t.Fatalf("remove left the server behind: %#v", servers)
	}
	if servers["other"]["url"] != "http://other/mcp" {
		t.Fatalf("remove lost the unrelated server: %#v", servers)
	}
	assertCommentFreeJSON(t, path)
}
