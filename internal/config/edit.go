package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
)

// IsReserved reports whether name is a reserved server name that would collide
// with a command (present or future).
func IsReserved(name string) bool {
	return reservedNames[name]
}

// AddHTTPServer inserts an HTTP server entry into the config file at path.
//
// The URL is validated before the file is touched: it must be absolute with an
// http or https scheme. A missing file starts from an empty document; unknown
// top-level keys and unrelated servers are preserved (comments and formatting
// are not, because the document is rewritten through a JSON parser). Parent
// directories are created as needed.
func AddHTTPServer(path, name, rawURL string) error {
	if err := validateHTTPURL(rawURL); err != nil {
		return err
	}

	root, err := readRoot(path)
	if err != nil {
		return err
	}

	servers, err := decodeServers(root)
	if err != nil {
		return err
	}
	if _, exists := servers[name]; exists {
		return fmt.Errorf("server %q already exists in %s", name, path)
	}

	entry, err := json.Marshal(map[string]string{"url": rawURL})
	if err != nil {
		return fmt.Errorf("encoding server %q: %w", name, err)
	}
	servers[name] = json.RawMessage(entry)

	return writeRoot(path, root, servers)
}

// RemoveServer deletes an HTTP or stdio server entry from the config file at
// path. The file must exist and the name must be present; on any error the file
// is left unchanged.
func RemoveServer(path, name string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("config file %s does not exist", path)
		}
		return fmt.Errorf("reading config %s: %w", path, err)
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(StripComments(string(data))), &root); err != nil {
		return fmt.Errorf("invalid config JSON: %w", err)
	}
	if root == nil {
		return errors.New("config must be a JSON object")
	}

	servers, err := decodeServers(root)
	if err != nil {
		return err
	}
	if _, exists := servers[name]; !exists {
		return fmt.Errorf("server %q not found in %s", name, path)
	}
	delete(servers, name)

	return writeRoot(path, root, servers)
}

// validateHTTPURL rejects anything that is not an absolute http(s) URL.
func validateHTTPURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if !u.IsAbs() {
		return fmt.Errorf("invalid URL %q: must be absolute", rawURL)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid URL %q: must include a host", rawURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("invalid URL %q: scheme must be http or https", rawURL)
	}
	return nil
}

// readRoot reads a config document into a map of raw top-level values. A
// missing file yields an empty document.
func readRoot(path string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]json.RawMessage{}, nil
		}
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	root := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(StripComments(string(data))), &root); err != nil {
		return nil, fmt.Errorf("invalid config JSON: %w", err)
	}
	if root == nil {
		return nil, errors.New("config must be a JSON object")
	}
	return root, nil
}

// decodeServers decodes the mcpServers member, creating an empty map when it is
// absent. A present member that is not a JSON object (including null) is an
// error, matching the loader's SPEC §6.2 validation; the editor must not
// silently repair a document the loader would reject.
func decodeServers(root map[string]json.RawMessage) (map[string]json.RawMessage, error) {
	raw, ok := root["mcpServers"]
	if !ok {
		return map[string]json.RawMessage{}, nil
	}
	servers := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &servers); err != nil {
		return nil, fmt.Errorf("mcpServers must be an object: %w", err)
	}
	if servers == nil {
		return nil, errors.New("mcpServers must be an object")
	}
	return servers, nil
}

// writeRoot serializes root with the updated servers as two-space-indented JSON
// plus a trailing newline, creating parent directories and using mode 0644 for
// new files.
//
// The write is atomic: bytes go to a temp file in the destination directory,
// are synced, and are renamed over the target. A failure at any point leaves
// the original file untouched and removes the temp file, so a partial write can
// never destroy the user's config.
func writeRoot(path string, root map[string]json.RawMessage, servers map[string]json.RawMessage) error {
	encoded, err := json.Marshal(servers)
	if err != nil {
		return fmt.Errorf("encoding mcpServers: %w", err)
	}
	root["mcpServers"] = encoded

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}
	out = append(out, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	// Preserve the existing file's permissions; new files are 0644.
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}

	tmp, err := os.CreateTemp(dir, ".servers-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp config: %w", err)
	}
	tmpName := tmp.Name()
	// Remove the temp file on every failure path; renamed means nothing to do.
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("setting config mode: %w", err)
	}
	if _, err := tmp.Write(out); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("syncing config %s: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("writing config %s: %w", path, err)
	}
	tmpName = ""
	return nil
}
