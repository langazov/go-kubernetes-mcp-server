// Package testutil provides shared helpers for building a *tools.Toolkit backed
// by fake Kubernetes clients, so every tool package's tests can exercise
// handlers against an in-memory API server.
package testutil

import (
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/kubernetes/scheme"
	k8stesting "k8s.io/client-go/testing"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/kube"
	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

// Option configures the fake Toolkit.
type Option func(*builder)

type builder struct {
	typedObjs    []runtime.Object
	dynamicObjs  []runtime.Object
	metricsObjs  []runtime.Object
	resources    []*metav1.APIResourceList
	mutateConfig func(*config.Config)
}

// WithObjs seeds the typed clientset with objects (pods, deployments, ...).
func WithObjs(objs ...runtime.Object) Option {
	return func(b *builder) { b.typedObjs = append(b.typedObjs, objs...) }
}

// WithDynamicObjs seeds the dynamic client (used by describe/apply/delete).
func WithDynamicObjs(objs ...runtime.Object) Option {
	return func(b *builder) { b.dynamicObjs = append(b.dynamicObjs, objs...) }
}

// WithMetricsObjs seeds the metrics clientset (NodeMetrics/PodMetrics).
func WithMetricsObjs(objs ...runtime.Object) Option {
	return func(b *builder) { b.metricsObjs = append(b.metricsObjs, objs...) }
}

// WithResources primes discovery with API resource lists (GVR/kind metadata).
func WithResources(r []*metav1.APIResourceList) Option {
	return func(b *builder) { b.resources = append(b.resources, r...) }
}

// WithConfig mutates the config before the Toolkit is built (e.g. to enable
// --allow-writes for mutating-handler tests).
func WithConfig(fn func(*config.Config)) Option {
	return func(b *builder) { b.mutateConfig = fn }
}

// NewToolkit builds a Toolkit wired to fake clients. A default discovery
// resource list (pods/deployments/services/configmaps/nodes) is always included.
func NewToolkit(t *testing.T, opts ...Option) *tools.Toolkit {
	t.Helper()
	b := &builder{mutateConfig: func(*config.Config) {}}
	for _, o := range opts {
		o(b)
	}

	cs := kubefake.NewSimpleClientset(b.typedObjs...)
	cs.Resources = append(defaultResources(), b.resources...)

	sch := runtime.NewScheme()
	if err := scheme.AddToScheme(sch); err != nil {
		t.Fatalf("build scheme: %v", err)
	}
	dyn := dynamicfake.NewSimpleDynamicClient(sch, b.dynamicObjs...)
	metrics := metricsfake.NewSimpleClientset(b.metricsObjs...)

	clients := &kube.Clients{
		Core:      cs,
		Dynamic:   dyn,
		Metrics:   metrics,
		Discovery: cs.Discovery(),
	}

	cfg := config.Defaults()
	b.mutateConfig(&cfg)

	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	return &tools.Toolkit{
		Clients: clients,
		Policy:  security.FromConfig(cfg),
		Cfg:     &cfg,
		Audit:   audit.NewLogger(discard),
		Log:     discard,
	}
}

// DefaultClients exposes the underlying fake clients for tests that need to
// assert on created/deleted objects.
type DefaultClients struct {
	Typed   *kubefake.Clientset
	Dynamic *dynamicfake.FakeDynamicClient
	Metrics *metricsfake.Clientset
}

// ClientsFor returns the concrete fake clients used by a Toolkit, for assertions.
func ClientsFor(tk *tools.Toolkit) DefaultClients {
	return DefaultClients{
		Typed:   tk.Clients.Core.(*kubefake.Clientset),
		Dynamic: tk.Clients.Dynamic.(*dynamicfake.FakeDynamicClient),
		Metrics: tk.Clients.Metrics.(*metricsfake.Clientset),
	}
}

// RegisterScaleReactors wires fake reactors for the "deployments/scale",
// "statefulsets/scale", and "replicasets/scale" subresources, which the fake
// clientset does not implement by default. It maps scale get/update onto the
// controller's spec.replicas.
func RegisterScaleReactors(typed *kubefake.Clientset) {
	scaleGet := func(resource string) k8stesting.ReactionFunc {
		return func(a k8stesting.Action) (bool, runtime.Object, error) {
			ga, ok := a.(k8stesting.GetAction)
			if !ok {
				return false, nil, nil
			}
			obj, err := typed.Tracker().Get(schemaFor(resource), ga.GetNamespace(), ga.GetName())
			if err != nil {
				return true, nil, err
			}
			replicas := int32(0)
			switch v := obj.(type) {
			case *appsv1.Deployment:
				if v.Spec.Replicas != nil {
					replicas = *v.Spec.Replicas
				}
			case *appsv1.StatefulSet:
				if v.Spec.Replicas != nil {
					replicas = *v.Spec.Replicas
				}
			case *appsv1.ReplicaSet:
				if v.Spec.Replicas != nil {
					replicas = *v.Spec.Replicas
				}
			}
			return true, &autoscalingv1.Scale{
				ObjectMeta: metav1.ObjectMeta{Name: ga.GetName(), Namespace: ga.GetNamespace()},
				Spec:       autoscalingv1.ScaleSpec{Replicas: replicas},
			}, nil
		}
	}
	scaleUpdate := func(resource string) k8stesting.ReactionFunc {
		return func(a k8stesting.Action) (bool, runtime.Object, error) {
			ua, ok := a.(k8stesting.UpdateAction)
			if !ok {
				return false, nil, nil
			}
			sc, ok := ua.GetObject().(*autoscalingv1.Scale)
			if !ok {
				return false, nil, nil
			}
			obj, err := typed.Tracker().Get(schemaFor(resource), ua.GetNamespace(), sc.Name)
			if err != nil {
				return true, nil, err
			}
			switch v := obj.(type) {
			case *appsv1.Deployment:
				v.Spec.Replicas = &sc.Spec.Replicas
				_ = typed.Tracker().Update(schemaFor(resource), v, ua.GetNamespace())
			case *appsv1.StatefulSet:
				v.Spec.Replicas = &sc.Spec.Replicas
				_ = typed.Tracker().Update(schemaFor(resource), v, ua.GetNamespace())
			case *appsv1.ReplicaSet:
				v.Spec.Replicas = &sc.Spec.Replicas
				_ = typed.Tracker().Update(schemaFor(resource), v, ua.GetNamespace())
			}
			return true, sc, nil
		}
	}
	for _, r := range []string{"deployments", "statefulsets", "replicasets"} {
		typed.PrependReactor("get", r+"/scale", scaleGet(r))
		typed.PrependReactor("update", r+"/scale", scaleUpdate(r))
	}
}

// RegisterApplyReactor stubs the fake dynamic client's patch/apply reaction for
// a resource, since the default reactor cannot apply an unstructured patch. The
// stub returns the object encoded in the patch.
func RegisterApplyReactor(dyn *dynamicfake.FakeDynamicClient, resource string) {
	dyn.PrependReactor("patch", resource, func(a k8stesting.Action) (bool, runtime.Object, error) {
		pa, ok := a.(k8stesting.PatchAction)
		if !ok {
			return false, nil, nil
		}
		obj := &unstructured.Unstructured{}
		if err := json.Unmarshal(pa.GetPatch(), obj); err != nil {
			return true, nil, err
		}
		return true, obj, nil
	})
}

func schemaFor(resource string) schema.GroupVersionResource {
	switch resource {
	case "deployments", "statefulsets", "replicasets":
		return schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: resource}
	case "pods", "services", "configmaps", "secrets":
		return schema.GroupVersionResource{Version: "v1", Resource: resource}
	case "namespaces", "nodes":
		return schema.GroupVersionResource{Version: "v1", Resource: resource}
	}
	return schema.GroupVersionResource{Resource: resource}
}

