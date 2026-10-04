// Package config loads and validates the mcp server configuration document.
//
// It owns the resolution order for the config location, environment-variable
// substitution, string-aware comment stripping, and server type inference.
// It has no dependency on the MCP protocol library.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
)

// Type is the transport type inferred for a server entry.
type Type string

const (
	// TypeStdio is a server spawned as a child process.
	TypeStdio Type = "stdio"
	// TypeHTTP is a server reached over Streamable HTTP (or SSE).
	TypeHTTP Type = "http"
)

// Server is one validated entry from mcpServers.
type Server struct {
	Name string
	Type Type

	// stdio
	Command string
	Args    []string
	Env     map[string]string

	// http
	URL     string
	Headers map[string]string
}

// Set is the ordered, validated collection of servers plus any non-fatal
// warnings gathered while loading.
type Set struct {
	Servers  []*Server
	Warnings []string
}

// reservedNames are server names that would collide with commands (present or
// future). They are warned about and skipped.
var reservedNames = map[string]bool{
	"search": true, "add": true, "remove": true, "update": true,
	"list": true, "help": true, "version": true, "serve": true,
	"logs": true, "acl": true, "config": true, "completions": true,
	"healthcheck": true,
}

var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Substitute replaces every ${VAR} with getenv(VAR), or the empty string when
// the variable is unset.
//
// Substitution runs over raw JSON text before parsing (SPEC §6.2), so the
// replacement is JSON-escaped: a placeholder only appears inside a JSON string
// value, and escaping keeps quotes, backslashes, and control characters from
// altering the document's structure. The resulting value round-trips to the
// exact environment string after json.Unmarshal.
func Substitute(s string, getenv func(string) string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		name := match[2 : len(match)-1]
		val := getenv(name)
		b, err := json.Marshal(val)
		if err != nil {
			return ""
		}
		// Drop the surrounding quotes: the value is now a safe JSON string
		// fragment that sits inside the existing string literal.
		return string(b[1 : len(b)-1])
	})
}

// StripComments removes // line comments and /* ... */ block comments while
// preserving those sequences inside JSON string literals. Newlines inside
// removed comments are preserved so the surrounding JSON remains well-formed.
func StripComments(s string) string {
	const (
		stateNormal = iota
		stateString
		stateLineComment
		stateBlockComment
	)

	var b []byte
	state := stateNormal
	escaped := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch state {
		case stateNormal:
			switch {
			case ch == '"':
				state = stateString
				b = append(b, ch)
			case ch == '/' && i+1 < len(s) && s[i+1] == '/':
				state = stateLineComment
				i++
			case ch == '/' && i+1 < len(s) && s[i+1] == '*':
				state = stateBlockComment
				i++
			default:
				b = append(b, ch)
			}
		case stateString:
			b = append(b, ch)
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				state = stateNormal
			}
		case stateLineComment:
			if ch == '\n' {
				b = append(b, ch)
				state = stateNormal
			}
		case stateBlockComment:
			if ch == '*' && i+1 < len(s) && s[i+1] == '/' {
				state = stateNormal
				i++
				continue
			}
			if ch == '\n' {
				b = append(b, ch)
			}
		}
	}
	return string(b)
}

// LoadContent runs the ordered processing pipeline (substitution, then comment
// removal) over raw config text and parses the result.
func LoadContent(raw string, getenv func(string) string) (*Set, error) {
	processed := StripComments(Substitute(raw, getenv))
	return parse(processed)
}

func parse(content string) (*Set, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(content), &top); err != nil {
		return nil, fmt.Errorf("invalid config JSON: %w", err)
	}
	if top == nil {
		return nil, errors.New("config must be a JSON object")
	}

	set := &Set{}
	rawServers, ok := top["mcpServers"]
	if !ok {
		// An absent key means an empty set; a present key, even null, must be
		// an object (SPEC §6.2).
		return set, nil
	}

	var entries map[string]json.RawMessage
	if err := json.Unmarshal(rawServers, &entries); err != nil {
		return nil, fmt.Errorf("mcpServers must be an object: %w", err)
	}
	if entries == nil {
		return nil, errors.New("mcpServers must be an object")
	}

	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		if reservedNames[name] {
			set.Warnings = append(set.Warnings,
				fmt.Sprintf("server %q is a reserved name; skipping", name))
			continue
		}
		srv, err := parseServer(name, entries[name])
		if err != nil {
			return nil, err
		}
		set.Servers = append(set.Servers, srv)
	}
	return set, nil
}

