package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// registerOAuthMetadata serves RFC 9728 OAuth Protected-Resource Metadata when
// authorization servers are configured. MCP clients use it to discover where to
// obtain tokens. When no authorization servers are set, the endpoint is omitted.
func (a *App) registerOAuthMetadata(mux *http.ServeMux) {
	if len(a.Config.OAuthAuthorizationServers) == 0 {
		return
	}
	meta := &oauthex.ProtectedResourceMetadata{
		Resource:               a.resourceURL(),
		AuthorizationServers:   a.Config.OAuthAuthorizationServers,
		ScopesSupported:        a.Config.OAuthScopes,
		BearerMethodsSupported: []string{"header"},
	}
	mux.HandleFunc("/.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		m := *meta
		m.Resource = absoluteResource(r, a.Config)
		_ = json.NewEncoder(w).Encode(m)
	})
}

func (a *App) resourceURL() string {
	host := strings.TrimPrefix(a.Config.Listen, ":")
	if host == "" {
		host = "localhost"
	}
	return "http://" + host + a.Config.Endpoint
}

// absoluteResource builds the resource identifier from the incoming request,
// falling back to the configured listen address.
func absoluteResource(r *http.Request, cfg *config.Config) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		return scheme + "://" + h + cfg.Endpoint
	}
	u := &url.URL{Scheme: scheme, Host: r.Host, Path: cfg.Endpoint}
	return u.String()
}
