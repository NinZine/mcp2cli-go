// Package preflight checks whether a stdio server's command can be run before
// attempting to spawn it, producing the exact reason strings the CLI surfaces.
package preflight

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Available reports whether command is runnable. It returns an empty reason when
// the command may be attempted, or one of the canonical reason strings from the
// specification when it cannot run.
//
// env is the server's own environment. A PATH set there is honored; otherwise
// the process PATH is used. When the effective PATH is unset or empty a bare
// command is not blocked (nil reason), letting the OS attempt the spawn.
func Available(command string, env map[string]string) (bool, string) {
	if command == "" {
		return false, "command is empty"
	}

	// A command containing a path separator is checked directly.
	if strings.ContainsRune(command, filepath.Separator) {
		info, err := os.Stat(command)
		if err != nil {
			return false, fmt.Sprintf("command does not exist: %s", command)
		}
		if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			return false, fmt.Sprintf("command is not executable: %s", command)
		}
		return true, ""
	}

	pathValue, ok := env["PATH"]
	if !ok {
		pathValue = os.Getenv("PATH")
	}
	if pathValue == "" {
		// Cannot decide; do not block.
		return true, ""
	}

	for _, dir := range filepath.SplitList(pathValue) {
		if dir == "" {
			dir = "."
		}
		candidate := filepath.Join(dir, command)
		info, err := os.Stat(candidate)
		if err != nil {
			continue
		}
		if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		return true, ""
	}

	return false, fmt.Sprintf("command not found in PATH: %s", command)
}