func parseServer(name string, raw json.RawMessage) (*Server, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, fmt.Errorf("server %q must be an object: %w", name, err)
	}
	if obj == nil {
		return nil, fmt.Errorf("server %q must be an object", name)
	}

	srv := &Server{
		Name: name,
		Args: []string{},
		Env:  map[string]string{},
	}

	command, hasCommand, err := stringField(obj, "command")
	if err != nil {
		return nil, fmt.Errorf("server %q: %w", name, err)
	}
	url, hasURL, err := stringField(obj, "url")
	if err != nil {
		return nil, fmt.Errorf("server %q: %w", name, err)
	}

	cli, err := boolField(obj, "cli")
	if err != nil {
		return nil, fmt.Errorf("server %q: %w", name, err)
	}

	// env and headers are validated whenever present, regardless of the
	// inferred type: a malformed cross-type field is an error (SPEC §6.3).
	env, err := objectField(obj, "env")
	if err != nil {
		return nil, fmt.Errorf("server %q: %w", name, err)
	}
	headers, err := objectField(obj, "headers")
	if err != nil {
		return nil, fmt.Errorf("server %q: %w", name, err)
	}

	if hasCommand && !cli {
		srv.Type = TypeStdio
		srv.Command = command
	} else if hasURL {
		srv.Type = TypeHTTP
		srv.URL = url
	} else {
		return nil, fmt.Errorf("server %q has neither command nor url", name)
	}

	if srv.Type == TypeStdio {
		if rawArgs, ok := obj["args"]; ok && string(rawArgs) != "null" {
			var args []string
			if err := json.Unmarshal(rawArgs, &args); err != nil {
				return nil, fmt.Errorf("server %q: args must be an array of strings: %w", name, err)
			}
			if args != nil {
				srv.Args = args
			}
		}
		if env != nil {
			srv.Env = env
		}
	}

	if srv.Type == TypeHTTP {
		if headers != nil {
			srv.Headers = headers
		}
	}

	return srv, nil
}

func stringField(obj map[string]json.RawMessage, key string) (string, bool, error) {
	raw, ok := obj[key]
	if !ok || string(raw) == "null" {
		return "", false, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false, fmt.Errorf("%s must be a string", key)
	}
	return s, true, nil
}

func boolField(obj map[string]json.RawMessage, key string) (bool, error) {
	raw, ok := obj[key]
	if !ok || string(raw) == "null" {
		return false, nil
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		return false, fmt.Errorf("%s must be a boolean", key)
	}
	return v, nil
}

func objectField(obj map[string]json.RawMessage, key string) (map[string]string, error) {
	raw, ok := obj[key]
	if !ok {
		return nil, nil
	}
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("%s must be an object of strings: %w", key, err)
	}
	if m == nil {
		return nil, fmt.Errorf("%s must be an object of strings", key)
	}
	return m, nil
}

// ResolvePath returns the config file path from the environment without reading
// the file. The second result lists non-fatal warnings (for example when the
// home directory cannot be determined).
func ResolvePath() (string, []string) {
	if v := os.Getenv("MCP_CONFIG_PATH"); v != "" {
		return v, nil
	}
	if v := os.Getenv("MCP_CONFIG_DIR"); v != "" {
		return filepath.Join(v, "servers.json"), nil
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "mcp", "servers.json"), nil
	}
	return filepath.Join("/tmp", "mcp", "servers.json"),
		[]string{"home directory not found; using /tmp/mcp/servers.json"}
}

// Load resolves the config location and parses it. A missing file yields an
// empty set, not an error. MCP_SERVERS_CONFIG (inline JSON) takes precedence
// over all file locations.
func Load() (*Set, error) {
	if inline, ok := os.LookupEnv("MCP_SERVERS_CONFIG"); ok {
		set, err := LoadContent(inline, os.Getenv)
		if err != nil {
			return nil, err
		}
		return set, nil
	}

	path, warnings := ResolvePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &Set{Servers: []*Server{}, Warnings: warnings}, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	set, err := LoadContent(string(data), os.Getenv)
	if err != nil {
		return nil, err
	}
	set.Warnings = append(warnings, set.Warnings...)
	return set, nil
}

// Find returns the server with the given name, or nil.
func (s *Set) Find(name string) *Server {
	for _, srv := range s.Servers {
		if srv.Name == name {
			return srv
		}
	}
	return nil
}
