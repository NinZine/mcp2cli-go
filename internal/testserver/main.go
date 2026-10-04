// Command testserver is a fake MCP server used only by the black-box
// acceptance tests. It speaks MCP over stdio and exposes a small, fixed set
// of tools so the CLI's output can be asserted deterministically.
//
// It is intentionally a separate `package main` so it can be built into a
// standalone binary at test time and spawned by the CLI under test.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	s := server.NewMCPServer(
		"testserver",
		"0.0.1",
		server.WithToolCapabilities(false),
	)

	// search_issues: description "Search for issues", requires a string query.
	s.AddTool(
		mcp.NewTool("search_issues",
			mcp.WithDescription("Search for issues"),
			mcp.WithString("query",
				mcp.Required(),
				mcp.Description("The search query"),
			),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			query, _ := req.GetArguments()["query"].(string)
			return mcp.NewToolResultText(fmt.Sprintf("Found 23 issues for %q.", query)), nil
		},
	)

	// echo: returns its arguments as deterministic JSON text so a call with
	// {"x":1} renders predictably.
	s.AddTool(
		mcp.NewTool("echo",
			mcp.WithDescription("Echo the arguments back as JSON"),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := req.GetArguments()
			if args == nil {
				args = map[string]any{}
			}
			b, err := json.Marshal(args)
			if err != nil {
				return mcp.NewToolResultErrorFromErr("marshal arguments", err), nil
			}
			return mcp.NewToolResultText(string(b)), nil
		},
	)

	// fail: returns isError:true with text, to exercise the error-content path.
	s.AddTool(
		mcp.NewTool("fail",
			mcp.WithDescription("Always fails with an error result"),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return mcp.NewToolResultError("boom"), nil
		},
	)

	if err := server.ServeStdio(s); err != nil {
		fmt.Fprintln(os.Stderr, "testserver:", err)
		os.Exit(1)
	}
}
