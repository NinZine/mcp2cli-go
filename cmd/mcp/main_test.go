package main

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestParseLogLevel(t *testing.T) {
	cases := map[string]logLevel{
		"debug": levelDebug,
		"info":  levelInfo,
		"warn":  levelWarn,
		"error": levelError,
		"":      levelInfo,
		"bogus": levelInfo,
		"WARN":  levelWarn,
	}
	for in, want := range cases {
		if got := parseLogLevel(in); got != want {
			t.Errorf("parseLogLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLoggerLevelGating(t *testing.T) {
	var buf bytes.Buffer
	l := &logger{level: levelInfo, w: &buf}
	l.debugf("d")
	l.warnf("w")
	if strings.Contains(buf.String(), "debug") {
		t.Fatalf("debug must be suppressed at info: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "warning: w") {
		t.Fatalf("warn must be shown at info: %q", buf.String())
	}

	buf.Reset()
	l = &logger{level: levelError, w: &buf}
	l.warnf("w")
	if buf.Len() != 0 {
		t.Fatalf("warn must be suppressed at error: %q", buf.String())
	}

	buf.Reset()
	l = &logger{level: levelDebug, w: &buf}
	l.debugf("d")
	if !strings.Contains(buf.String(), "debug: d") {
		t.Fatalf("debug must be shown at debug: %q", buf.String())
	}
}

func TestRequestTimeoutClamp(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Duration
	}{
		{"", 60 * time.Second},
		{"30", 30 * time.Second},
		{"0", 60 * time.Second},
		{"-5", 60 * time.Second},
		{"abc", 60 * time.Second},
		{"99999999999999999999", 60 * time.Second},              // out of int range
		{"9223372037", 60 * time.Second},                        // overflows duration
		{"9223372036", time.Duration(9223372036) * time.Second}, // largest safe
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			t.Setenv("MCP_TIMEOUT", tc.raw)
			if got := requestTimeout(); got != tc.want {
				t.Fatalf("requestTimeout() with %q = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
