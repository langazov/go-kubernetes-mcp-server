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

	// TLS / client auth for the HTTP transport.
	TLSCert     string
	TLSKey      string
	TLSClientCA string

	// Shared-secret bearer auth for the HTTP transport (/mcp). When set, every
	// request must carry `Authorization: Bearer <token>`. Empty = no authN.
	AuthToken     string
	AuthTokenFile string

	// InsecureHTTP explicitly opts out of the refusal to serve plaintext,
	// unauthenticated HTTP on a non-loopback address.
	InsecureHTTP bool

	// OAuth Protected-Resource Metadata (RFC 9728), served by the HTTP transport
	// at /.well-known/oauth-protected-resource. Empty = not advertised.
	OAuthAuthorizationServers []string
	OAuthScopes               []string

	// Security / blast radius
	AllowWrites                 bool
	AllowDestructive            bool
	AllowDebug                  bool
	AllowPrivilegedTargets      bool
	Namespaces                  []string
	RevealSecrets               bool
	AllowedManifestKinds        []string
	ForbiddenImages             []string
	AllowedDebugServiceAccounts []string

	// Tooling
	EnableCategories   []string
	OutputFormat       string
	MaxOutputBytes     int
	MaxLogLines        int
	MaxManifestBytes   int
	MaxConcurrentCalls int

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
		Listen:                 "127.0.0.1:8080",
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
		MaxManifestBytes:       256 * 1024,
		MaxConcurrentCalls:     16,
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
	if c.MaxManifestBytes <= 0 {
		return fmt.Errorf("max-manifest-bytes must be positive, got %d", c.MaxManifestBytes)
	}
	if c.MaxConcurrentCalls < 0 {
		return fmt.Errorf("max-concurrent-calls must be non-negative, got %d", c.MaxConcurrentCalls)
	}
	if c.AuthToken != "" && c.AuthTokenFile != "" {
		return fmt.Errorf("--auth-token and --auth-token-file are mutually exclusive")
	}
	// Destructive implies writes.
	if c.AllowDestructive {
		c.AllowWrites = true
	}
	// Refuse to serve an unauthenticated, plaintext HTTP server on a
	// non-loopback address unless the operator explicitly opts in.
	if strings.EqualFold(c.Transport, "http") && !c.InsecureHTTP {
		if err := c.validateHTTPExposure(); err != nil {
			return err
		}
	}
	return nil
}

func (c *Config) validateHTTPExposure() error {
	if c.IsLoopbackListen() {
		return nil
	}
	tls := c.TLSCert != "" && c.TLSKey != ""
	if tls || (c.AuthToken != "" || c.AuthTokenFile != "") {
		return nil
	}
	return fmt.Errorf("refusing to serve unauthenticated plaintext HTTP on %q (non-loopback): "+
		"set --tls-cert/--tls-key and/or --auth-token, bind to 127.0.0.1 with --listen, "+
		"or pass --insecure-http to acknowledge the risk", c.Listen)
}

// IsLoopbackListen reports whether the HTTP listen address is loopback only.
func (c *Config) IsLoopbackListen() bool {
	host := listenHost(c.Listen)
	switch host {
	case "", "0.0.0.0", "::":
		return false
	case "127.0.0.1", "localhost", "::1":
		return true
	}
	return false
}

// ResolvedAuthToken returns the bearer token configured via --auth-token or
// --auth-token-file (file wins), or empty when no authN is configured.
func (c *Config) ResolvedAuthToken() string {
	if c.AuthTokenFile != "" {
		b, err := os.ReadFile(c.AuthTokenFile)
		if err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return strings.TrimSpace(c.AuthToken)
}

func listenHost(addr string) string {
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
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
