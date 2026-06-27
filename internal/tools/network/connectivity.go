package network

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

func registerConnectivity(tk *tools.Toolkit, s *mcp.Server) {
	mcp.AddTool(s, tools.NewReadTool("check_connectivity",
		"Probe a service's network health: resolve its cluster DNS name, list ready endpoints, and optionally TCP-dial a port from the server's network namespace. Does not send application traffic."),
		tools.Wrap(tk, "check_connectivity", read, checkConnectivity(tk)))
}

type connectivityArgs struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Service   string `json:"service" jsonschema:"the service name to probe"`
	Port      int    `json:"port,omitempty" jsonschema:"optional TCP port to dial against each ready endpoint (e.g. 80)"`
	Timeout   string `json:"timeout,omitempty" jsonschema:"dial timeout per endpoint (e.g. 3s; default 3s)"`
}

func checkConnectivity(tk *tools.Toolkit) tools.ToolFunc[connectivityArgs] {
	return func(ctx context.Context, a connectivityArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Service); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Service", ns, a.Service, false)

		// 1. Service exists?
		svc, err := tk.Clients.Core.CoreV1().Services(ns).Get(ctx, a.Service, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("get service %s/%s", ns, a.Service))
		}

		var b strings.Builder
		fmt.Fprintf(&b, "Service %s/%s (type=%s)\n", ns, a.Service, svc.Spec.Type)
		if len(svc.Spec.Selector) == 0 && svc.Spec.Type != corev1.ServiceTypeExternalName {
			fmt.Fprintf(&b, "⚠ service has no selector and is not ExternalName; it will not get endpoints from pods\n")
		}

		// 2. DNS resolution (cluster DNS name).
		dnsName := fmt.Sprintf("%s.%s.svc.cluster.local", a.Service, ns)
		ips, dnsErr := net.LookupIP(dnsName)
		if dnsErr != nil {
			fmt.Fprintf(&b, "DNS: ✗ could not resolve %s (%v)\n", dnsName, dnsErr)
		} else {
			ipStrs := make([]string, 0, len(ips))
			for _, ip := range ips {
				ipStrs = append(ipStrs, ip.String())
			}
			fmt.Fprintf(&b, "DNS: ✓ %s → %s\n", dnsName, strings.Join(ipStrs, ", "))
		}

		// 3. Ready endpoints (via EndpointSlices, fall back to Endpoints).
		ready := readyEndpoints(ctx, tk, ns, a.Service)
		if ready == 0 {
			fmt.Fprintf(&b, "Endpoints: ✗ no ready endpoints. Check that pods exist, are Ready, and match the selector %s\n", mapStr(svc.Spec.Selector))
		} else {
			fmt.Fprintf(&b, "Endpoints: ✓ %d ready address(es)\n", ready)
		}

		// 4. Optional TCP dial to the service IP/port from the server's view.
		if a.Port > 0 && len(svc.Spec.ClusterIPs) > 0 && svc.Spec.ClusterIPs[0] != "" {
			dur := 3 * time.Second
			if a.Timeout != "" {
				if d, err := time.ParseDuration(a.Timeout); err == nil && d > 0 {
					dur = d
				}
			}
			addr := fmt.Sprintf("%s:%d", svc.Spec.ClusterIPs[0], a.Port)
			d := net.Dialer{Timeout: dur}
			if conn, err := d.DialContext(ctx, "tcp", addr); err == nil {
				_ = conn.Close()
				fmt.Fprintf(&b, "TCP dial %s: ✓ connected\n", addr)
			} else {
				fmt.Fprintf(&b, "TCP dial %s: ✗ %v\n", addr, err)
			}
		}

		// Verdict.
		healthy := (dnsErr == nil) && ready > 0
		b.WriteString("\n")
		if healthy {
			fmt.Fprintf(&b, "VERDICT: service %s/%s looks healthy.\n", ns, a.Service)
		} else {
			fmt.Fprintf(&b, "VERDICT: service %s/%s has connectivity problems (see above).\n", ns, a.Service)
		}
		return rpc.TextResult(b.String()), nil
	}
}

func readyEndpoints(ctx context.Context, tk *tools.Toolkit, ns, svc string) int {
	// Prefer EndpointSlices.
	slices, err := tk.Clients.Dynamic.Resource(endpointSliceGVR()).Namespace(ns).List(ctx, metav1.ListOptions{
		LabelSelector: "kubernetes.io/service-name=" + svc,
	})
	if err == nil {
		count := 0
		for _, us := range slices.Items {
			eps, _ := us.Object["endpoints"].([]any)
			for _, e := range eps {
				em, _ := e.(map[string]any)
				ready, _ := em["ready"].(bool)
				if ready {
					count++
				}
			}
		}
		return count
	}
	// Fall back to v1 Endpoints.
	ep, err := tk.Clients.Core.CoreV1().Endpoints(ns).Get(ctx, svc, metav1.GetOptions{})
	if err != nil {
		return 0
	}
	count := 0
	for _, sub := range ep.Subsets {
		count += len(sub.Addresses)
	}
	return count
}
