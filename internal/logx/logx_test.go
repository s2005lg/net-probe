package logx

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]Level{
		"debug":   LevelDebug,
		"INFO":    LevelInfo,
		"warning": LevelWarn,
		"warn":    LevelWarn,
		"error":   LevelError,
		"":        LevelInfo,
		"bogus":   LevelInfo,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Fatalf("ParseLevel(%q)=%d want %d", in, got, want)
		}
	}
}

func TestLoggerFiltersBelowLevel(t *testing.T) {
	var buf bytes.Buffer
	l := New("warn")
	l.SetOutput(&buf)
	l.Debugf("debug")
	l.Infof("info")
	l.Warnf("warn")
	l.Errorf("error")
	out := buf.String()
	if strings.Contains(out, "debug") || strings.Contains(out, "INFO") {
		t.Fatalf("unexpected low-level output: %q", out)
	}
	if !strings.Contains(out, "WARN") || !strings.Contains(out, "ERROR") {
		t.Fatalf("missing warn/error output: %q", out)
	}
}
