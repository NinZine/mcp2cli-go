package preflight

import (
	"os"
	"path/filepath"
	"testing"
)

func writeExecutable(t *testing.T, dir, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBareNameFoundInPath(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "mytool", 0o755)
	ok, reason := Available("mytool", map[string]string{"PATH": dir})
	if !ok || reason != "" {
		t.Fatalf("Available = %v, %q; want true, empty", ok, reason)
	}
}

func TestBareNameNotFound(t *testing.T) {
	ok, reason := Available("definitely-not-real-xyz", map[string]string{"PATH": t.TempDir()})
	if ok {
		t.Fatal("expected unavailable")
	}
	want := "command not found in PATH: definitely-not-real-xyz"
	if reason != want {
		t.Fatalf("reason = %q, want %q", reason, want)
	}
}

func TestExplicitPathMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	ok, reason := Available(missing, nil)
	if ok {
		t.Fatal("expected unavailable")
	}
	if reason != "command does not exist: "+missing {
		t.Fatalf("reason = %q", reason)
	}
}

func TestExplicitPathNotExecutable(t *testing.T) {
	dir := t.TempDir()
	path := writeExecutable(t, dir, "plain", 0o644)
	ok, reason := Available(path, nil)
	if ok {
		t.Fatal("expected unavailable")
	}
	if reason != "command is not executable: "+path {
		t.Fatalf("reason = %q", reason)
	}
}

func TestExplicitPathExecutable(t *testing.T) {
	dir := t.TempDir()
	path := writeExecutable(t, dir, "run", 0o755)
	ok, reason := Available(path, nil)
	if !ok || reason != "" {
		t.Fatalf("Available = %v, %q; want true, empty", ok, reason)
	}
}

func TestEmptyPathDoesNotBlock(t *testing.T) {
	ok, reason := Available("anything-bare", map[string]string{"PATH": ""})
	if !ok || reason != "" {
		t.Fatalf("empty PATH must not block, got %v %q", ok, reason)
	}
}

func TestEnvPathOverridesProcessPath(t *testing.T) {
	dir := t.TempDir()
	writeExecutable(t, dir, "onlyhere", 0o755)
	// Process PATH almost certainly lacks onlyhere; env PATH points at it.
	ok, reason := Available("onlyhere", map[string]string{"PATH": dir})
	if !ok {
		t.Fatalf("env PATH must be honored: %q", reason)
	}
}

func TestBareNameFallsBackToProcessPath(t *testing.T) {
	// "sh" is on the process PATH and env does not set PATH.
	ok, reason := Available("sh", nil)
	if !ok {
		t.Fatalf("fallback to process PATH failed: %q", reason)
	}
}
