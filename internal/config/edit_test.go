package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// ---------------------------------------------------------------------------
// Improvement 1 — config file editing API (planned internal/config/edit.go).
//
// These tests reference AddHTTPServer / RemoveServer / IsReserved, which do
// not exist yet. They are expected to fail to compile until the planned
// production code lands (RED phase).
// ---------------------------------------------------------------------------

// readFileString reads path or fails the test.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// readServers returns the parsed mcpServers object from a config file.
func readServers(t *testing.T, path string) map[string]map[string]any {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFileString(t, path)), &top); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	raw, ok := top["mcpServers"]
	if !ok {
		return nil
	}
	var servers map[string]map[string]any
	if err := json.Unmarshal(raw, &servers); err != nil {
		t.Fatalf("mcpServers is not an object: %v", err)
	}
	return servers
}

// readTopKey decodes a top-level key into v.
func readTopKey(t *testing.T, path, key string, v any) {
	t.Helper()
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFileString(t, path)), &top); err != nil {
		t.Fatalf("config is not valid JSON: %v", err)
	}
	raw, ok := top[key]
	if !ok {
		t.Fatalf("top-level key %q missing", key)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("decode %q: %v", key, err)
	}
}

// ---------------------------------------------------------------------------
// IsReserved
// ---------------------------------------------------------------------------

func TestIsReserved(t *testing.T) {
	reserved := []string{
		"search", "add", "remove", "update", "list", "help", "version",
		"serve", "logs", "acl", "config", "completions", "healthcheck",
	}
	for _, name := range reserved {
		if !IsReserved(name) {
			t.Errorf("IsReserved(%q) = false, want true", name)
		}
	}
	notReserved := []string{"sentry", "linear", "my-server", "Add", "CONFIG", ""}
	for _, name := range notReserved {
		if IsReserved(name) {
			t.Errorf("IsReserved(%q) = true, want false", name)
		}
	}
}

// ---------------------------------------------------------------------------
// AddHTTPServer
// ---------------------------------------------------------------------------

func TestAddHTTPServerHappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "servers.json")

	if err := AddHTTPServer(path, "s", "https://example.invalid/mcp"); err != nil {
		t.Fatalf("AddHTTPServer: %v", err)
	}

	// Parent directories must be created.
	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("parent dir not created: %v", err)
	}

	// Exact on-disk shape: two-space indent, JSON object, trailing newline.
	want := `{
  "mcpServers": {
    "s": {
      "url": "https://example.invalid/mcp"
    }
  }
}
`
	if got := readFileString(t, path); got != want {
		t.Fatalf("file content:\n%q\nwant:\n%q", got, want)
	}

	// Mode 0644.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("file mode = %o, want 644", perm)
	}

	servers := readServers(t, path)
	if len(servers) != 1 || servers["s"]["url"] != "https://example.invalid/mcp" {
		t.Fatalf("unexpected servers: %#v", servers)
	}
}

func TestAddHTTPServerDuplicateName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")

	if err := AddHTTPServer(path, "s", "https://a.example/mcp"); err != nil {
		t.Fatalf("first AddHTTPServer: %v", err)
	}
	if err := AddHTTPServer(path, "s", "https://b.example/mcp"); err == nil {
		t.Fatal("duplicate AddHTTPServer must error")
	}

	servers := readServers(t, path)
	if len(servers) != 1 {
		t.Fatalf("duplicate must not add a second entry: %#v", servers)
	}
	if servers["s"]["url"] != "https://a.example/mcp" {
		t.Fatalf("first url was overwritten: %#v", servers["s"])
	}
}

func TestAddHTTPServerURLValidation(t *testing.T) {
	invalid := []string{
		"",
		"not-a-url",
		"ftp://example.invalid/mcp",
		"file:///tmp/x",
		"//example.invalid/mcp",
		"/relative/path",
		"example.invalid/mcp",
	}
	for _, rawURL := range invalid {
		t.Run("invalid_"+rawURL, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "servers.json")
			if err := AddHTTPServer(path, "s", rawURL); err == nil {
				t.Fatalf("AddHTTPServer(%q) = nil error, want error", rawURL)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("file must not be created for invalid URL %q", rawURL)
			}
		})
	}

	valid := []string{
		"http://localhost:38429/mcp",
		"https://example.invalid/mcp",
	}
	for _, rawURL := range valid {
		t.Run("valid_"+rawURL, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "servers.json")
			if err := AddHTTPServer(path, "s", rawURL); err != nil {
				t.Fatalf("AddHTTPServer(%q): %v", rawURL, err)
			}
		})
	}
}

