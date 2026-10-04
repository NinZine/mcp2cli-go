package acceptance_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// headerRecorder wraps an http.Handler and records the headers of every
// incoming request so tests can assert that config headers were sent.
type headerRecorder struct {
	next    http.Handler
	mu      sync.Mutex
	headers []http.Header
}

func (h *headerRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	h.headers = append(h.headers, r.Header.Clone())
	h.mu.Unlock()
	h.next.ServeHTTP(w, r)
}

// recorded returns a copy of all recorded headers.
func (h *headerRecorder) recorded() []http.Header {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]http.Header, len(h.headers))
	copy(out, h.headers)
	return out
}

// sawHeader reports whether any recorded request carried key: value.
func (h *headerRecorder) sawHeader(key, value string) bool {
	for _, hdr := range h.recorded() {
		if hdr.Get(key) == value {
			return true
		}
	}
	return false
}

// newHTTPTestServer starts an in-process Streamable HTTP MCP server exposing
// the same tools as the stdio test server, wrapped in a header recorder.
// It returns the httptest.Server and the recorder. The caller must Close it.
func newHTTPTestServer() (*httptest.Server, *headerRecorder) {
	mcpServer := server.NewMCPServer(
		"httptestserver",
		"0.0.1",
		server.WithToolCapabilities(false),
	)
	mcpServer.AddTool(
		mcp.NewTool("search_issues",
			mcp.WithDescription("Search for issues"),
			mcp.WithString("query", mcp.Required()),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText("Found 23 issues."), nil
		},
	)
	mcpServer.AddTool(
		mcp.NewTool("echo", mcp.WithDescription("Echo the arguments back as JSON")),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultText(`{"x":1}`), nil
		},
	)

	streamable := server.NewStreamableHTTPServer(mcpServer,
		server.WithDisableLocalhostProtection(true),
	)

	rec := &headerRecorder{}
	rec.next = streamable

	ts := httptest.NewServer(rec)
	return ts, rec
}
