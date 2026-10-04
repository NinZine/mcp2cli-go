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
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"

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

	// clientVersion is the version reported to the server during Initialize.
	// It is embedded in the per-request _meta for modern connections.
	clientVersion string
	// requestID is an atomic counter for raw JSON-RPC requests. It is an
	// atomic.Int64 (rather than a bare int64 used with atomic.AddInt64) so the
	// counter is always 64-bit aligned, including on 32-bit platforms.
	requestID atomic.Int64
}

// Connect builds the appropriate transport for srv, starts it, and performs the
// MCP initialize handshake exactly once. The caller owns ctx and must call
// Close.
func Connect(ctx context.Context, srv *config.Server, clientVersion string) (*Conn, error) {
	conn := &Conn{clientVersion: clientVersion}

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

// ListTools returns all tools, following pagination, with each tool's
// inputSchema preserved exactly as the server sent it. It fetches tools/list
// from the raw transport rather than the typed API, because the typed API
// marshals mcp-go's fixed ToolArgumentsSchema and silently drops schema
// keywords (title, oneOf, $ref, ...) and synthesizes required/properties.
//
// This path deliberately bypasses the typed client's c.rememberTools and the
// x-mcp-header/param-cache machinery: the CLI lists and calls on separate
// connections, so there is never a cached tool definition to derive Mcp-Param-*
// headers from, and the lossy typed schema would discard the headers' source
// annotations anyway. The hand-built _meta below mirrors mcp-go's
// applyRequestMeta (protocol version, client capabilities, client info) minus
// the log-level field and the handler-derived capabilities, neither of which
// this CLI configures.
func (c *Conn) ListTools(ctx context.Context) ([]Tool, error) {
	out := []Tool{}
	cursor := ""
	// seenCursors guards against a misbehaving server that returns a constant
	// (or cyclic) nextCursor: without it, pagination would spin until the
	// context deadline. On a repeat, stop and return what has been gathered.
	seenCursors := map[string]bool{}
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}

		protocolVersion := ""
		if c.c != nil {
			protocolVersion = c.c.ProtocolVersion()
		}
		// The stateless protocol core requires the protocol version, client
		// capabilities, and client identity on every request. A partial _meta
		// is rejected, so include all three (or none, for legacy connections).
		if mcp.IsModernProtocol(protocolVersion) {
			params["_meta"] = map[string]any{
				mcp.MetaKeyProtocolVersion:    protocolVersion,
				mcp.MetaKeyClientCapabilities: map[string]any{},
				mcp.MetaKeyClientInfo: map[string]any{
					"name":    "mcp",
					"version": c.clientVersion,
				},
			}
		}

		var header http.Header
		if pj, err := json.Marshal(params); err == nil {
			for k, v := range mcp.StandardHeaders(protocolVersion, mcp.MethodToolsList, pj) {
				if header == nil {
					header = http.Header{}
				}
				header.Set(k, v)
			}
		}

		resp, err := c.c.GetTransport().SendRequest(ctx, transport.JSONRPCRequest{
			JSONRPC: mcp.JSONRPC_VERSION,
			ID:      mcp.NewRequestId(c.requestID.Add(1)),
			Method:  string(mcp.MethodToolsList),
			Params:  params,
			Header:  header,
		})
		if err != nil {
			return nil, c.annotate(err)
		}
		if resp.Error != nil {
			return nil, c.annotate(fmt.Errorf("tools/list: %s", resp.Error.Message))
		}

		var result struct {
			Tools []struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if err := json.Unmarshal(resp.Result, &result); err != nil {
			return nil, c.annotate(fmt.Errorf("parsing tools/list: %w", err))
		}

		for _, t := range result.Tools {
			schema := map[string]any{}
			if len(t.InputSchema) > 0 {
				if err := json.Unmarshal(t.InputSchema, &schema); err != nil {
					return nil, c.annotate(fmt.Errorf("parsing inputSchema for %s: %w", t.Name, err))
				}
				if schema == nil {
					schema = map[string]any{}
				}
			}
			out = append(out, Tool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: schema,
			})
		}

		if result.NextCursor == "" {
			return out, nil
		}
		if seenCursors[result.NextCursor] {
			return out, nil
		}
		seenCursors[result.NextCursor] = true
		cursor = result.NextCursor
	}
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
