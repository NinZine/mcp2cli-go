package output

import (
	"strings"
	"testing"

	"github.com/NinZine/mcp2cli-go/internal/client"
)

// SPEC §9.5 — an unknown content type that nonetheless carries text renders the
// text, not the unsupported placeholder. `audio` is a valid MCP type the CLI
// does not special-case, so it exercises the default branch.
func TestToolResultUnknownTypeWithTextRendersText(t *testing.T) {
	r, out, _ := newRenderer()
	res := &client.Result{Content: []client.Content{{Type: "audio", Text: "hello audio"}}}
	if err := r.ToolResult(res); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	if !strings.Contains(s, "hello audio") {
		t.Fatalf("expected text to be printed, got %q", s)
	}
	if strings.Contains(s, "unsupported content type") {
		t.Fatalf("text-bearing unknown type must not render the placeholder: %q", s)
	}
}

// SPEC §9.5 — an unknown content type with neither text nor a known MIME type
// renders the placeholder with its type name.
func TestToolResultUnknownTypeWithoutTextRendersPlaceholder(t *testing.T) {
	r, out, _ := newRenderer()
	res := &client.Result{Content: []client.Content{{Type: "audio"}}}
	if err := r.ToolResult(res); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "[audio: unsupported content type]" {
		t.Fatalf("got %q", got)
	}
}

// SPEC §9.5 — isError with no content emits exactly one `error: (no details)`
// line on stderr and nothing on stdout.
func TestToolResultEmptyErrorStreamSeparation(t *testing.T) {
	r, out, errb := newRenderer()
	if err := r.ToolResult(&client.Result{IsError: true}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must be empty, got %q", out.String())
	}
	if got := strings.TrimSpace(errb.String()); got != "error: (no details)" {
		t.Fatalf("stderr = %q", got)
	}
}

// SPEC §9.1 — the text endpoint for a stdio server is `command args...`.
func TestServerListTextStdioEndpoint(t *testing.T) {
	r, out, _ := newRenderer()
	ok := true
	entries := []ServerEntry{
		{Name: "s", Type: "stdio", Command: "npx", Args: []string{"-y", "pkg"}, Available: &ok},
	}
	if err := r.ServerList(entries); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "npx -y pkg") {
		t.Fatalf("endpoint missing in:\n%s", out.String())
	}
}

// SPEC §9.6 — the text form of `config path` is just the path.
func TestConfigPathText(t *testing.T) {
	r, out, _ := newRenderer()
	if err := r.ConfigPath("/x/servers.json", true, "/x"); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(out.String()); got != "/x/servers.json" {
		t.Fatalf("got %q", got)
	}
}

// SPEC §9.3 — a tool with no schema still renders the Parameters section with
// an explicit "(none)" marker in text mode.
func TestToolInfoTextNoParameters(t *testing.T) {
	r, out, _ := newRenderer()
	tools := []client.Tool{{Name: "ping", Description: "Ping"}}
	if err := r.ToolInfo(tools); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"ping", "Parameters:", "(none)"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

// SPEC §9.5 — text mode with isError and multiple text blocks prefixes each
// line on stderr.
func TestToolResultMultipleErrorTextBlocks(t *testing.T) {
	r, out, errb := newRenderer()
	res := &client.Result{IsError: true, Content: []client.Content{
		{Type: "text", Text: "first"},
		{Type: "text", Text: "second"},
	}}
	if err := r.ToolResult(res); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must be empty, got %q", out.String())
	}
	s := errb.String()
	if !strings.Contains(s, "error: first") || !strings.Contains(s, "error: second") {
		t.Fatalf("stderr = %q", s)
	}
}
