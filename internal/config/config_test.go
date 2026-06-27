package config

import (
	"testing"
	"time"
)

func TestDefaultsAreReadOnly(t *testing.T) {
	c := Defaults()
	if c.AllowWrites || c.AllowDestructive || c.AllowDebug {
		t.Fatalf("defaults must be read-only, got writes=%v destructive=%v debug=%v", c.AllowWrites, c.AllowDestructive, c.AllowDebug)
	}
	if c.Transport != "stdio" {
		t.Errorf("default transport = %q, want stdio", c.Transport)
	}
	if c.DefaultTimeout != 30*time.Second {
		t.Errorf("default timeout = %v, want 30s", c.DefaultTimeout)
	}
}

func TestValidateDestructiveImpliesWrites(t *testing.T) {
	c := Defaults()
	c.AllowDestructive = true
	if err := c.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !c.AllowWrites {
		t.Error("AllowDestructive should imply AllowWrites")
	}
}

func TestValidateRejectsBadTransport(t *testing.T) {
	c := Defaults()
	c.Transport = "grpc"
	if err := c.Validate(); err == nil {
		t.Fatal("expected error for bad transport")
	}
}

func TestCategoryEnabled(t *testing.T) {
	c := Defaults()
	if !c.CategoryEnabled("core") {
		t.Error("empty EnableCategories should enable all")
	}
	c.EnableCategories = []string{"core", "workloads"}
	if !c.CategoryEnabled("workloads") {
		t.Error("workloads should be enabled")
	}
	if c.CategoryEnabled("debug") {
		t.Error("debug should not be enabled")
	}
}

func TestHasNamespaceAllowlist(t *testing.T) {
	c := Defaults()
	if c.HasNamespaceAllowlist() {
		t.Error("no namespaces should mean no allowlist")
	}
	c.Namespaces = []string{"default"}
	if !c.HasNamespaceAllowlist() {
		t.Error("expected allowlist present")
	}
}

func TestHTTPPlaintextNonLoopbackRejected(t *testing.T) {
	c := Defaults()
	c.Transport = "http"
	c.Listen = "0.0.0.0:8080"
	if err := c.Validate(); err == nil {
		t.Fatal("expected plaintext HTTP on non-loopback to be rejected")
	}
}

func TestHTTPLoopbackAllowed(t *testing.T) {
	c := Defaults()
	c.Transport = "http"
	c.Listen = "127.0.0.1:8080"
	if err := c.Validate(); err != nil {
		t.Fatalf("loopback HTTP should be allowed: %v", err)
	}
}

func TestHTTPAllowedWithAuthToken(t *testing.T) {
	c := Defaults()
	c.Transport = "http"
	c.Listen = "0.0.0.0:8080"
	c.AuthToken = "s3cret"
	if err := c.Validate(); err != nil {
		t.Fatalf("HTTP with bearer auth should be allowed: %v", err)
	}
}

func TestHTTPAllowedWithTLS(t *testing.T) {
	c := Defaults()
	c.Transport = "http"
	c.Listen = "0.0.0.0:8080"
	c.TLSCert = "/tmp/cert.pem"
	c.TLSKey = "/tmp/key.pem"
	if err := c.Validate(); err != nil {
		t.Fatalf("HTTP with TLS should be allowed: %v", err)
	}
}

func TestHTTPInsecureOptIn(t *testing.T) {
	c := Defaults()
	c.Transport = "http"
	c.Listen = "0.0.0.0:8080"
	c.InsecureHTTP = true
	if err := c.Validate(); err != nil {
		t.Fatalf("--insecure-http should bypass the guard: %v", err)
	}
}
