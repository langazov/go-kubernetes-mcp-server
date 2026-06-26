package network

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/testutil"
)

func TestListServices(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeClusterIP, ClusterIP: "10.0.0.1", Ports: []corev1.ServicePort{{Port: 80}}}},
			&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: "lb", Namespace: "default"}, Spec: corev1.ServiceSpec{Type: corev1.ServiceTypeLoadBalancer, ClusterIP: "10.0.0.2", Ports: []corev1.ServicePort{{Port: 443, NodePort: 31443}}}, Status: corev1.ServiceStatus{LoadBalancer: corev1.LoadBalancerStatus{Ingress: []corev1.LoadBalancerIngress{{IP: "203.0.113.9"}}}}},
		),
	)
	res, err := listServices(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listServices: %v", err)
	}
	out := testutil.TextOf(res)
	for _, want := range []string{"web", "lb", "203.0.113.9", "443:31443"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestGetServiceNoEndpoints(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "web"}, Ports: []corev1.ServicePort{{Port: 80}}},
		}),
	)
	res, err := getService(tk)(context.Background(), tools.NamespaceNameArgs{Name: "web"})
	if err != nil {
		t.Fatalf("getService: %v", err)
	}
	out := testutil.TextOf(res)
	// No endpoints => helpful message.
	if !strings.Contains(out, "no ready pods") && !strings.Contains(out, "none") {
		t.Errorf("expected no-endpoints message:\n%s", out)
	}
}

func TestGetServiceInvalidName(t *testing.T) {
	tk := testutil.NewToolkit(t)
	res, _ := getService(tk)(context.Background(), tools.NamespaceNameArgs{Name: ""})
	if !testutil.IsError(res) {
		t.Error("expected error for empty name")
	}
}

func TestListNetworkPolicies(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(
			&networkingv1.NetworkPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "deny-all", Namespace: "default"},
				Spec:       networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}},
			},
		),
	)
	res, err := listNetworkPolicies(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listNetworkPolicies: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "deny-all") {
		t.Errorf("missing policy:\n%s", out)
	}
}
