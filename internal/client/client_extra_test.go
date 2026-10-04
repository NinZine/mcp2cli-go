package client

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// toTool converts a typed mcp.Tool via mcp-go's fixed ToolArgumentsSchema. It
// lives only in the tests: ListTools no longer uses it because that conversion
// is lossy (it drops schema keywords and synthesizes required/properties).
func toTool(t mcp.Tool) Tool {
	schema := map[string]any{}
	if b, err := json.Marshal(t.InputSchema); err == nil {
		_ = json.Unmarshal(b, &schema)
	}
	return Tool{
		Name:        t.Name,
		Description: t.Description,
		InputSchema: schema,
	}
}

// SPEC §8.1 — SSE is selected when the URL path ends in /sse, otherwise
// Streamable HTTP is used.
func TestIsSSE(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"https://mcp.sentry.dev/sse", true},
		{"https://mcp.sentry.dev/sse/", true},
		{"https://host/base/sse", true},
		{"https://host/mcp", false},
		{"https://host/notsse", false},
		{"https://host/sse-extra", false},
		{"https://host/SSE", false},
	}
	for _, tc := range cases {
		if got := isSSE(tc.url); got != tc.want {
			t.Errorf("isSSE(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// SPEC §8.1 — SSE detection must use the URL path component, not the raw
// string: a query string or trailing slash must not defeat it, and a URL with
// no scheme/path is never SSE.
func TestIsSSEPathComponent(t *testing.T) {
	cases := []struct {
		url  string
		want bool
	}{
		{"http://h/sse", true},
		{"http://h/sse?token=x", true},
		{"http://h/sse/", true},
		{"http://h/mcp", false},
		{"http://h/sses", false},
		{"/sse", false},
		{"http://h", false},
	}
	for _, tc := range cases {
		if got := isSSE(tc.url); got != tc.want {
			t.Errorf("isSSE(%q) = %v, want %v", tc.url, got, tc.want)
		}
	}
}

// SPEC §8.1 — server env overrides win over the parent environment, and the
// parent environment is still inherited.
func TestMergedEnvOverridesAndInherits(t *testing.T) {
	t.Setenv("MCP_CLIENT_TEST_INHERIT", "parent-value")
	t.Setenv("MCP_CLIENT_TEST_OVERRIDE", "parent")

	env := envMap(mergedEnv(map[string]string{
		"MCP_CLIENT_TEST_OVERRIDE": "child",
		"MCP_CLIENT_TEST_EXTRA":    "extra",
	}))

	if env["MCP_CLIENT_TEST_INHERIT"] != "parent-value" {
		t.Errorf("parent env not inherited: %q", env["MCP_CLIENT_TEST_INHERIT"])
	}
	if env["MCP_CLIENT_TEST_OVERRIDE"] != "child" {
		t.Errorf("server override lost: %q", env["MCP_CLIENT_TEST_OVERRIDE"])
	}
	if env["MCP_CLIENT_TEST_EXTRA"] != "extra" {
		t.Errorf("server extra env missing: %q", env["MCP_CLIENT_TEST_EXTRA"])
	}
}

func envMap(kvs []string) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

// SPEC §9.5 — the neutral conversion must surface type, text, MIME type, and
// embedded-resource text so the renderer can implement the output rules.
func TestToResultContentConversion(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Type: mcp.ContentTypeText, Text: "hello"},
			mcp.ImageContent{Type: mcp.ContentTypeImage, Data: "AAAA", MIMEType: "image/png"},
			mcp.EmbeddedResource{
				Type: mcp.ContentTypeResource,
				Resource: mcp.TextResourceContents{
					URI:      "file:///x",
					MIMEType: "text/plain",
					Text:     "resource body",
				},
			},
			mcp.EmbeddedResource{
				Type: mcp.ContentTypeResource,
				Resource: mcp.BlobResourceContents{
					URI:      "file:///bin",
					MIMEType: "application/octet-stream",
					Blob:     "AQID",
				},
			},
		},
	}

	got := toResult(res)
	if got.IsError {
		t.Fatal("IsError should be false")
	}
	if len(got.Content) != 4 {
		t.Fatalf("want 4 content blocks, got %d", len(got.Content))
	}

	if c := got.Content[0]; c.Type != "text" || c.Text != "hello" {
		t.Errorf("text block = %+v", c)
	}
	if c := got.Content[1]; c.Type != "image" || c.MIMEType != "image/png" {
		t.Errorf("image block = %+v", c)
	}
	if c := got.Content[2]; c.Type != "resource" || c.ResourceText != "resource body" || c.MIMEType != "text/plain" {
		t.Errorf("resource block = %+v", c)
	}
	if c := got.Content[3]; c.Type != "resource" || c.ResourceText != "" || c.MIMEType != "application/octet-stream" {
		t.Errorf("blob resource block = %+v", c)
	}
}

// SPEC §9.5 — an isError result with no content must remain empty so the
// renderer can emit `error: (no details)`.
func TestToResultEmptyError(t *testing.T) {
	got := toResult(&mcp.CallToolResult{IsError: true})
	if !got.IsError {
		t.Fatal("IsError should be true")
	}
	if len(got.Content) != 0 {
		t.Fatalf("want no content, got %d", len(got.Content))
	}
}

// SPEC §9.3 — tool input schemas must survive conversion for --info.
func TestToToolSchemaPreserved(t *testing.T) {
	tool := mcp.NewTool("search_issues",
		mcp.WithDescription("Search for issues"),
		mcp.WithString("query", mcp.Required(), mcp.Description("The search query")),
	)

	got := toTool(tool)
	if got.Name != "search_issues" || got.Description != "Search for issues" {
		t.Fatalf("tool = %+v", got)
	}
	props, ok := got.InputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema.properties missing: %#v", got.InputSchema)
	}
	q, ok := props["query"].(map[string]any)
	if !ok {
		t.Fatalf("query property missing: %#v", props)
	}
	if q["type"] != "string" {
		t.Errorf("query type = %v, want string", q["type"])
	}
	req, _ := got.InputSchema["required"].([]any)
	found := false
	for _, r := range req {
		if r == "query" {
			found = true
		}
	}
	if !found {
		t.Errorf("query not required: %#v", got.InputSchema["required"])
	}
}

// SPEC §9.5 — a resource with no text must still expose its MIME type from the
// nested resource object (`[resource: <mimeType>]`).
func TestToResultResourceMIMEFallback(t *testing.T) {
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.EmbeddedResource{
				Type: mcp.ContentTypeResource,
				Resource: mcp.BlobResourceContents{
					URI:      "file:///x",
					MIMEType: "application/pdf",
					Blob:     "AQID",
				},
			},
		},
	}
	got := toResult(res)
	if got.Content[0].ResourceText != "" {
		t.Fatalf("ResourceText = %q, want empty", got.Content[0].ResourceText)
	}
	if got.Content[0].MIMEType != "application/pdf" {
		t.Fatalf("MIMEType = %q, want application/pdf", got.Content[0].MIMEType)
	}
}
