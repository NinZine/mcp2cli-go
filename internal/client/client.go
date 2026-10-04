// Package client is the only place that depends on the MCP protocol library.
// It adapts config.Server values into mcp-go clients and exposes a small,
// dependency-free surface (Conn, Tool, Result) to the rest of the program.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"

	"mcp/internal/config"
)

// Tool is the neutral representation of an MCP tool definition.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Content is one neutral content block from a tool result.
type Content struct {
	Type         string
	Text         string
	MIMEType     string
	ResourceText string
	// Raw is the JSON object form of the block, used for JSON output.
	Raw map[string]any
}

// Result is the neutral representation of a tool call result.
type Result struct {
	Content           []Content
	IsError           bool
	StructuredContent any
}

// Conn is an initialized connection to an MCP server.
type Conn struct {
	c      *client.Client
	stderr *syncBuffer
}

// Connect builds the appropriate transport for srv, starts it, and performs the
// MCP initialize handshake exactly once. The caller owns ctx and must call
// Close.
func Connect(ctx context.Context, srv *config.Server, clientVersion string) (*Conn, error) {
	conn := &Conn{}

	var c *client.Client
	var err error

	switch srv.Type {
	case config.TypeStdio:
		conn.stderr = &syncBuffer{}
		env := mergedEnv(srv.Env)
		c, err = client.NewStdioMCPClientWithOptions(
			srv.Command,
			env,
			srv.Args,
			transport.WithCommandStderrWriter(conn.stderr),
		)
	case config.TypeHTTP:
		if isSSE(srv.URL) {
			c, err = client.NewSSEMCPClient(srv.URL, client.WithHeaders(srv.Headers))
		} else {
			c, err = client.NewStreamableHttpClient(srv.URL, transport.WithHTTPHeaders(srv.Headers))
		}
	default:
		return nil, fmt.Errorf("unsupported server type %q", srv.Type)
	}
	if err != nil {
		return nil, err
	}
	conn.c = c

	if srv.Type == config.TypeHTTP {
		if err := c.Start(ctx); err != nil {
			_ = c.Close()
			return nil, conn.annotate(err)
		}
	}

	initReq := mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			Capabilities:    mcp.ClientCapabilities{},
			ClientInfo: mcp.Implementation{
				Name:    "mcp",
				Version: clientVersion,
			},
		},
	}
	if _, err := c.Initialize(ctx, initReq); err != nil {
		_ = c.Close()
		return nil, conn.annotate(err)
	}

	return conn, nil
}

// Close terminates the connection.
func (c *Conn) Close() error {
	if c.c == nil {
		return nil
	}
	return c.c.Close()
}

// ProtocolVersion returns the negotiated protocol version.
func (c *Conn) ProtocolVersion() string {
	if c.c == nil {
		return ""
	}
	return c.c.ProtocolVersion()
}

// ListTools returns all tools, following pagination.
func (c *Conn) ListTools(ctx context.Context) ([]Tool, error) {
	tools := []Tool{}
	for tool, err := range c.c.IterTools(ctx, mcp.ListToolsRequest{}) {
		if err != nil {
			return nil, c.annotate(err)
		}
		tools = append(tools, toTool(tool))
	}
	return tools, nil
}

// CallTool invokes a tool with the given arguments.
func (c *Conn) CallTool(ctx context.Context, name string, args map[string]any) (*Result, error) {
	res, err := c.c.CallTool(ctx, mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
	if err != nil {
		return nil, c.annotate(err)
	}
	return toResult(res), nil
}

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

func toResult(res *mcp.CallToolResult) *Result {
	out := &Result{
		Content:           []Content{},
		IsError:           res.IsError,
		StructuredContent: res.StructuredContent,
	}
	for _, block := range res.Content {
		raw := map[string]any{}
		if b, err := json.Marshal(block); err == nil {
			_ = json.Unmarshal(b, &raw)
		}
		c := Content{
			Type:     stringValue(raw["type"]),
			Text:     stringValue(raw["text"]),
			MIMEType: stringValue(raw["mimeType"]),
			Raw:      raw,
		}
		if resource, ok := raw["resource"].(map[string]any); ok {
			c.ResourceText = stringValue(resource["text"])
			if c.MIMEType == "" {
				c.MIMEType = stringValue(resource["mimeType"])
			}
		}
		out.Content = append(out.Content, c)
	}
	return out
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func isSSE(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Path == "" {
		return false
	}
	// Decide on the path component so a query string (e.g. /sse?token=x) does
	// not defeat detection; tolerate trailing slashes.
	path := strings.TrimRight(u.Path, "/")
	return strings.HasSuffix(path, "/sse")
}

// mergedEnv returns the parent environment with srv.Env applied as overrides.
// (The mcp-go stdio transport also inherits os.Environ, so overrides must come
// after it; duplicates resolve to the last entry.)
func mergedEnv(extra map[string]string) []string {
	merged := make(map[string]string, len(os.Environ())+len(extra))
	for _, kv := range os.Environ() {
		k, v, _ := strings.Cut(kv, "=")
		merged[k] = v
	}
	for k, v := range extra {
		merged[k] = v
	}
	out := make([]string, 0, len(merged))
	for k, v := range merged {
		out = append(out, k+"="+v)
	}
	return out
}

// annotate appends captured child stderr to err when available.
func (c *Conn) annotate(err error) error {
	if err == nil || c.stderr == nil {
		return err
	}
	if detail := strings.TrimSpace(c.stderr.String()); detail != "" {
		return fmt.Errorf("%w\nserver stderr:\n%s", err, detail)
	}
	return err
}

// syncBuffer is a concurrency-safe bytes.Buffer used to collect child stderr.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ensure io is referenced (kept for interface parity with future writers).
var _ io.Writer = (*syncBuffer)(nil)
