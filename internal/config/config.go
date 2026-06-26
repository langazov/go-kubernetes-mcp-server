package config

import (
	"fmt"
	"os"
	"strings"
	"time"
)

// Config holds all server configuration, derived from flags and environment
// variables (prefix K8S_MCP_, dashes -> underscores).
type Config struct {
	// Cluster connection
	Kubeconfig     string
	Context        string
	ClusterName    string
	DefaultTimeout time.Duration
	QPS            float64
	Burst          int

	// Transport
	Transport   string
	Listen      string
	Endpoint    string
	CORSOrigins []string

	// OAuth Protected-Resource Metadata (RFC 9728), served by the HTTP transport
	// at /.well-known/oauth-protected-resource. Empty = not advertised.
	OAuthAuthorizationServers []string
	OAuthScopes               []string

	// Security / blast radius
	AllowWrites            bool
	AllowDestructive       bool
	AllowDebug             bool
	AllowPrivilegedTargets bool
	Namespaces             []string
	RevealSecrets          bool
	AllowedManifestKinds   []string
	ForbiddenImages        []string

	// Tooling
	EnableCategories []string
	OutputFormat     string
	MaxOutputBytes   int
	MaxLogLines      int

	// Observability
	LogLevel     string
	LogFormat    string
	AuditPath    string
	OTPLEndpoint string
}

// Defaults returns a Config populated with safe defaults (read-only).
func Defaults() Config {
	return Config{
		Kubeconfig:             "",
		Context:                "",
		ClusterName:            "",
		DefaultTimeout:         30 * time.Second,
		QPS:                    50,
		Burst:                  100,
		Transport:              "stdio",
		Listen:                 ":8080",
		Endpoint:               "/mcp",
		CORSOrigins:            nil,
		AllowWrites:            false,
		AllowDestructive:       false,
		AllowDebug:             false,
		AllowPrivilegedTargets: false,
		Namespaces:             nil,
		RevealSecrets:          false,
		OutputFormat:           "text",
		MaxOutputBytes:         256 * 1024,
		MaxLogLines:            1000,
		LogLevel:               "info",
		LogFormat:              "json",
		AuditPath:              "",
		OTPLEndpoint:           "",
	}
}

// Validate checks the configuration for consistency and applies implications.
func (c *Config) Validate() error {
	switch strings.ToLower(c.Transport) {
	case "stdio", "http":
	default:
		return fmt.Errorf("invalid transport %q: must be stdio or http", c.Transport)
	}
	switch strings.ToLower(c.OutputFormat) {
	case "text", "json":
	default:
		return fmt.Errorf("invalid output-format %q: must be text or json", c.OutputFormat)
	}
	if c.DefaultTimeout <= 0 {
		return fmt.Errorf("default-timeout must be positive, got %s", c.DefaultTimeout)
	}
	if c.QPS < 0 {
		return fmt.Errorf("qps must be non-negative, got %v", c.QPS)
	}
	if c.Burst < 0 {
		return fmt.Errorf("burst must be non-negative, got %v", c.Burst)
	}
	if c.MaxOutputBytes <= 0 {
		return fmt.Errorf("max-output-bytes must be positive, got %d", c.MaxOutputBytes)
	}
	if c.MaxLogLines <= 0 {
		return fmt.Errorf("max-log-lines must be positive, got %d", c.MaxLogLines)
	}
	// Destructive implies writes.
	if c.AllowDestructive {
		c.AllowWrites = true
	}
	return nil
}

// HasNamespaceAllowlist reports whether namespaces are restricted.
func (c *Config) HasNamespaceAllowlist() bool { return len(c.Namespaces) > 0 }

// CategoryEnabled reports whether a tool category is enabled.
// An empty EnableCategories list means all categories are enabled.
func (c *Config) CategoryEnabled(name string) bool {
	if len(c.EnableCategories) == 0 {
		return true
	}
	for _, e := range c.EnableCategories {
		if strings.EqualFold(e, name) {
			return true
		}
	}
	return false
}

// EnvOrDefault returns the env value for a flag name (e.g. "kubeconfig") if set,
// else the provided default.
func EnvOrDefault(flag, def string) string {
	key := "K8S_MCP_" + strings.ToUpper(strings.ReplaceAll(flag, "-", "_"))
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