// Unrelated servers and unknown top-level keys must survive a rewrite.
func TestAddHTTPServerPreservesUnrelatedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	initial := `{"extra":{"keep":true},"mcpServers":{"other":{"command":"npx"}}}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AddHTTPServer(path, "new", "https://new.example/mcp"); err != nil {
		t.Fatalf("AddHTTPServer: %v", err)
	}

	var extra map[string]any
	readTopKey(t, path, "extra", &extra)
	if extra["keep"] != true {
		t.Fatalf("unknown top-level key not preserved: %#v", extra)
	}

	servers := readServers(t, path)
	if servers["other"]["command"] != "npx" {
		t.Fatalf("unrelated server not preserved: %#v", servers)
	}
	if servers["new"]["url"] != "https://new.example/mcp" {
		t.Fatalf("new server missing: %#v", servers)
	}
}

// A missing file starts from an empty document (the common `add` case).
func TestAddHTTPServerMissingFileStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	if err := AddHTTPServer(path, "s", "http://host/mcp"); err != nil {
		t.Fatalf("AddHTTPServer on missing file: %v", err)
	}
	if servers := readServers(t, path); servers["s"]["url"] != "http://host/mcp" {
		t.Fatalf("server not written: %#v", servers)
	}
}

// ---------------------------------------------------------------------------
// RemoveServer
// ---------------------------------------------------------------------------

func TestRemoveServerHappyPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	initial := `{"mcpServers":{"one":{"url":"https://a/mcp"},"two":{"url":"https://b/mcp"}},"extra":{"keep":true}}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveServer(path, "one"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}

	servers := readServers(t, path)
	if _, ok := servers["one"]; ok {
		t.Fatalf("server was not removed: %#v", servers)
	}
	if servers["two"]["url"] != "https://b/mcp" {
		t.Fatalf("unrelated server not preserved: %#v", servers)
	}

	var extra map[string]any
	readTopKey(t, path, "extra", &extra)
	if extra["keep"] != true {
		t.Fatalf("unknown top-level key not preserved: %#v", extra)
	}
}

func TestRemoveServerMissingName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	initial := `{"mcpServers":{"one":{"url":"https://a/mcp"}}}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveServer(path, "nope"); err == nil {
		t.Fatal("removing a missing name must error")
	}

	if servers := readServers(t, path); servers["one"]["url"] != "https://a/mcp" {
		t.Fatalf("file must be unchanged on error: %#v", servers)
	}
}

// The config file must exist for a removal.
func TestRemoveServerMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	if err := RemoveServer(path, "x"); err == nil {
		t.Fatal("removing from a missing file must error")
	}
}

// An add/remove round-trip preserves everything unrelated to the edited name.
func TestAddRemoveRoundTripPreservesUnrelated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	initial := `{"extra":{"keep":true},"mcpServers":{"other":{"command":"npx","args":["-y","pkg"]}}}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AddHTTPServer(path, "temp", "https://temp.example/mcp"); err != nil {
		t.Fatalf("AddHTTPServer: %v", err)
	}
	if err := RemoveServer(path, "temp"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}

	var extra map[string]any
	readTopKey(t, path, "extra", &extra)
	if extra["keep"] != true {
		t.Fatalf("unknown top-level key not preserved: %#v", extra)
	}
	servers := readServers(t, path)
	if _, ok := servers["temp"]; ok {
		t.Fatalf("temp server should be gone: %#v", servers)
	}
	if servers["other"]["command"] != "npx" {
		t.Fatalf("unrelated server not preserved: %#v", servers)
	}
}

// ---------------------------------------------------------------------------
// Comment-bearing configs (regression).
//
// `mcp config edit` can leave // and /* */ comments in the file. The loader
// accepts them, and add/remove must too: comments are dropped by the rewrite,
// not treated as a hard error.
// ---------------------------------------------------------------------------

// assertCommentFreeValidJSON fails if path still contains comments or is not
// parseable JSON. StripComments is the authority on what a comment is, so it is
// also used to detect leftovers (a `//` inside a URL string is not a comment).
func assertCommentFreeValidJSON(t *testing.T, path string) {
	t.Helper()
	got := readFileString(t, path)
	if stripped := StripComments(got); stripped != got {
		t.Fatalf("comments were not dropped on rewrite:\n%s", got)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(got), &top); err != nil {
		t.Fatalf("rewritten config is not valid JSON: %v\n%s", err, got)
	}
}

