package client

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/NinZine/mcp2cli-go/internal/config"
)

// ---------------------------------------------------------------------------
// Improvement 2 — `--info` schema fidelity.
//
// The current implementation marshals mcp-go's fixed ToolArgumentsSchema,
// which drops unmodeled keywords and synthesizes `required: []` /
// `properties: {}`. The planned implementation reads `tools/list` from the raw
// transport and keeps `inputSchema` verbatim.
//
// These tests cover Conn.ListTools in two harnesses: a fake transport that
// returns a hand-written tools/list payload (full control over raw keywords,
// the absence of `required`/`properties`, the absence of `inputSchema`
// entirely, and tool order), and a real in-process Streamable HTTP mcp-go
// server on loopback. Both are hermetic: no external network, no child
// process.
// ---------------------------------------------------------------------------

// rawSchemaToolList deliberately:
//   - returns tools in a non-alphabetical order ("third", "first", "second")
//     so a client-side sort would be observable;
//   - gives the first tool a schema with `title`/`oneOf` and neither `required`
//     nor `properties`;
//   - omits `inputSchema` for the second tool (the "absent schema" case);
//   - includes a conventional schema for the third tool.
const rawSchemaToolList = `{
  "tools": [
    {
      "name": "third",
      "description": "raw schema",
      "inputSchema": {"title": "Root", "oneOf": [{"type": "object"}, {"type": "string"}]}
    },
    {
      "name": "first",
      "description": "no schema"
    },
    {
      "name": "second",
      "description": "normal",
      "inputSchema": {"type": "object", "properties": {"q": {"type": "string"}}, "required": ["q"]}
    }
  ]
}`

// fakeTransport is a minimal transport.Interface whose responses are supplied
// by the test.
type fakeTransport struct {
	toolListResult string
}

func (f *fakeTransport) Start(context.Context) error { return nil }

func (f *fakeTransport) SendRequest(_ context.Context, req transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	switch req.Method {
	case "initialize":
		return jsonRPCResult(req.ID, `{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"schemafake","version":"0.0.1"}}`), nil
	case "tools/list":
		return jsonRPCResult(req.ID, f.toolListResult), nil
	default:
		return &transport.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcp.JSONRPCErrorDetails{Code: -32601, Message: "method not found: " + req.Method},
		}, nil
	}
}

func (f *fakeTransport) SendNotification(context.Context, mcp.JSONRPCNotification) error { return nil }
func (f *fakeTransport) SetNotificationHandler(func(mcp.JSONRPCNotification))            {}
func (f *fakeTransport) Close() error                                                    { return nil }
func (f *fakeTransport) GetSessionId() string                                            { return "" }

func jsonRPCResult(id mcp.RequestId, result string) *transport.JSONRPCResponse {
	return &transport.JSONRPCResponse{JSONRPC: "2.0", ID: id, Result: json.RawMessage(result)}
}

// listToolsFromFake wires up a Conn over the fake transport, initializes it
// using a legacy protocol version (so the fake only has to answer `initialize`
// and `tools/list`), and calls the code under test.
func listToolsFromFake(t *testing.T, toolsResult string) []Tool {
	t.Helper()

	ft := &fakeTransport{toolListResult: toolsResult}
	mc := client.NewClient(ft)

	ctx := context.Background()
	if err := mc.Start(ctx); err != nil {
		t.Fatalf("start fake client: %v", err)
	}
	if _, err := mc.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_LEGACY_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "schema-test", Version: "0"},
		},
	}); err != nil {
		t.Fatalf("initialize fake client: %v", err)
	}

	conn := &Conn{c: mc}
	tools, err := conn.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	return tools
}

// The raw `inputSchema` must be returned exactly as the server sent it:
// `title` and `oneOf` survive and neither `required` nor `properties` is
// synthesized.
func TestListToolsPreservesRawInputSchema(t *testing.T) {
	tools := listToolsFromFake(t, rawSchemaToolList)
	if len(tools) != 3 {
		t.Fatalf("want 3 tools, got %d", len(tools))
	}

	got := tools[0].InputSchema
	want := map[string]any{
		"title": "Root",
		"oneOf": []any{
			map[string]any{"type": "object"},
			map[string]any{"type": "string"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("inputSchema altered by the client:\n got: %#v\nwant: %#v", got, want)
	}
	if _, ok := got["required"]; ok {
		t.Errorf("client synthesized `required`: %#v", got)
	}
	if _, ok := got["properties"]; ok {
		t.Errorf("client synthesized `properties`: %#v", got)
	}
}

// A conventional schema must also pass through unchanged.
func TestListToolsPreservesConventionalSchema(t *testing.T) {
	tools := listToolsFromFake(t, rawSchemaToolList)
	got := tools[2].InputSchema
	want := map[string]any{
		"type":       "object",
		"properties": map[string]any{"q": map[string]any{"type": "string"}},
		"required":   []any{"q"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conventional inputSchema altered:\n got: %#v\nwant: %#v", got, want)
	}
}

// Tool order must be the server's order, not sorted.
func TestListToolsPreservesServerOrder(t *testing.T) {
	tools := listToolsFromFake(t, rawSchemaToolList)
	got := make([]string, 0, len(tools))
	for _, tool := range tools {
		got = append(got, tool.Name)
	}
	want := []string{"third", "first", "second"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tool order = %v, want server order %v", got, want)
	}
}

// Decision (documented): an absent `inputSchema` is surfaced as an empty JSON
// object ({}), never null. The renderer additionally coerces nil to {} for the
// JSON output, so a tool with no declared schema always renders
// `"inputSchema": {}` consistently.
func TestListToolsAbsentInputSchemaIsEmptyObject(t *testing.T) {
	tools := listToolsFromFake(t, rawSchemaToolList)
	got := tools[1].InputSchema
	if got == nil {
		t.Fatal("absent inputSchema must be a non-nil empty object, got nil")
	}
	if len(got) != 0 {
		t.Fatalf("absent inputSchema = %#v, want empty object", got)
	}
}

// TestListToolsRawSchemaOverHTTP is the end-to-end version of the fidelity
// test: a real in-process Streamable HTTP mcp-go server (started on loopback
// via httptest, no helper binary) registers a tool with the exact raw schema,
// and Conn.ListTools must return it unchanged.
func TestListToolsRawSchemaOverHTTP(t *testing.T) {
	mcpServer := server.NewMCPServer(
		"schemahttp",
		"0.0.1",
		server.WithToolCapabilities(false),
	)
	mcpServer.AddTool(
		mcp.NewToolWithRawSchema("root_schema", "Schema with title and oneOf", json.RawMessage(rawInputSchemaJSON)),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("ok"), nil
		},
	)
	ts := httptest.NewServer(server.NewStreamableHTTPServer(mcpServer,
		server.WithDisableLocalhostProtection(true),
	))
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := Connect(ctx, &config.Server{
		Name: "schemahttp",
		Type: config.TypeHTTP,
		URL:  ts.URL + "/mcp",
	}, "test")
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	tools, err := conn.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "root_schema" {
		t.Fatalf("unexpected tools: %#v", tools)
	}

	want := map[string]any{
		"title": "Root",
		"oneOf": []any{
			map[string]any{"type": "object"},
			map[string]any{"type": "string"},
		},
	}
	if got := tools[0].InputSchema; !reflect.DeepEqual(got, want) {
		t.Fatalf("inputSchema altered over HTTP:\n got: %#v\nwant: %#v", got, want)
	}
}

const rawInputSchemaJSON = `{"title":"Root","oneOf":[{"type":"object"},{"type":"string"}]}`
