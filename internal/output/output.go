// Package output renders command data as JSON or human-readable text per the
// specification. JSON is pretty-printed with two-space indentation; empty
// collections render as [] rather than null.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"mcp/internal/client"
)

// Renderer writes either JSON or text depending on JSON.
type Renderer struct {
	JSON bool
	Out  io.Writer
	Err  io.Writer
}

// ServerEntry is one row of `mcp --list`.
type ServerEntry struct {
	Name              string
	Type              string
	URL               string
	Command           string
	Args              []string
	Available         *bool
	UnavailableReason string
}

type toolListItem struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type toolInfoItem struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type healthItem struct {
	Server          string `json:"server"`
	Status          string `json:"status"`
	ProtocolVersion string `json:"protocolVersion"`
}

type configPathItem struct {
	Path   string `json:"path"`
	Exists bool   `json:"exists"`
	Dir    string `json:"dir"`
}

// ServerList renders `mcp --list`.
func (r *Renderer) ServerList(entries []ServerEntry) error {
	if r.JSON {
		out := make([]map[string]any, 0, len(entries))
		for _, e := range entries {
			m := map[string]any{"name": e.Name, "type": e.Type}
			switch e.Type {
			case "http":
				m["url"] = e.URL
			default:
				args := e.Args
				if args == nil {
					args = []string{}
				}
				m["command"] = e.Command
				m["args"] = args
			}
			if e.Available != nil && !*e.Available {
				m["available"] = false
				m["unavailable_reason"] = e.UnavailableReason
			}
			out = append(out, m)
		}
		return r.writeJSON(out)
	}

	showStatus := false
	for _, e := range entries {
		if e.Available != nil && !*e.Available {
			showStatus = true
		}
	}

	headers := []string{"Server", "Type", "Endpoint"}
	if showStatus {
		headers = append(headers, "Status")
	}
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		endpoint := e.URL
		if e.Type != "http" {
			endpoint = strings.TrimSpace(e.Command + " " + strings.Join(e.Args, " "))
		}
		row := []string{e.Name, e.Type, endpoint}
		if showStatus {
			status := ""
			if e.Available != nil && !*e.Available {
				status = e.UnavailableReason
			}
			row = append(row, status)
		}
		rows = append(rows, row)
	}
	r.writeTable(headers, rows)
	fmt.Fprintln(r.Out)
	fmt.Fprintf(r.Out, "%d server(s) configured\n", len(entries))
	return nil
}

// ToolList renders `mcp <server> --list`.
func (r *Renderer) ToolList(tools []client.Tool) error {
	if r.JSON {
		out := make([]toolListItem, 0, len(tools))
		for _, t := range tools {
			out = append(out, toolListItem{Name: t.Name, Description: t.Description})
		}
		return r.writeJSON(out)
	}

	rows := make([][]string, 0, len(tools))
	for _, t := range tools {
		rows = append(rows, []string{t.Name, t.Description})
	}
	r.writeTable([]string{"Tool", "Description"}, rows)
	fmt.Fprintln(r.Out)
	fmt.Fprintf(r.Out, "%d tool(s) available\n", len(tools))
	return nil
}

// ToolInfo renders `mcp <server> --info`.
func (r *Renderer) ToolInfo(tools []client.Tool) error {
	if r.JSON {
		out := make([]toolInfoItem, 0, len(tools))
		for _, t := range tools {
			schema := t.InputSchema
			if schema == nil {
				schema = map[string]any{}
			}
			out = append(out, toolInfoItem{Name: t.Name, Description: t.Description, InputSchema: schema})
		}
		return r.writeJSON(out)
	}

	for i, t := range tools {
		if i > 0 {
			fmt.Fprintln(r.Out)
		}
		fmt.Fprintln(r.Out, t.Name)
		if t.Description != "" {
			fmt.Fprintf(r.Out, "  %s\n", t.Description)
		}
		fmt.Fprintln(r.Out, "  Parameters:")
		params := schemaParameters(t.InputSchema)
		if len(params) == 0 {
			fmt.Fprintln(r.Out, "    (none)")
			continue
		}
		for _, p := range params {
			suffix := ""
			if p.Required {
				suffix = " (required)"
			}
			desc := p.Description
			line := strings.TrimSpace(p.Name + " " + p.Type)
			if desc != "" {
				line += " — " + desc
			}
			fmt.Fprintf(r.Out, "    %s%s\n", line, suffix)
		}
	}
	fmt.Fprintln(r.Out)
	fmt.Fprintf(r.Out, "%d tool(s) available\n", len(tools))
	return nil
}

