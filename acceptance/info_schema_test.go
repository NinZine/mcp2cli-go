package acceptance_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// rawInputSchema is the schema described by the improvements plan: it has a
// root `title` and `oneOf`, and deliberately omits both `required` and
// `properties`. mcp-go's fixed ToolArgumentsSchema cannot represent it, so the
// current `--info` output synthesizes `required: []` / `properties: {}` and
// drops `title`/`oneOf`.
const rawInputSchema = `{"title":"Root","oneOf":[{"type":"object"},{"type":"string"}]}`

// newRawSchemaHTTPServer starts an in-process Streamable HTTP MCP server that
// exposes one tool with the raw input schema above. It is hermetic (httptest
// binds to loopback) and needs no helper binary.
func newRawSchemaHTTPServer() *httptest.Server {
	mcpServer := server.NewMCPServer(
		"schemaserver",
		"0.0.1",
		server.WithToolCapabilities(false),
	)
	// NewToolWithRawSchema leaves the structured InputSchema empty; using
	// NewTool + WithRawInputSchema would set both and mcp-go refuses to
	// marshal such a tool.
	mcpServer.AddTool(
		mcp.NewToolWithRawSchema("root_schema", "Schema with title and oneOf", json.RawMessage(rawInputSchema)),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)
	streamable := server.NewStreamableHTTPServer(mcpServer,
		server.WithDisableLocalhostProtection(true),
	)
	return httptest.NewServer(streamable)
}

// ---------------------------------------------------------------------------
// Improvement 2 — `--info` must render the server's inputSchema verbatim.
// ---------------------------------------------------------------------------

func TestCLI_InfoSchemaVerbatim(t *testing.T) {
	ts := newRawSchemaHTTPServer()
	defer ts.Close()

	cfg := configWithServers(map[string]string{
		"remote": `{"url":"` + ts.URL + `/mcp"}`,
	})

	r := run(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "--json", "remote", "--info")
	mustExit(t, r, 0)

	var tools []map[string]any
	decodeJSON(t, r.Stdout, &tools)

	var found map[string]any
	for _, tool := range tools {
		if tool["name"] == "root_schema" {
			found = tool
		}
	}
	if found == nil {
		t.Fatalf("root_schema not found in --info output: %s", r.Stdout)
	}

	schema, ok := found["inputSchema"].(map[string]any)
	if !ok {
		t.Fatalf("inputSchema missing/not an object: %v", found["inputSchema"])
	}

	want := map[string]any{
		"title": "Root",
		"oneOf": []any{
			map[string]any{"type": "object"},
			map[string]any{"type": "string"},
		},
	}
	if !reflect.DeepEqual(schema, want) {
		t.Fatalf("--info altered the inputSchema:\n got: %#v\nwant: %#v\nraw output:\n%s",
			schema, want, r.Stdout)
	}
	if _, ok := schema["required"]; ok {
		t.Errorf("--info synthesized `required`: %#v", schema)
	}
	if _, ok := schema["properties"]; ok {
		t.Errorf("--info synthesized `properties`: %#v", schema)
	}
}

// ---------------------------------------------------------------------------
// Improvement 3 — `--info` text footer.
//
// ToolList prints a blank line + "N tool(s) available"; ToolInfo must do the
// same. The `(none)` line for tools without parameters is retained.
// ---------------------------------------------------------------------------

func TestCLI_InfoTextFooter(t *testing.T) {
	cfg := testServerConfig("fake")
	r := runPTY(t, runOpts{Env: baseEnv(inlineConfig(cfg))}, "fake", "--info")
	mustExit(t, r, 0)

	// Normalize PTY newline translation before matching a blank line.
	stdout := strings.ReplaceAll(r.Stdout, "\r\n", "\n")
	mustContain(t, stdout, "\n\n3 tool(s) available", "info footer")
	mustContain(t, stdout, "(none)", "no-parameter marker")
}
