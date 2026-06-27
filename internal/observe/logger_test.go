package observe

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"debug", "DEBUG"},
		{"DEBUG", "DEBUG"},
		{"warn", "WARN"},
		{"warning", "WARN"},
		{"error", "ERROR"},
		{"info", "INFO"},
		{"", "INFO"},
		{"nonsense", "INFO"},
	} {
		lvl := parseLevel(tc.in)
		if got := lvl.String(); got != tc.want {
			t.Errorf("parseLevel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewLoggerToJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	log := NewLoggerTo(&buf, "info", "json")
	log.Info("hello", "k", "v")
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("json-format output should be valid JSON, got %q: %v", buf.String(), err)
	}
	if out["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", out["msg"])
	}
}

func TestNewLoggerToTextFormat(t *testing.T) {
	var buf bytes.Buffer
	log := NewLoggerTo(&buf, "info", "text")
	log.Info("hello")
	if !strings.Contains(buf.String(), "msg=hello") {
		t.Errorf("text-format output should contain msg=hello, got %q", buf.String())
	}
}

func TestNewLoggerToDefaultsToJSON(t *testing.T) {
	var buf bytes.Buffer
	log := NewLoggerTo(&buf, "info", "")
	log.Info("hello")
	if err := json.Unmarshal(buf.Bytes(), &map[string]any{}); err != nil {
		t.Errorf("empty format should default to JSON, got %q: %v", buf.String(), err)
	}
}

func TestLoggerLevelFilters(t *testing.T) {
	var buf bytes.Buffer
	log := NewLoggerTo(&buf, "warn", "json")
	log.Info("filtered-out")
	log.Warn("kept")
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	var kept bool
	for _, ln := range lines {
		if strings.Contains(ln, "kept") {
			kept = true
		}
		if strings.Contains(ln, "filtered-out") {
			t.Error("info record must not be emitted at warn level")
		}
	}
	if !kept {
		t.Errorf("warn record should be emitted, got %q", buf.String())
	}
}

func TestLoggerDebugEmittedAtDebugLevel(t *testing.T) {
	var buf bytes.Buffer
	log := NewLoggerTo(&buf, "debug", "json")
	log.Debug("trace")
	if !strings.Contains(buf.String(), "trace") {
		t.Errorf("debug record should be emitted at debug level, got %q", buf.String())
	}
}

func TestAuditSinkEmptyPathReturnsFallback(t *testing.T) {
	fb := &bytes.Buffer{}
	if got := AuditSink("", fb); got != fb {
		t.Error("empty path should return the fallback writer unchanged")
	}
}

func TestAuditSinkOpensFileForAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.log")
	sink := AuditSink(path, &bytes.Buffer{})
	f, ok := sink.(*os.File)
	if !ok {
		t.Fatalf("expected an *os.File for a non-empty path, got %T", sink)
	}
	if _, err := f.WriteString("line1\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	// A second open must append, not truncate.
	sink2 := AuditSink(path, &bytes.Buffer{})
	f2, _ := sink2.(*os.File)
	_, _ = f2.WriteString("line2\n")
	_ = f2.Close()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	body := string(b)
	if !strings.Contains(body, "line1") || !strings.Contains(body, "line2") {
		t.Errorf("expected both lines appended, got %q", body)
	}
}

func TestAuditSinkFallsBackOnUnwritablePath(t *testing.T) {
	fb := &bytes.Buffer{}
	// A path inside a nonexistent directory cannot be opened.
	sink := AuditSink(filepath.Join(t.TempDir(), "no-such-dir", "audit.log"), fb)
	if sink != fb {
		t.Error("an unwritable path should fall back to the fallback writer")
	}
}