// Health renders `mcp <server> --health`.
func (r *Renderer) Health(server, protocolVersion string) error {
	if r.JSON {
		return r.writeJSON(healthItem{
			Server:          server,
			Status:          "ok",
			ProtocolVersion: protocolVersion,
		})
	}
	fmt.Fprintf(r.Out, "%s: ok\n", server)
	return nil
}

// ConfigPath renders `mcp config path`.
func (r *Renderer) ConfigPath(path string, exists bool, dir string) error {
	if r.JSON {
		return r.writeJSON(configPathItem{Path: path, Exists: exists, Dir: dir})
	}
	fmt.Fprintln(r.Out, path)
	return nil
}

// ToolResult renders `mcp <server> <tool>`.
func (r *Renderer) ToolResult(res *client.Result) error {
	if r.JSON {
		content := make([]any, 0, len(res.Content))
		for _, c := range res.Content {
			if c.Raw != nil {
				content = append(content, c.Raw)
			}
		}
		m := map[string]any{
			"content": content,
			"isError": res.IsError,
		}
		if res.StructuredContent != nil {
			m["structuredContent"] = res.StructuredContent
		}
		return r.writeJSON(m)
	}

	if res.IsError && len(res.Content) == 0 {
		fmt.Fprintln(r.Err, "error: (no details)")
		return nil
	}

	for _, c := range res.Content {
		switch c.Type {
		case "text":
			if res.IsError {
				fmt.Fprintf(r.Err, "error: %s\n", c.Text)
			} else {
				fmt.Fprintln(r.Out, c.Text)
			}
		case "image":
			fmt.Fprintf(r.Out, "[image: %s]\n", c.MIMEType)
		case "resource":
			if c.ResourceText != "" {
				fmt.Fprintln(r.Out, c.ResourceText)
			} else {
				fmt.Fprintf(r.Out, "[resource: %s]\n", c.MIMEType)
			}
		default:
			if c.Text != "" {
				fmt.Fprintln(r.Out, c.Text)
			} else {
				fmt.Fprintf(r.Out, "[%s: unsupported content type]\n", c.Type)
			}
		}
	}
	return nil
}

func (r *Renderer) writeJSON(v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if _, err := r.Out.Write(b); err != nil {
		return err
	}
	_, err = io.WriteString(r.Out, "\n")
	return err
}

// writeTable prints a left-aligned, space-padded table.
func (r *Renderer) writeTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i < len(widths) && len(cell) > widths[i] {
				widths[i] = len(cell)
			}
		}
	}
	writeRow := func(cells []string) {
		var b strings.Builder
		for i, cell := range cells {
			b.WriteString(cell)
			if i < len(cells)-1 {
				pad := widths[i] - len(cell) + 2
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
		fmt.Fprintln(r.Out, b.String())
	}
	writeRow(headers)
	for _, row := range rows {
		writeRow(row)
	}
}

// parameter is a flattened input-schema property.
type parameter struct {
	Name        string
	Type        string
	Description string
	Required    bool
}

func schemaParameters(schema map[string]any) []parameter {
	if schema == nil {
		return nil
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil
	}
	required := map[string]bool{}
	if req, ok := schema["required"].([]any); ok {
		for _, r := range req {
			if s, ok := r.(string); ok {
				required[s] = true
			}
		}
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	// Stable ordering for deterministic text output.
	sortStrings(names)

	params := make([]parameter, 0, len(names))
	for _, name := range names {
		p := parameter{Name: name, Required: required[name]}
		if def, ok := props[name].(map[string]any); ok {
			p.Type = typeString(def["type"])
			p.Description = stringField(def["description"])
		}
		params = append(params, p)
	}
	return params
}

func typeString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "|")
	}
	return ""
}

func stringField(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j] < xs[j-1]; j-- {
			xs[j], xs[j-1] = xs[j-1], xs[j]
		}
	}
}
