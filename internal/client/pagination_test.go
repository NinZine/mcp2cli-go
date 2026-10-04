package client

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

// ---------------------------------------------------------------------------
// Pagination robustness (regression).
//
// Conn.ListTools follows nextCursor until it is empty. A misbehaving server
// that keeps returning the same non-empty cursor must not make the client spin
// until the context deadline; the seen-cursor guard stops pagination and
// returns the tools gathered so far.
// ---------------------------------------------------------------------------

// loopingTransport answers initialize, then always returns the same non-empty
// nextCursor. Tools are returned only on the first page so the result is
// deterministic: the first page's tool, and nothing after the loop is detected.
type loopingTransport struct {
	mu       sync.Mutex
	requests int
}

func (l *loopingTransport) Start(context.Context) error { return nil }

func (l *loopingTransport) SendRequest(ctx context.Context, req transport.JSONRPCRequest) (*transport.JSONRPCResponse, error) {
	// Honor cancellation so an unfixed implementation fails at the deadline
	// instead of hanging the test forever.
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	switch req.Method {
	case "initialize":
		return jsonRPCResult(req.ID, `{"protocolVersion":"2025-11-25","capabilities":{"tools":{}},"serverInfo":{"name":"looping","version":"0.0.1"}}`), nil
	case "tools/list":
		l.mu.Lock()
		l.requests++
		l.mu.Unlock()

		params, _ := req.Params.(map[string]any)
		cursor, _ := params["cursor"].(string)
		if cursor == "" {
			return jsonRPCResult(req.ID, `{"tools":[{"name":"first","description":"page one","inputSchema":{"type":"object"}}],"nextCursor":"loop"}`), nil
		}
		return jsonRPCResult(req.ID, `{"tools":[],"nextCursor":"loop"}`), nil
	default:
		return &transport.JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &mcp.JSONRPCErrorDetails{Code: -32601, Message: "method not found: " + req.Method},
		}, nil
	}
}

func (l *loopingTransport) SendNotification(context.Context, mcp.JSONRPCNotification) error {
	return nil
}
func (l *loopingTransport) SetNotificationHandler(func(mcp.JSONRPCNotification)) {}
func (l *loopingTransport) Close() error                                         { return nil }
func (l *loopingTransport) GetSessionId() string                                 { return "" }

func (l *loopingTransport) requestCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.requests
}

// A server that keeps returning the same nextCursor must not make ListTools
// loop until the deadline. The tools gathered before the repeated cursor is
// detected are returned, and no error is reported.
func TestListToolsStopsOnRepeatedCursor(t *testing.T) {
	ft := &loopingTransport{}
	mc := client.NewClient(ft)

	// A short deadline keeps the test fast; the fix returns in milliseconds.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := mc.Start(ctx); err != nil {
		t.Fatalf("start fake client: %v", err)
	}
	if _, err := mc.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_LEGACY_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "pagination-test", Version: "0"},
		},
	}); err != nil {
		t.Fatalf("initialize fake client: %v", err)
	}

	conn := &Conn{c: mc}

	start := time.Now()
	tools, err := conn.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools returned an error on a looping server: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Fatalf("ListTools took %v; the cursor guard did not bound pagination", elapsed)
	}

	// The tools from the first page must survive.
	if len(tools) != 1 || tools[0].Name != "first" {
		t.Fatalf("want the first page's single tool, got %#v", tools)
	}

	// Bounded number of requests: the loop must be detected quickly, not spin
	// until the deadline (which would be thousands of requests).
	if got := ft.requestCount(); got > 3 {
		t.Fatalf("tools/list requested %d times, want a bounded number (<=3)", got)
	}
}
