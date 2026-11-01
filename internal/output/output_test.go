package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NinZine/mcp2cli-go/internal/client"
)

func newRenderer() (*Renderer, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	err := &bytes.Buffer{}
	return &Renderer{Out: out, Err: err}, out, err
}

func TestServerListJSONShape(t *testing.T) {
	r, out, _ := newRenderer()
	r.JSON = true
	avail := false
	entries := []ServerEntry{
		{Name: "remote", Type: "http", URL: "https://example.invalid/mcp"},
		{Name: "local", Type: "stdio", Command: "npx", Args: []string{"-y", "pkg"},
			Available: &avail, UnavailableReason: "command not found in PATH: npx"},
	}
	if err := r.ServerList(entries); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out.String())
	}
	if len(got) != 2 {
		t.Fatalf("want 2 entries, got %d", len(got))
	}
	if _, ok := got[0]["command"]; ok {
		t.Fatalf("http entry must not carry command: %v", got[0])
	}
	if got[1]["available"] != false {
		t.Fatalf("stdio unavailable entry missing available:false: %v", got[1])
	}
	if got[1]["unavailable_reason"] != "command not found in PATH: npx" {
		t.Fatalf("reason = %v", got[1]["unavailable_reason"])
	}
}

func TestServerListJSONEmptyIsArray(t *testing.T) {
	r, out, _ := newRenderer()
	r.JSON = true
	if err := r.ServerList(nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Fatalf("empty list must render [], got %q", out.String())
	}
}

