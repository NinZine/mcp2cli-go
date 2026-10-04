package preflight

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// SPEC §8.4 — the server's own env.PATH is authoritative: when it is set (and
// non-empty) the process PATH must NOT be consulted as a fallback.
func TestEnvPathShadowsProcessPath(t *testing.T) {
	full, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("sh not found on process PATH; cannot exercise shadowing")
	}
	base := filepath.Base(full)

	ok, reason := Available(base, map[string]string{"PATH": t.TempDir()})
	if ok {
		t.Fatalf("env PATH must shadow process PATH: %q resolved but should be unavailable", base)
	}
	want := "command not found in PATH: " + base
	if reason != want {
		t.Fatalf("reason = %q, want %q", reason, want)
	}
}

// SPEC §8.4 — when the server provides no env.PATH and the process PATH is
// empty, a bare command must not be blocked.
func TestProcessPathEmptyDoesNotBlock(t *testing.T) {
	t.Setenv("PATH", "")
	ok, reason := Available("anything-bare", nil)
	if !ok || reason != "" {
		t.Fatalf("empty process PATH must not block, got available=%v reason=%q", ok, reason)
	}
}

// SPEC §8.4 — when PATH is unset entirely, a bare command must not be blocked.
func TestProcessPathUnsetDoesNotBlock(t *testing.T) {
	orig, had := os.LookupEnv("PATH")
	if err := os.Unsetenv("PATH"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if had {
			_ = os.Setenv("PATH", orig)
		}
	}()

	ok, reason := Available("anything-bare", nil)
	if !ok || reason != "" {
		t.Fatalf("unset process PATH must not block, got available=%v reason=%q", ok, reason)
	}
}

// SPEC §8.4 — an env.PATH that is explicitly empty (rather than absent) must
// also not block, even if the process PATH would resolve the command.
func TestEnvPathEmptyDoesNotFallBackToProcessPath(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not found on process PATH")
	}
	ok, reason := Available("sh", map[string]string{"PATH": ""})
	if !ok || reason != "" {
		t.Fatalf("explicit empty env PATH must not block, got available=%v reason=%q", ok, reason)
	}
}
