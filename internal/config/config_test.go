package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mapGetenv(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestSubstitute(t *testing.T) {
	getenv := mapGetenv(map[string]string{"TOKEN": "secret", "PATHX": "/bin"})

	cases := []struct {
		in   string
		want string
	}{
		{"${TOKEN}", "secret"},
		{"Bearer ${TOKEN}", "Bearer secret"},
		{"${MISSING}", ""},
		{"a${TOKEN}b${MISSING}c", "asecretbc"},
		{"no vars", "no vars"},
		{"${PATHX}/x", "/bin/x"},
	}
	for _, tc := range cases {
		if got := Substitute(tc.in, getenv); got != tc.want {
			t.Errorf("Substitute(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestStripComments(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "line comment",
			in:   "{\n  // hi\n  \"a\": 1\n}",
			want: "{\n  \n  \"a\": 1\n}",
		},
		{
			name: "block comment",
			in:   "{ /* hi */ \"a\": 1 }",
			want: "{  \"a\": 1 }",
		},
		{
			name: "https preserved",
			in:   `{"url":"https://example.invalid/mcp"}`,
			want: `{"url":"https://example.invalid/mcp"}`,
		},
		{
			name: "trailing comment after string",
			in:   `{"url":"https://x/mcp" // note` + "\n}",
			want: `{"url":"https://x/mcp" ` + "\n}",
		},
		{
			name: "escaped quote in string",
			in:   `{"a":"he said \"// not a comment\"","b":1}`,
			want: `{"a":"he said \"// not a comment\"","b":1}`,
		},
		{
			name: "block comment keeps newlines",
			in:   "{\n/* a\nb */\n\"x\":1}",
			want: "{\n\n\n\"x\":1}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StripComments(tc.in); got != tc.want {
				t.Errorf("StripComments(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestLoadContentValid(t *testing.T) {
	raw := `{
	  "mcpServers": {
	    "s1": {"command":"npx","args":["-y","pkg"],"env":{"A":"${V}"}},
	    "s2": {"url":"https://example.invalid/mcp","headers":{"H":"${V}"}}
	  }
	}`
	set, err := LoadContent(raw, mapGetenv(map[string]string{"V": "x"}))
	if err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	if len(set.Servers) != 2 {
		t.Fatalf("want 2 servers, got %d", len(set.Servers))
	}
	a := set.Find("s1")
	if a == nil || a.Type != TypeStdio || a.Command != "npx" || len(a.Args) != 2 || a.Env["A"] != "x" {
		t.Fatalf("s1 parsed wrong: %+v", a)
	}
	b := set.Find("s2")
	if b == nil || b.Type != TypeHTTP || b.URL != "https://example.invalid/mcp" || b.Headers["H"] != "x" {
		t.Fatalf("s2 parsed wrong: %+v", b)
	}
}

func TestLoadContentDefaultsArgsNotEmptySlice(t *testing.T) {
	set, err := LoadContent(`{"mcpServers":{"s":{"command":"npx"}}}`, mapGetenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if set.Servers[0].Args == nil {
		t.Fatal("args must be non-nil for deterministic JSON ([] not null)")
	}
}

func TestLoadContentReservedSkipped(t *testing.T) {
	raw := `{"mcpServers":{"list":{"command":"npx"},"ok":{"command":"npx"}}}`
	set, err := LoadContent(raw, mapGetenv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Servers) != 1 || set.Servers[0].Name != "ok" {
		t.Fatalf("reserved name not skipped: %+v", set.Servers)
	}
	if len(set.Warnings) != 1 {
		t.Fatalf("want 1 warning, got %v", set.Warnings)
	}
}

func TestLoadContentErrors(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"malformed", `{ not json `},
		{"top level array", `[]`},
		{"top level null", `null`},
		{"mcpServers array", `{"mcpServers":[]}`},
		{"neither command nor url", `{"mcpServers":{"s":{"foo":"bar"}}}`},
		{"env not object", `{"mcpServers":{"s":{"command":"npx","env":"x"}}}`},
		{"headers not object", `{"mcpServers":{"s":{"url":"https://x","headers":"x"}}}`},
		{"command not string", `{"mcpServers":{"s":{"command":5}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadContent(tc.raw, mapGetenv(nil)); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestLoadContentUnknownKeysIgnored(t *testing.T) {
	raw := `{"unknownTop":true,"mcpServers":{"s":{"command":"npx","future":123}}}`
	set, err := LoadContent(raw, mapGetenv(nil))
	if err != nil {
		t.Fatalf("unknown keys must be ignored: %v", err)
	}
	if len(set.Servers) != 1 || set.Servers[0].Name != "s" || set.Servers[0].Command != "npx" {
		t.Fatalf("unknown keys must be ignored, got %+v", set.Servers)
	}
}

func TestResolvePathPrecedence(t *testing.T) {
	t.Setenv("MCP_SERVERS_CONFIG", "")
	// Use paths that cannot be mistaken for an existing config.
	t.Setenv("MCP_CONFIG_PATH", "/tmp/explicit.json")
	t.Setenv("MCP_CONFIG_DIR", "/tmp/dir")
	path, _ := ResolvePath()
	if path != "/tmp/explicit.json" {
		t.Fatalf("path = %q, want /tmp/explicit.json", path)
	}

	os.Unsetenv("MCP_CONFIG_PATH")
	path, _ = ResolvePath()
	if path != filepath.Join("/tmp/dir", "servers.json") {
		t.Fatalf("path = %q, want /tmp/dir/servers.json", path)
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	os.Unsetenv("MCP_SERVERS_CONFIG")
	dir := t.TempDir()
	t.Setenv("MCP_CONFIG_PATH", filepath.Join(dir, "absent.json"))
	set, err := Load()
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if len(set.Servers) != 0 {
		t.Fatalf("want empty set, got %d", len(set.Servers))
	}
}

func TestLoadInlineOverridesFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "servers.json")
	if err := os.WriteFile(file, []byte(`{"mcpServers":{"fromfile":{"command":"npx"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MCP_CONFIG_PATH", file)
	t.Setenv("MCP_SERVERS_CONFIG", `{"mcpServers":{"frominline":{"command":"npx"}}}`)
	set, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(set.Servers) != 1 || set.Servers[0].Name != "frominline" {
		t.Fatalf("inline must win, got %+v", set.Servers)
	}
}

func TestErrorMessagesMentionFields(t *testing.T) {
	_, err := LoadContent(`{"mcpServers":{"s":{"command":"npx","env":"x"}}}`, mapGetenv(nil))
	if err == nil || !strings.Contains(err.Error(), "env") {
		t.Fatalf("error should mention env, got %v", err)
	}
}
