package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// SPEC §6.2 — substitution must apply to every string location, not just env
// and headers: command, args, url, env values, and header values.
func TestLoadContentSubstitutesEveryStringLocation(t *testing.T) {
	raw := `{"mcpServers":{
	  "s": {
	    "command":"${CMD}",
	    "args":["--token","${TOKEN}","${MISSING}"],
	    "env":{"E":"${TOKEN}"}
	  },
	  "h": {
	    "url":"https://${HOST}/mcp",
	    "headers":{"Authorization":"Bearer ${TOKEN}"}
	  }
	}}`

	getenv := func(k string) string {
		switch k {
		case "CMD":
			return "/usr/bin/server"
		case "TOKEN":
			return "tok"
		case "HOST":
			return "example.invalid"
		}
		return ""
	}

	set, err := LoadContent(raw, getenv)
	if err != nil {
		t.Fatalf("LoadContent: %v", err)
	}

	s := set.Find("s")
	if s == nil {
		t.Fatal("server s missing")
	}
	if s.Command != "/usr/bin/server" {
		t.Errorf("command = %q, want /usr/bin/server", s.Command)
	}
	wantArgs := []string{"--token", "tok", ""}
	if len(s.Args) != len(wantArgs) {
		t.Fatalf("args = %v, want %v", s.Args, wantArgs)
	}
	for i := range wantArgs {
		if s.Args[i] != wantArgs[i] {
			t.Errorf("args[%d] = %q, want %q", i, s.Args[i], wantArgs[i])
		}
	}
	if s.Env["E"] != "tok" {
		t.Errorf("env E = %q, want tok", s.Env["E"])
	}

	h := set.Find("h")
	if h == nil {
		t.Fatal("server h missing")
	}
	if h.URL != "https://example.invalid/mcp" {
		t.Errorf("url = %q, want https://example.invalid/mcp", h.URL)
	}
	if h.Headers["Authorization"] != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", h.Headers["Authorization"], "Bearer tok")
	}
}

// SPEC §6.2 — comment markers inside JSON string literals must survive
// comment stripping, including `//`, `/*...*/`, and `https://`.
func TestLoadContentCommentsInsideStringsPreserved(t *testing.T) {
	raw := `{"mcpServers":{"s":{"command":"npx","args":["a//b","/*c*/","http://x/y"]}}}`
	set, err := LoadContent(raw, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	s := set.Find("s")
	if s == nil {
		t.Fatal("server s missing")
	}
	want := []string{"a//b", "/*c*/", "http://x/y"}
	if len(s.Args) != len(want) {
		t.Fatalf("args = %v, want %v", s.Args, want)
	}
	for i := range want {
		if s.Args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, s.Args[i], want[i])
		}
	}
}

