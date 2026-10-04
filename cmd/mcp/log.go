package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// logLevel orders diagnostics from most to least verbose (SPEC §7).
type logLevel int

const (
	levelDebug logLevel = iota
	levelInfo
	levelWarn
	levelError
)

// parseLogLevel maps MCP_LOG_LEVEL to a level. An unset or unrecognized value
// defaults to info (SPEC §7).
func parseLogLevel(s string) logLevel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return levelDebug
	case "info":
		return levelInfo
	case "warn":
		return levelWarn
	case "error":
		return levelError
	default:
		return levelInfo
	}
}

// logger is a tiny level-gated writer. A message at level L is emitted when the
// configured threshold is no higher than L, i.e. debug < info < warn < error.
type logger struct {
	level logLevel
	w     io.Writer
}

func newLogger(w io.Writer) *logger {
	return &logger{level: parseLogLevel(os.Getenv("MCP_LOG_LEVEL")), w: w}
}

func (l *logger) logf(level logLevel, prefix, format string, args ...any) {
	if l == nil || l.level > level {
		return
	}
	fmt.Fprintf(l.w, "mcp: %s: %s\n", prefix, fmt.Sprintf(format, args...))
}

func (l *logger) debugf(format string, args ...any) {
	l.logf(levelDebug, "debug", format, args...)
}

func (l *logger) infof(format string, args ...any) {
	l.logf(levelInfo, "info", format, args...)
}

// warnf emits reserved-name and similar warnings. These are shown at
// debug/info/warn and suppressed at error.
func (l *logger) warnf(format string, args ...any) {
	l.logf(levelWarn, "warning", format, args...)
}
