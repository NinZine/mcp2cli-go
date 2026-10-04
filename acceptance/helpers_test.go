package acceptance_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// ---------------------------------------------------------------------------
// Binary build (TestMain)
// ---------------------------------------------------------------------------

var (
	// mcpBin is the absolute path to the built CLI binary.
	mcpBin string
	// testServerBin is the absolute path to the fake stdio MCP server.
	testServerBin string
	// buildErr records a build failure so tests can report it clearly.
	buildErr error
)

// TestMain builds the CLI and the fake stdio server once for the whole suite.
// During the RED phase the CLI build is expected to fail because ./cmd/mcp
// does not exist yet; we record the error and let every test fail with a
// clear message rather than aborting the process.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "mcp-acceptance-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, "acceptance: cannot create temp dir:", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmp)

	mcpBin = filepath.Join(tmp, "mcp")
	testServerBin = filepath.Join(tmp, "testserver")

	// Build the fake stdio MCP server first; it is part of the test harness.
	if out, err := runGoBuild(testServerBin, "./internal/testserver"); err != nil {
		fmt.Fprintf(os.Stderr, "acceptance: failed to build test server: %v\n%s\n", err, out)
		os.Exit(1)
	}

	// Build the CLI under test. Failure is expected in the RED phase.
	if out, err := runGoBuild(mcpBin, "./cmd/mcp"); err != nil {
		buildErr = fmt.Errorf("go build ./cmd/mcp failed: %w\n%s", err, out)
	}

	os.Exit(m.Run())
}

func runGoBuild(outPath, pkg string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-o", outPath, pkg)
	cmd.Dir = repoRoot()
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// repoRoot returns the module root (the directory containing go.mod).
func repoRoot() string {
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return wd
		}
		dir = parent
	}
}

// requireBinary fails the test with a clear message if the CLI was not built.
func requireBinary(t *testing.T) {
	t.Helper()
	if buildErr != nil {
		t.Fatalf("mcp binary unavailable (expected during RED phase):\n%v", buildErr)
	}
	if _, err := os.Stat(mcpBin); err != nil {
		t.Fatalf("mcp binary missing at %s: %v", mcpBin, err)
	}
}

// ---------------------------------------------------------------------------
// Running the CLI
// ---------------------------------------------------------------------------

// result captures a completed CLI invocation.
type result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// runOpts configures a CLI invocation.
type runOpts struct {
	// Env is the complete environment for the child. If nil, a minimal
	// deterministic environment is used (see baseEnv).
	Env []string
	// Stdin is fed to the child. If nil, stdin is /dev/null (not a TTY).
	Stdin io.Reader
	// Dir is the working directory. Defaults to a fresh temp dir.
	Dir string
}

// baseEnv returns a deterministic environment. It deliberately does NOT
// inherit the parent environment so tests cannot be affected by ambient
// MCP_* variables. PATH is preserved so stdio servers can be spawned.
func baseEnv(extra ...string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	return append(env, extra...)
}

// run executes the CLI with the given args and options and returns the result.
func run(t *testing.T, opts runOpts, args ...string) result {
	t.Helper()
	requireBinary(t)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, mcpBin, args...)
	if opts.Env != nil {
		cmd.Env = opts.Env
	} else {
		cmd.Env = baseEnv()
	}
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	} else {
		cmd.Dir = t.TempDir()
	}
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	} else {
		devnull, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatalf("open /dev/null: %v", err)
		}
		defer devnull.Close()
		cmd.Stdin = devnull
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("running mcp %v: %v", args, err)
		}
	}
	return result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// runPTY executes the CLI with stdout attached to a pseudo-terminal so the
// text renderer is exercised. stderr is captured separately via a pipe.
func runPTY(t *testing.T, opts runOpts, args ...string) result {
	t.Helper()
	requireBinary(t)

	cmd := exec.Command(mcpBin, args...)
	if opts.Env != nil {
		cmd.Env = opts.Env
	} else {
		cmd.Env = baseEnv()
	}
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	} else {
		cmd.Dir = t.TempDir()
	}
	if opts.Stdin != nil {
		cmd.Stdin = opts.Stdin
	}

	// Capture stderr through a pipe; stdout goes to the PTY.
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("pty.Start: %v", err)
	}
	defer ptmx.Close()

	var stdout bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stdout, ptmx)
	}()

	waitErr := cmd.Wait()
	wg.Wait()

	code := 0
	if waitErr != nil {
		var ee *exec.ExitError
		if ok := asExitError(waitErr, &ee); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("running mcp (pty) %v: %v", args, waitErr)
		}
	}
	return result{Stdout: stdout.String(), Stderr: stderr.String(), ExitCode: code}
}

// ---------------------------------------------------------------------------
// Config helpers
// ---------------------------------------------------------------------------

// inlineConfig returns an MCP_SERVERS_CONFIG environment entry.
func inlineConfig(jsonConfig string) string {
	return "MCP_SERVERS_CONFIG=" + jsonConfig
}

// stdioServerJSON builds a stdio server config object.
func stdioServerJSON(command string, args ...string) string {
	argsJSON, _ := json.Marshal(args)
	return fmt.Sprintf(`{"command":%q,"args":%s}`, command, argsJSON)
}

// configWithServers wraps server objects into a full config document.
func configWithServers(servers map[string]string) string {
	inner := make(map[string]json.RawMessage, len(servers))
	for name, obj := range servers {
		inner[name] = json.RawMessage(obj)
	}
	doc := map[string]any{"mcpServers": inner}
	b, _ := json.Marshal(doc)
	return string(b)
}

// testServerConfig returns a config with a single stdio server named `name`
// pointing at the fake test server binary.
func testServerConfig(name string) string {
	return configWithServers(map[string]string{
		name: stdioServerJSON(testServerBin),
	})
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

// decodeJSON unmarshals stdout into v, failing the test on invalid JSON.
func decodeJSON(t *testing.T, stdout string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(stdout), v); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\nstdout:\n%s", err, stdout)
	}
}

// mustContain asserts that haystack contains needle.
func mustContain(t *testing.T, haystack, needle, label string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Fatalf("%s: expected to contain %q\nactual:\n%s", label, needle, haystack)
	}
}

// mustNotContain asserts that haystack does not contain needle.
func mustNotContain(t *testing.T, haystack, needle, label string) {
	t.Helper()
	if strings.Contains(haystack, needle) {
		t.Fatalf("%s: expected NOT to contain %q\nactual:\n%s", label, needle, haystack)
	}
}

// mustExit asserts the exit code.
func mustExit(t *testing.T, r result, want int) {
	t.Helper()
	if r.ExitCode != want {
		t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s",
			r.ExitCode, want, r.Stdout, r.Stderr)
	}
}
