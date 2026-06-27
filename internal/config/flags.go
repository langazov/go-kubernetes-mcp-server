package config

import (
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// AddFlags registers all configuration flags on the given command, using
// environment variables as defaults.
func AddFlags(cmd *cobra.Command) {
	fs := cmd.Flags()

	// Cluster connection
	fs.String("kubeconfig", EnvOrDefault("kubeconfig", ""), "path to kubeconfig (empty = in-cluster service account)")
	fs.String("context", EnvOrDefault("context", ""), "named kubeconfig context to use")
	fs.String("cluster-name", EnvOrDefault("cluster-name", ""), "friendly cluster name surfaced in cluster_health")
	fs.Duration("default-timeout", durEnvOrDefault("default-timeout", 30*time.Second), "context deadline for API-server calls")
	fs.Float64("qps", numEnvOrDefault("qps", 50), "client-go QPS limit")
	fs.Int("burst", intEnvOrDefault("burst", 100), "client-go burst limit")

	// Transport
	fs.String("transport", EnvOrDefault("transport", "stdio"), "transport: stdio | http")
	fs.String("listen", EnvOrDefault("listen", "127.0.0.1:8080"), "HTTP listen address (default 127.0.0.1:8080)")
	fs.String("endpoint", EnvOrDefault("endpoint", "/mcp"), "HTTP MCP endpoint path")
	fs.StringSlice("cors-origins", nil, "comma-separated allowed CORS origins for HTTP transport")
	fs.String("tls-cert", EnvOrDefault("tls-cert", ""), "TLS certificate for the HTTP transport (enables HTTPS)")
	fs.String("tls-key", EnvOrDefault("tls-key", ""), "TLS private key for the HTTP transport")
	fs.String("tls-client-ca", EnvOrDefault("tls-client-ca", ""), "CA bundle to verify and require client certificates (mTLS)")
	fs.String("auth-token", EnvOrDefault("auth-token", ""), "shared-secret bearer token required by the HTTP transport (Authorization: Bearer <token>)")
	fs.String("auth-token-file", EnvOrDefault("auth-token-file", ""), "file containing the shared-secret bearer token (alternative to --auth-token)")
	fs.Bool("insecure-http", boolEnvOrDefault("insecure-http", false), "allow plaintext unauthenticated HTTP on a non-loopback address (NOT recommended)")
	fs.StringSlice("oauth-authorization-servers", nil, "OAuth authorization server URLs advertised at /.well-known/oauth-protected-resource (HTTP transport)")
	fs.StringSlice("oauth-scopes", nil, "OAuth scopes supported by this resource (e.g. mcp:read,mcp:write)")

	// Security / blast radius
	fs.Bool("allow-writes", boolEnvOrDefault("allow-writes", false), "register mutating tools")
	fs.Bool("allow-destructive", boolEnvOrDefault("allow-destructive", false), "register destructive tools (implies --allow-writes)")
	fs.Bool("allow-debug", boolEnvOrDefault("allow-debug", false), "register exec/ephemeral/port-forward/debug-pod tools")
	fs.Bool("allow-privileged-targets", boolEnvOrDefault("allow-privileged-targets", false), "permit ops on kube-system and cluster-scoped resources")
	fs.StringSlice("namespace", nil, "namespace allowlist (repeatable; empty = all namespaces)")
	fs.Bool("reveal-secrets", boolEnvOrDefault("reveal-secrets", false), "allow per-call secret value reveal")
	fs.StringSlice("allowed-manifest-kinds", nil, "restrict apply_manifest to these GVKs (e.g. Deployment.v1.apps)")
	fs.StringSlice("forbidden-images", nil, "block these images for run_debug_pod/ephemeral containers")
	fs.StringSlice("allowed-debug-service-accounts", nil, "service accounts run_debug_pod may use (empty = only 'default')")

	// Tooling
	fs.StringSlice("enable-categories", nil, "comma list of tool categories (core,workloads,troubleshoot,debug,network,configstore,operations)")
	fs.String("output-format", EnvOrDefault("output-format", "text"), "default result rendering: text | json")
	fs.Int("max-output-bytes", intEnvOrDefault("max-output-bytes", 256*1024), "per-result truncation ceiling in bytes")
	fs.Int("max-log-lines", intEnvOrDefault("max-log-lines", 1000), "default tail line count for get_logs")
	fs.Int("max-manifest-bytes", intEnvOrDefault("max-manifest-bytes", 256*1024), "maximum accepted size of an apply_manifest payload")
	fs.Int("max-concurrent-calls", intEnvOrDefault("max-concurrent-calls", 16), "maximum in-flight tool calls (0 = unlimited)")

	// Observability
	fs.String("log-level", EnvOrDefault("log-level", "info"), "log level: debug | info | warn | error")
	fs.String("log-format", EnvOrDefault("log-format", "json"), "log format: json | text")
	fs.String("audit-path", EnvOrDefault("audit-path", ""), "path to write audit log (empty = stderr)")
	fs.String("otel-endpoint", EnvOrDefault("otel-endpoint", ""), "OTLP exporter URL for tracing")
}

// FromFlags builds a Config from a parsed flag set.
func FromFlags(fs *pflag.FlagSet) (Config, error) {
	getStr := func(name string) string {
		v, _ := fs.GetString(name)
		return v
	}
	getBool := func(name string) bool {
		v, _ := fs.GetBool(name)
		return v
	}
	getInt := func(name string) int {
		v, _ := fs.GetInt(name)
		return v
	}
	getF := func(name string) float64 {
		v, _ := fs.GetFloat64(name)
		return v
	}
	getDur := func(name string) time.Duration {
		v, _ := fs.GetDuration(name)
		return v
	}
	getSlice := func(name string) []string {
		v, _ := fs.GetStringSlice(name)
		return v
	}

	c := Config{
		Kubeconfig:                  getStr("kubeconfig"),
		Context:                     getStr("context"),
		ClusterName:                 getStr("cluster-name"),
		DefaultTimeout:              getDur("default-timeout"),
		QPS:                         getF("qps"),
		Burst:                       getInt("burst"),
		Transport:                   getStr("transport"),
		Listen:                      getStr("listen"),
		Endpoint:                    getStr("endpoint"),
		CORSOrigins:                 getSlice("cors-origins"),
		TLSCert:                     getStr("tls-cert"),
		TLSKey:                      getStr("tls-key"),
		TLSClientCA:                 getStr("tls-client-ca"),
		AuthToken:                   getStr("auth-token"),
		AuthTokenFile:               getStr("auth-token-file"),
		InsecureHTTP:                getBool("insecure-http"),
		OAuthAuthorizationServers:   getSlice("oauth-authorization-servers"),
		OAuthScopes:                 getSlice("oauth-scopes"),
		AllowWrites:                 getBool("allow-writes"),
		AllowDestructive:            getBool("allow-destructive"),
		AllowDebug:                  getBool("allow-debug"),
		AllowPrivilegedTargets:      getBool("allow-privileged-targets"),
		Namespaces:                  getSlice("namespace"),
		RevealSecrets:               getBool("reveal-secrets"),
		AllowedManifestKinds:        getSlice("allowed-manifest-kinds"),
		ForbiddenImages:             getSlice("forbidden-images"),
		AllowedDebugServiceAccounts: getSlice("allowed-debug-service-accounts"),
		EnableCategories:            getSlice("enable-categories"),
		OutputFormat:                getStr("output-format"),
		MaxOutputBytes:              getInt("max-output-bytes"),
		MaxLogLines:                 getInt("max-log-lines"),
		MaxManifestBytes:            getInt("max-manifest-bytes"),
		MaxConcurrentCalls:          getInt("max-concurrent-calls"),
		LogLevel:                    getStr("log-level"),
		LogFormat:                   getStr("log-format"),
		AuditPath:                   getStr("audit-path"),
		OTPLEndpoint:                getStr("otel-endpoint"),
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func boolEnvOrDefault(flag string, def bool) bool {
	key := envKey(flag)
	if v, ok := lookupEnv(key); ok {
		return strings.EqualFold(v, "true") || v == "1"
	}
	return def
}

func intEnvOrDefault(flag string, def int) int {
	key := envKey(flag)
	if v, ok := lookupEnv(key); ok {
		if n, err := parseInt(v); err == nil {
			return n
		}
	}
	return def
}

func numEnvOrDefault(flag string, def float64) float64 {
	key := envKey(flag)
	if v, ok := lookupEnv(key); ok {
		if f, err := parseFloat(v); err == nil {
			return f
		}
	}
	return def
}

func durEnvOrDefault(flag string, def time.Duration) time.Duration {
	key := envKey(flag)
	if v, ok := lookupEnv(key); ok {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
