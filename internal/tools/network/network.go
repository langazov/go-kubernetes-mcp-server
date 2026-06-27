// Package network implements read tools for services, endpoints, ingresses, and
// network policies.
package network

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

// Register registers all network tools on s and returns the number added.
func Register(tk *tools.Toolkit, s *mcp.Server) int {
	mcp.AddTool(s, tools.NewReadTool("list_services",
		"List services with type, cluster/external IPs, ports, and age."), tools.Wrap(tk, "list_services", read, listServices(tk)))
	mcp.AddTool(s, tools.NewReadTool("get_service",
		"Get full details of a service including selectors, ports, and endpoints."), tools.Wrap(tk, "get_service", read, getService(tk)))
	mcp.AddTool(s, tools.NewReadTool("get_endpoints",
		"Show the backing endpoints (ready IPs/ports) for a service, useful when a Service has no endpoints."), tools.Wrap(tk, "get_endpoints", read, getEndpoints(tk)))
	mcp.AddTool(s, tools.NewReadTool("list_ingresses",
		"List ingresses with hosts, paths, and address."), tools.Wrap(tk, "list_ingresses", read, listIngresses(tk)))
	mcp.AddTool(s, tools.NewReadTool("list_networkpolicies",
		"List network policies and their ingress/egress coverage."), tools.Wrap(tk, "list_networkpolicies", read, listNetworkPolicies(tk)))

	registerConnectivity(tk, s)

	return 6
}

var read = security.VerbRead

