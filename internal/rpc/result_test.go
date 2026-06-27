package rpc

import (
	"strings"
	"testing"
)

func TestValidateSelectorToken(t *testing.T) {
	for _, bad := range []string{"a,b", "x=y", "a b", "a,b=1", "a\"b"} {
		if err := ValidateSelectorToken(bad); err == nil {
			t.Errorf("expected rejection for selector token %q", bad)
		}
	}
	for _, ok := range []string{"", "Pod", "my-pod-1", "PersistentVolumeClaim"} {
		if err := ValidateSelectorToken(ok); err != nil {
			t.Errorf("expected acceptance for selector token %q: %v", ok, err)
		}
	}
}

func TestTruncateText(t *testing.T) {
	if got := TruncateText("hello", 100); got != "hello" {
		t.Errorf("short string should be unchanged, got %q", got)
	}
	got := TruncateText(strings.Repeat("a", 100), 10)
	if !strings.HasPrefix(got, "aaaaaaaaaa") {
		t.Errorf("expected first 10 bytes preserved, got %q", got)
	}
	if !strings.Contains(got, "truncated") {
		t.Errorf("expected truncation marker, got %q", got)
	}
}

func TestTruncateMultibyte(t *testing.T) {
	// '€' is 3 bytes in UTF-8; ensure truncation backs up to a rune boundary
	// rather than splitting a rune.
	s := strings.Repeat("€", 50) // 150 bytes
	got := TruncateText(s, 10)
	// Everything before the marker must be valid UTF-8 with whole runes.
	body := strings.SplitN(got, "\n…", 2)[0]
	for _, r := range body {
		if r == 0xFFFD {
			t.Errorf("truncation produced an invalid rune (0xFFFD): %q", got)
		}
	}
}

func TestTextAndErrorResult(t *testing.T) {
	r := TextResult("hello")
	if r.IsError {
		t.Error("TextResult must not be an error")
	}
	if len(r.Content) != 1 {
		t.Fatalf("expected 1 content, got %d", len(r.Content))
	}

	e := ErrorResult("bad %s", "thing")
	if !e.IsError {
		t.Error("ErrorResult must be an error")
	}
}

func TestTableRender(t *testing.T) {
	tab := NewTable("NAME", "STATUS")
	tab.AddRow("p1", "Running")
	tab.AddRow("with-a-long-name", "Pending")
	out := tab.Render()
	if !strings.Contains(out, "NAME") || !strings.Contains(out, "p1") {
		t.Errorf("table missing content: %q", out)
	}
	if !strings.Contains(out, "with-a-long-name") {
		t.Errorf("long row missing: %q", out)
	}
}

func TestValidateName(t *testing.T) {
	if err := ValidateName("my-pod-123"); err != nil {
		t.Errorf("valid name rejected: %v", err)
	}
	if err := ValidateName(""); err == nil {
		t.Error("empty name should be invalid")
	}
	if err := ValidateName("UPPER"); err == nil {
		t.Error("uppercase name should be invalid")
	}
}

func TestValidateNamespace(t *testing.T) {
	if err := ValidateNamespace(""); err != nil {
		t.Errorf("empty namespace should be allowed (resolved later): %v", err)
	}
	if err := ValidateNamespace("kube-system"); err != nil {
		t.Errorf("valid namespace rejected: %v", err)
	}
	if err := ValidateNamespace("Bad_NS"); err == nil {
		t.Error("invalid namespace should be rejected")
	}
}

func TestIsPrivilegedNamespace(t *testing.T) {
	if !IsPrivilegedNamespace("kube-system") {
		t.Error("kube-system should be privileged")
	}
	if IsPrivilegedNamespace("default") {
		t.Error("default should not be privileged")
	}
}