// SPEC §6.2 — substitution happens before comment stripping, so a substituted
// value containing comment markers that is already inside a string is intact.
func TestLoadContentSubstitutedValueWithCommentMarkers(t *testing.T) {
	raw := `{"mcpServers":{"s":{"command":"npx","args":["${V}"]}}}`
	set, err := LoadContent(raw, func(k string) string {
		if k == "V" {
			return "a//b/*c*/"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	if got := set.Find("s").Args[0]; got != "a//b/*c*/" {
		t.Fatalf("args[0] = %q, want %q", got, "a//b/*c*/")
	}
}

// SPEC §6.1 — when the home directory cannot be determined the path falls back
// to /tmp/mcp/servers.json and a warning is reported.
func TestResolvePathHomeUnsetFallsBackToTmp(t *testing.T) {
	t.Setenv("MCP_CONFIG_PATH", "")
	t.Setenv("MCP_CONFIG_DIR", "")
	// An empty HOME is treated as "not defined" by os.UserHomeDir on Unix.
	t.Setenv("HOME", "")

	path, warnings := ResolvePath()
	want := filepath.Join("/tmp", "mcp", "servers.json")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1", warnings)
	}
	if !strings.Contains(warnings[0], "/tmp/mcp/servers.json") {
		t.Fatalf("warning %q should mention /tmp/mcp/servers.json", warnings[0])
	}
}

// SPEC §6.3 — `"cli": true` disqualifies a command from being a stdio server;
// with no url the entry is an error.
func TestLoadContentCLIFlagWithoutURLIsError(t *testing.T) {
	_, err := LoadContent(`{"mcpServers":{"s":{"command":"npx","cli":true}}}`, mapGetenv(nil))
	if err == nil {
		t.Fatal("expected error for cli:true server without url")
	}
}

// SPEC §6.3 — args defaults to an empty (non-nil) slice, including when the
// field is explicitly null.
func TestLoadContentArgsNullIsEmptySlice(t *testing.T) {
	set, err := LoadContent(`{"mcpServers":{"s":{"command":"npx","args":null}}}`, mapGetenv(nil))
	if err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	if set.Find("s").Args == nil {
		t.Fatal("args must be non-nil after explicit null")
	}
}

// SPEC §6.3 — env values must be strings; a numeric value is an error.
func TestLoadContentEnvNonStringValueIsError(t *testing.T) {
	_, err := LoadContent(`{"mcpServers":{"s":{"command":"npx","env":{"N":5}}}}`, mapGetenv(nil))
	if err == nil {
		t.Fatal("expected error for non-string env value")
	}
}

// SPEC §6.2 — a present-but-not-an-object mcpServers (including null, arrays,
// strings, numbers, and bools) is an error.
func TestLoadContentMcpServersPresentNonObjectIsError(t *testing.T) {
	cases := []string{
		`{"mcpServers":null}`,
		`{"mcpServers":[]}`,
		`{"mcpServers":"x"}`,
		`{"mcpServers":1}`,
		`{"mcpServers":true}`,
	}
	for _, raw := range cases {
		if _, err := LoadContent(raw, mapGetenv(nil)); err == nil {
			t.Errorf("LoadContent(%s) = nil error, want error", raw)
		}
	}
}

// SPEC §6.2 — an absent mcpServers key still means an empty server set.
func TestLoadContentMcpServersAbsentIsEmpty(t *testing.T) {
	set, err := LoadContent(`{}`, mapGetenv(nil))
	if err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	if len(set.Servers) != 0 {
		t.Fatalf("absent mcpServers must be empty, got %d", len(set.Servers))
	}
}

// SPEC §6.3 — env and headers are validated whenever present, regardless of the
// inferred server type: a malformed cross-type field is an error.
func TestLoadContentCrossTypeEnvHeadersValidated(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"stdio env not object", `{"mcpServers":{"s":{"command":"npx","env":"not-an-object"}}}`},
		{"stdio headers not object", `{"mcpServers":{"s":{"command":"npx","headers":"nope"}}}`},
		{"http env not object", `{"mcpServers":{"s":{"url":"https://example.invalid/mcp","env":"not-an-object"}}}`},
		{"http headers not object", `{"mcpServers":{"s":{"url":"https://example.invalid/mcp","headers":"nope"}}}`},
		{"stdio env null", `{"mcpServers":{"s":{"command":"npx","env":null}}}`},
		{"http headers null", `{"mcpServers":{"s":{"url":"https://example.invalid/mcp","headers":null}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := LoadContent(tc.raw, mapGetenv(nil)); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

// SPEC §6.2/§6.3 — a substituted value containing quotes and backslashes must
// round-trip unchanged: substitution is JSON-escaped so it cannot break the
// surrounding string literal.
func TestLoadContentSubstitutedValueWithQuotesAndBackslashes(t *testing.T) {
	raw := `{"mcpServers":{"s":{"command":"npx","env":{"E":"${V}"}}}}`
	want := `ab"cd\ef`
	set, err := LoadContent(raw, func(k string) string {
		if k == "V" {
			return want
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	s := set.Find("s")
	if s == nil {
		t.Fatal("server s missing")
	}
	if got := s.Env["E"]; got != want {
		t.Fatalf("env E = %q, want %q", got, want)
	}
}

// SPEC §6.2 — a substituted value must not be able to inject structural JSON.
// The placeholder sits inside a string value, so escaping keeps the payload
// contained: no second url appears and the header keeps the raw env string.
func TestLoadContentSubstitutionCannotInjectStructure(t *testing.T) {
	raw := `{"mcpServers":{"s":{"url":"https://real.example/mcp","headers":{"X":"${DATA}"}}}}`
	data := `"}, "url": "https://evil.example`
	set, err := LoadContent(raw, func(k string) string {
		if k == "DATA" {
			return data
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	if len(set.Servers) != 1 {
		t.Fatalf("want 1 server, got %d", len(set.Servers))
	}
	s := set.Find("s")
	if s == nil {
		t.Fatal("server s missing")
	}
	if s.URL != "https://real.example/mcp" {
		t.Fatalf("url = %q, want the real url unchanged", s.URL)
	}
	if got := s.Headers["X"]; got != data {
		t.Fatalf("header X = %q, want raw env string %q", got, data)
	}
}

// SPEC §6.2 — a substituted value containing a newline is escaped by the
// substitution step and parses back to the exact original string.
func TestLoadContentSubstitutedValueWithNewline(t *testing.T) {
	raw := `{"mcpServers":{"s":{"command":"npx","env":{"E":"${V}"}}}}`
	want := "line1\nline2"
	set, err := LoadContent(raw, func(k string) string {
		if k == "V" {
			return want
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	if got := set.Find("s").Env["E"]; got != want {
		t.Fatalf("env E = %q, want %q", got, want)
	}
}

// SPEC §6.2 — a substituted value containing `//` is inside a string literal,
// so the subsequent comment-stripping step must leave it intact.
func TestLoadContentSubstitutedValueWithSlashSlashPreserved(t *testing.T) {
	raw := `{"mcpServers":{"s":{"command":"npx","args":["${V}"]}}}`
	want := "http://example.invalid//path"
	set, err := LoadContent(raw, func(k string) string {
		if k == "V" {
			return want
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadContent: %v", err)
	}
	if got := set.Find("s").Args[0]; got != want {
		t.Fatalf("args[0] = %q, want %q", got, want)
	}
}

// SPEC §6.2/§6.3 — a well-typed env/headers field of the other server type is
// accepted (and may be ignored for behavior).
func TestLoadContentCrossTypeEnvHeadersObjectAccepted(t *testing.T) {
	raw := `{"mcpServers":{
	  "s":{"command":"npx","env":{"A":"1"},"headers":{"H":"x"}},
	  "h":{"url":"https://example.invalid/mcp","env":{"A":"1"},"headers":{"H":"x"}}
	}}`
	set, err := LoadContent(raw, mapGetenv(nil))
	if err != nil {
		t.Fatalf("valid cross-type fields must be accepted: %v", err)
	}
	if len(set.Servers) != 2 {
		t.Fatalf("want 2 servers, got %d", len(set.Servers))
	}
}