func listServices(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Service", ns, "", false)
		list, err := tk.Clients.Core.CoreV1().Services(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list services")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "TYPE", "CLUSTER-IP", "EXTERNAL-IP", "PORT(S)", "AGE")
		for _, s := range list.Items {
			t.AddRow(s.Namespace, s.Name, string(s.Spec.Type), firstOrNone(s.Spec.ClusterIPs), externalIPs(s), portsStr(s), tools.AgeStr(s.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func getService(tk *tools.Toolkit) tools.ToolFunc[tools.NamespaceNameArgs] {
	return func(ctx context.Context, a tools.NamespaceNameArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Service", ns, a.Name, false)
		svc, err := tk.Clients.Core.CoreV1().Services(ns).Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "get service "+ns+"/"+a.Name)
		}
		// Also fetch endpoints for convenience.
		ep, _ := tk.Clients.Core.CoreV1().Endpoints(ns).Get(ctx, a.Name, metav1.GetOptions{})
		return rpc.TextResult(describeService(svc, ep)), nil
	}
}

func getEndpoints(tk *tools.Toolkit) tools.ToolFunc[tools.NamespaceNameArgs] {
	return func(ctx context.Context, a tools.NamespaceNameArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Endpoints", ns, a.Name, false)
		// Try EndpointSlices first (group discovery.networking.k8s.io).
		slices, err := tk.Clients.Dynamic.Resource(endpointSliceGVR()).Namespace(ns).List(ctx, metav1.ListOptions{
			LabelSelector: "kubernetes.io/service-name=" + a.Name,
		})
		if err == nil && len(slices.Items) > 0 {
			return rpc.TextResult(renderEndpointSlices(a.Name, ns, slices.Items)), nil
		}
		ep, err := tk.Clients.Core.CoreV1().Endpoints(ns).Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "get endpoints "+ns+"/"+a.Name)
		}
		return rpc.TextResult(renderEndpoints(ep)), nil
	}
}

func listIngresses(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Ingress", ns, "", false)
		list, err := tk.Clients.Core.NetworkingV1().Ingresses(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list ingresses")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "CLASS", "HOSTS", "ADDRESS", "PORTS", "AGE")
		for _, ing := range list.Items {
			t.AddRow(ing.Namespace, ing.Name, ingressClass(ing), strings.Join(ingressHosts(ing), ","), ingressAddr(ing), "80", tools.AgeStr(ing.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func listNetworkPolicies(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "NetworkPolicy", ns, "", false)
		list, err := tk.Clients.Core.NetworkingV1().NetworkPolicies(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list networkpolicies")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "POD-SELECTOR", "INGRESS", "EGRESS", "AGE")
		for _, p := range list.Items {
			t.AddRow(p.Namespace, p.Name, selectorStr(p.Spec.PodSelector), policySummary(len(p.Spec.Ingress)), policySummary(len(p.Spec.Egress)), tools.AgeStr(p.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

// ----- formatting -----

func firstOrNone(ips []string) string {
	if len(ips) == 0 || ips[0] == "" {
		return "<none>"
	}
	return ips[0]
}

func externalIPs(s corev1.Service) string {
	if len(s.Status.LoadBalancer.Ingress) > 0 {
		if s.Status.LoadBalancer.Ingress[0].IP != "" {
			return s.Status.LoadBalancer.Ingress[0].IP
		}
		return s.Status.LoadBalancer.Ingress[0].Hostname
	}
	if len(s.Spec.ExternalIPs) > 0 {
		return strings.Join(s.Spec.ExternalIPs, ",")
	}
	return "<none>"
}

func portsStr(s corev1.Service) string {
	var parts []string
	for _, p := range s.Spec.Ports {
		if p.NodePort != 0 {
			parts = append(parts, fmt.Sprintf("%d:%d/%s", p.Port, p.NodePort, p.Protocol))
		} else {
			parts = append(parts, fmt.Sprintf("%d/%s", p.Port, p.Protocol))
		}
	}
	if len(parts) == 0 {
		return "<none>"
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

func describeService(s *corev1.Service, ep *corev1.Endpoints) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name:              %s\n", s.Name)
	fmt.Fprintf(&b, "Namespace:         %s\n", s.Namespace)
	fmt.Fprintf(&b, "Type:              %s\n", s.Spec.Type)
	fmt.Fprintf(&b, "Cluster IP:        %s\n", firstOrNone(s.Spec.ClusterIPs))
	fmt.Fprintf(&b, "External IPs:      %s\n", externalIPs(*s))
	fmt.Fprintf(&b, "Ports:             %s\n", portsStr(*s))
	if len(s.Spec.Selector) > 0 {
		fmt.Fprintf(&b, "Selector:          %s\n", mapStr(s.Spec.Selector))
	} else {
		fmt.Fprintf(&b, "Selector:          <none>\n")
	}
	fmt.Fprintf(&b, "Age:               %s\n", tools.AgeStr(s.CreationTimestamp))
	if ep != nil && len(ep.Subsets) > 0 {
		b.WriteString("\nEndpoints:\n")
		for _, sub := range ep.Subsets {
			for _, a := range sub.Addresses {
				for _, p := range sub.Ports {
					fmt.Fprintf(&b, "  %s:%d (%s)\n", ipOf(a), p.Port, p.Name)
				}
			}
		}
	} else {
		b.WriteString("\nEndpoints:         <none> (no ready pods match the selector)\n")
	}
	return b.String()
}

func renderEndpoints(ep *corev1.Endpoints) string {
	if ep == nil {
		return "No endpoints found.\n"
	}
	var b strings.Builder
	ready, notReady := 0, 0
	for _, sub := range ep.Subsets {
		ready += len(sub.Addresses)
		notReady += len(sub.NotReadyAddresses)
	}
	fmt.Fprintf(&b, "Endpoints for %s/%s: %d ready, %d not ready\n\n", ep.Namespace, ep.Name, ready, notReady)
	for _, sub := range ep.Subsets {
		for _, a := range sub.Addresses {
			for _, p := range sub.Ports {
				fmt.Fprintf(&b, "  READY   %s:%d (%s)\n", ipOf(a), p.Port, p.Name)
			}
		}
		for _, a := range sub.NotReadyAddresses {
			for _, p := range sub.Ports {
				fmt.Fprintf(&b, "  UNREADY %s:%d (%s)\n", ipOf(a), p.Port, p.Name)
			}
		}
	}
	if ready == 0 {
		b.WriteString("\nNo ready endpoints. Check that pods exist, are Ready, and match this Service's selector.\n")
	}
	return b.String()
}

func ipOf(a corev1.EndpointAddress) string {
	if a.IP == "" {
		return "<none>"
	}
	return a.IP
}