// AddHTTPServer must accept a config containing line and block comments, drop
// the comments, and preserve unrelated keys and servers.
func TestAddHTTPServerCommentBearingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	initial := `{
  // a line comment before the object
  "extra": {"keep": true}, /* a block comment */
  "mcpServers": {
    "other": {"command": "npx"} // trailing line comment
  }
}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := AddHTTPServer(path, "new", "https://new.example/mcp"); err != nil {
		t.Fatalf("AddHTTPServer on comment-bearing config: %v", err)
	}

	assertCommentFreeValidJSON(t, path)

	var extra map[string]any
	readTopKey(t, path, "extra", &extra)
	if extra["keep"] != true {
		t.Fatalf("unknown top-level key not preserved: %#v", extra)
	}
	servers := readServers(t, path)
	if servers["other"]["command"] != "npx" {
		t.Fatalf("unrelated server not preserved: %#v", servers)
	}
	if servers["new"]["url"] != "https://new.example/mcp" {
		t.Fatalf("new server missing: %#v", servers)
	}
}

// ---------------------------------------------------------------------------
// Atomic writes (regression).
//
// writeRoot must replace the target via a same-directory temp file + rename so
// a partial write cannot destroy the config. After a successful add/remove the
// content and mode are unchanged from the documented shape and no temp residue
// remains.
// ---------------------------------------------------------------------------

// assertNoTempResidue fails if any writeRoot temp file is left behind.
func assertNoTempResidue(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".servers-*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("leftover temp files: %v", matches)
	}
}

func TestWriteRootAtomicNoTempResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")

	if err := AddHTTPServer(path, "s", "https://example.invalid/mcp"); err != nil {
		t.Fatalf("AddHTTPServer: %v", err)
	}
	assertNoTempResidue(t, dir)

	// Content and mode are exactly as documented.
	want := `{
  "mcpServers": {
    "s": {
      "url": "https://example.invalid/mcp"
    }
  }
}
`
	if got := readFileString(t, path); got != want {
		t.Fatalf("file content:\n%q\nwant:\n%q", got, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("file mode = %o, want 644", perm)
	}

	if err := RemoveServer(path, "s"); err != nil {
		t.Fatalf("RemoveServer: %v", err)
	}
	assertNoTempResidue(t, dir)
}

// An existing file's permissions are preserved across an atomic replace.
func TestWriteRootAtomicPreservesMode(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "servers.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := AddHTTPServer(path, "s", "https://example.invalid/mcp"); err != nil {
		t.Fatalf("AddHTTPServer: %v", err)
	}
	assertNoTempResidue(t, dir)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file mode = %o, want preserved 600", perm)
	}
}

// ---------------------------------------------------------------------------
// Present-but-not-object mcpServers (regression, SPEC §6.2).
//
// The loader rejects a present mcpServers that is not an object. The editor
// must agree: add/remove fail and leave the file untouched rather than silently
// repairing null (or any other non-object) into an object.
// ---------------------------------------------------------------------------

func TestEditRejectsPresentNonObjectMCPServers(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"null", `null`},
		{"array", `[]`},
		{"string", `"x"`},
		{"number", `1`},
		{"bool", `true`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "servers.json")
			initial := `{"mcpServers":` + tc.raw + `}`
			if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
				t.Fatal(err)
			}
			before := readFileString(t, path)

			if err := AddHTTPServer(path, "new", "https://new.example/mcp"); err == nil {
				t.Fatal("AddHTTPServer must reject present non-object mcpServers")
			}
			if got := readFileString(t, path); got != before {
				t.Fatalf("file changed on add error:\n%q", got)
			}

			if err := RemoveServer(path, "x"); err == nil {
				t.Fatal("RemoveServer must reject present non-object mcpServers")
			}
			if got := readFileString(t, path); got != before {
				t.Fatalf("file changed on remove error:\n%q", got)
			}
		})
	}
}

// An absent mcpServers key still means an empty set for add and remove.
func TestEditAllowsAbsentMCPServers(t *testing.T) {
	addPath := filepath.Join(t.TempDir(), "add.json")
	if err := os.WriteFile(addPath, []byte(`{"extra":{"keep":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AddHTTPServer(addPath, "s", "https://example.invalid/mcp"); err != nil {
		t.Fatalf("AddHTTPServer with absent mcpServers: %v", err)
	}
	if servers := readServers(t, addPath); servers["s"]["url"] != "https://example.invalid/mcp" {
		t.Fatalf("server not added: %#v", servers)
	}

	removePath := filepath.Join(t.TempDir(), "remove.json")
	if err := os.WriteFile(removePath, []byte(`{"extra":{"keep":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RemoveServer(removePath, "missing"); err == nil {
		t.Fatal("RemoveServer on absent key must report the missing name")
	}
}

// RemoveServer must accept a config containing comments, drop the comments, and
// remove the named entry while preserving the rest.
func TestRemoveServerCommentBearingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	initial := `{
  /* leading block comment */
  "mcpServers": {
    "one": {"url": "https://a/mcp"}, // remove this one
    "two": {"url": "https://b/mcp"}
  }
}`
	if err := os.WriteFile(path, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := RemoveServer(path, "one"); err != nil {
		t.Fatalf("RemoveServer on comment-bearing config: %v", err)
	}

	assertCommentFreeValidJSON(t, path)

	servers := readServers(t, path)
	if _, ok := servers["one"]; ok {
		t.Fatalf("server was not removed: %#v", servers)
	}
	if servers["two"]["url"] != "https://b/mcp" {
		t.Fatalf("unrelated server not preserved: %#v", servers)
	}
}
