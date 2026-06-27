package network

import (
	"fmt"
	"sort"
	"strings"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func endpointSliceGVR() schema.GroupVersionResource {
	return schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}
}

func renderEndpointSlices(svc, ns string, items []unstructured.Unstructured) string {
	var b strings.Builder
	total := 0
	for _, us := range items {
		addresses, _, _ := unstructured.NestedStringSlice(us.Object, "endpoints")
		// endpoints is a slice of objects with addresses; flatten.
		eps, _, _ := unstructured.NestedSlice(us.Object, "endpoints")
		var allAddr []string
		for _, e := range eps {
			em, _ := e.(map[string]any)
			addrs, _ := em["addresses"].([]any)
			for _, a := range addrs {
				if s, ok := a.(string); ok {
					allAddr = append(allAddr, s)
				}
			}
		}
		_ = addresses
		total += len(allAddr)
	}
	fmt.Fprintf(&b, "EndpointSlices for service %s/%s: %d ready addresses\n\n", ns, svc, total)
	for _, us := range items {
		eps, _ := us.Object["endpoints"].([]any)
		portVals, _ := us.Object["ports"].([]any)
		ports := make([]string, 0, len(portVals))
		for _, p := range portVals {
			pm, _ := p.(map[string]any)
			port := pm["port"]
			name := pm["name"]
			ports = append(ports, fmt.Sprintf("%v/%v", port, name))
		}
		for _, e := range eps {
			em, _ := e.(map[string]any)
			addrs, _ := em["addresses"].([]any)
			ready, _ := em["ready"].(bool)
			state := "READY"
			if !ready {
				state = "NOTREADY"
			}
			for _, a := range addrs {
				fmt.Fprintf(&b, "  %s %s (%s)\n", state, a.(string), strings.Join(ports, ","))
			}
		}
	}
	if total == 0 {
		b.WriteString("\nNo ready addresses. Ensure pods exist, are Ready, and match the Service selector.\n")
	}
	return b.String()
}

func ingressClass(ing networkingv1.Ingress) string {
	if ing.Spec.IngressClassName != nil {
		return *ing.Spec.IngressClassName
	}
	return "<default>"
}

func ingressHosts(ing networkingv1.Ingress) []string {
	set := map[string]struct{}{}
	for _, r := range ing.Spec.Rules {
		if r.Host != "" {
			set[r.Host] = struct{}{}
		}
	}
	if len(set) == 0 {
		return []string{"*"}
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

func ingressAddr(ing networkingv1.Ingress) string {
	if len(ing.Status.LoadBalancer.Ingress) > 0 {
		if ing.Status.LoadBalancer.Ingress[0].IP != "" {
			return ing.Status.LoadBalancer.Ingress[0].IP
		}
		return ing.Status.LoadBalancer.Ingress[0].Hostname
	}
	return "<none>"
}

func selectorStr(s metav1.LabelSelector) string {
	if len(s.MatchLabels) == 0 && len(s.MatchExpressions) == 0 {
		return "<none>"
	}
	return mapStr(s.MatchLabels)
}

func policySummary(n int) string {
	if n == 0 {
		return "deny-all"
	}
	return fmt.Sprintf("%d rule(s)", n)
}

func mapStr(m map[string]string) string {
	if len(m) == 0 {
		return "<none>"
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ",")
}