// TextOf extracts the concatenated text of every TextContent in a result.
func TextOf(res *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

// IsError reports whether the result is a tool error.
func IsError(res *mcp.CallToolResult) bool { return res != nil && res.IsError }

// defaultResources returns the discovery metadata for the most common built-ins,
// so resolveGVR works out of the box in tests.
func defaultResources() []*metav1.APIResourceList {
	core := func(name, kind string, namespaced bool, verbs ...string) metav1.APIResource {
		return metav1.APIResource{Name: name, Kind: kind, Namespaced: namespaced, Verbs: verbs}
	}
	verbs := []string{"get", "list", "watch", "create", "update", "patch", "delete"}
	return []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				core("pods", "Pod", true, verbs...),
				core("namespaces", "Namespace", false, verbs...),
				core("nodes", "Node", false, "get", "list", "watch", "patch"),
				core("services", "Service", true, verbs...),
				core("configmaps", "ConfigMap", true, verbs...),
				core("secrets", "Secret", true, verbs...),
				core("persistentvolumeclaims", "PersistentVolumeClaim", true, verbs...),
			},
		},
		{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{
				core("deployments", "Deployment", true, verbs...),
				core("statefulsets", "StatefulSet", true, verbs...),
				core("daemonsets", "DaemonSet", true, verbs...),
				core("replicasets", "ReplicaSet", true, verbs...),
			},
		},
		{
			GroupVersion: "batch/v1",
			APIResources: []metav1.APIResource{
				core("jobs", "Job", true, verbs...),
				core("cronjobs", "CronJob", true, verbs...),
			},
		},
	}
}

// Compile-time interface checks.
var (
	_ kubernetes.Interface         = (*kubefake.Clientset)(nil)
	_ dynamic.Interface            = (*dynamicfake.FakeDynamicClient)(nil)
	_ discovery.DiscoveryInterface = (discovery.DiscoveryInterface)(nil)
)