func TestServerListJSONStdioArgsNotNull(t *testing.T) {
	r, out, _ := newRenderer()
	r.JSON = true
	ok := true
	entries := []ServerEntry{{Name: "s", Type: "stdio", Command: "npx", Args: nil, Available: &ok}}
	if err := r.ServerList(entries); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"args": []`) {
		t.Fatalf("args must be [] not null:\n%s", out.String())
	}
}

func TestServerListTextSummaryAndStatus(t *testing.T) {
	r, out, _ := newRenderer()
	avail := false
	entries := []ServerEntry{
		{Name: "a", Type: "http", URL: "https://x/mcp"},
		{Name: "b", Type: "stdio", Command: "nope", Available: &avail, UnavailableReason: "command not found in PATH: nope"},
	}
	if err := r.ServerList(entries); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"Server", "Type", "Endpoint", "Status", "command not found in PATH: nope", "2 server(s) configured"} {
		if !strings.Contains(s, want) {
			t.Errorf("text output missing %q:\n%s", want, s)
		}
	}
}

func TestServerListTextNoStatusWhenAllAvailable(t *testing.T) {
	r, out, _ := newRenderer()
	ok := true
	entries := []ServerEntry{{Name: "a", Type: "stdio", Command: "npx", Available: &ok}}
	if err := r.ServerList(entries); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "Status") {
		t.Fatalf("Status column must be absent when everything is available:\n%s", out.String())
	}
}

func TestToolListJSON(t *testing.T) {
	r, out, _ := newRenderer()
	r.JSON = true
	tools := []client.Tool{{Name: "search_issues", Description: "Search for issues"}}
	if err := r.ToolList(tools); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got[0]["name"] != "search_issues" || got[0]["description"] != "Search for issues" {
		t.Fatalf("unexpected tool list JSON: %v", got)
	}
	if _, ok := got[0]["inputSchema"]; ok {
		t.Fatalf("--list JSON must not include inputSchema: %v", got[0])
	}
}

func TestToolListTextSummary(t *testing.T) {
	r, out, _ := newRenderer()
	tools := []client.Tool{{Name: "a"}, {Name: "b"}}
	if err := r.ToolList(tools); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "2 tool(s) available") {
		t.Fatalf("missing summary:\n%s", out.String())
	}
}

func TestToolInfoJSONAndText(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "The search query"},
		},
		"required": []any{"query"},
	}
	tools := []client.Tool{{Name: "search_issues", Description: "Search for issues", InputSchema: schema}}

	r, out, _ := newRenderer()
	r.JSON = true
	if err := r.ToolInfo(tools); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got[0]["inputSchema"].(map[string]any); !ok {
		t.Fatalf("inputSchema missing: %v", got[0])
	}

	r2, out2, _ := newRenderer()
	if err := r2.ToolInfo(tools); err != nil {
		t.Fatal(err)
	}
	s := out2.String()
	for _, want := range []string{"search_issues", "Search for issues", "Parameters", "query", "string", "(required)"} {
		if !strings.Contains(s, want) {
			t.Errorf("info text missing %q:\n%s", want, s)
		}
	}
}

func TestHealthRendering(t *testing.T) {
	r, out, _ := newRenderer()
	r.JSON = true
	if err := r.Health("fake", "2025-11-25"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	decode(t, out.String(), &got)
	if got["server"] != "fake" || got["status"] != "ok" || got["protocolVersion"] != "2025-11-25" {
		t.Fatalf("unexpected health JSON: %v", got)
	}

	r2, out2, _ := newRenderer()
	if err := r2.Health("fake", "2025-11-25"); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out2.String()) != "fake: ok" {
		t.Fatalf("health text = %q", out2.String())
	}
}

func TestConfigPathRendering(t *testing.T) {
	r, out, _ := newRenderer()
	r.JSON = true
	if err := r.ConfigPath("/x/servers.json", true, "/x"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	decode(t, out.String(), &got)
	if got["path"] != "/x/servers.json" || got["exists"] != true || got["dir"] != "/x" {
		t.Fatalf("unexpected config path JSON: %v", got)
	}
}

func TestToolResultJSONAlwaysHasIsError(t *testing.T) {
	r, out, _ := newRenderer()
	r.JSON = true
	res := &client.Result{
		Content: []client.Content{{Type: "text", Text: "hi", Raw: map[string]any{"type": "text", "text": "hi"}}},
		IsError: false,
	}
	if err := r.ToolResult(res); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	decode(t, out.String(), &got)
	if _, ok := got["isError"]; !ok {
		t.Fatalf("isError must always be present:\n%s", out.String())
	}
	if got["isError"] != false {
		t.Fatalf("isError = %v, want false", got["isError"])
	}
	if !strings.Contains(out.String(), "\n  ") {
		t.Fatalf("JSON must be two-space indented:\n%s", out.String())
	}
}

func TestToolResultTextSuccess(t *testing.T) {
	r, out, errb := newRenderer()
	res := &client.Result{Content: []client.Content{{Type: "text", Text: `{"x":1}`}}}
	if err := r.ToolResult(res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `{"x":1}`) {
		t.Fatalf("stdout = %q", out.String())
	}
	if errb.Len() != 0 {
		t.Fatalf("stderr must be empty, got %q", errb.String())
	}
}

func TestToolResultTextError(t *testing.T) {
	r, out, errb := newRenderer()
	res := &client.Result{IsError: true, Content: []client.Content{{Type: "text", Text: "boom"}}}
	if err := r.ToolResult(res); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("stdout must be empty, got %q", out.String())
	}
	if !strings.Contains(errb.String(), "error:") || !strings.Contains(errb.String(), "boom") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestToolResultEmptyError(t *testing.T) {
	r, _, errb := newRenderer()
	if err := r.ToolResult(&client.Result{IsError: true}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errb.String(), "error: (no details)") {
		t.Fatalf("stderr = %q", errb.String())
	}
}

func TestToolResultContentTypes(t *testing.T) {
	r, out, _ := newRenderer()
	res := &client.Result{Content: []client.Content{
		{Type: "image", MIMEType: "image/png"},
		{Type: "resource", ResourceText: "resource body"},
		{Type: "resource", MIMEType: "text/plain"},
		{Type: "custom"},
	}}
	if err := r.ToolResult(res); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	for _, want := range []string{"[image: image/png]", "resource body", "[resource: text/plain]", "[custom: unsupported content type]"} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in:\n%s", want, s)
		}
	}
}

func decode(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, s)
	}
}
