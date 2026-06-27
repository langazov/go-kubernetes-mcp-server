package operations

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/langazov/go-kubernetes-mcp-server/internal/audit"
	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

// ----- create_namespace -----

type createNamespaceArgs struct {
	Name   string            `json:"name" jsonschema:"the namespace name"`
	Labels map[string]string `json:"labels,omitempty" jsonschema:"optional labels for the namespace"`
	DryRun bool              `json:"dry_run,omitempty" jsonschema:"if true, validate without creating"`
}

func createNamespace(tk *tools.Toolkit) tools.ToolFunc[createNamespaceArgs] {
	return func(ctx context.Context, a createNamespaceArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := tk.Policy.CheckTarget("", true); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Namespace", "", a.Name, a.DryRun)
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: a.Name, Labels: a.Labels},
		}
		opts := metav1.CreateOptions{}
		if a.DryRun {
			opts.DryRun = []string{"All"}
		}
		if _, err := tk.Clients.Core.CoreV1().Namespaces().Create(ctx, ns, opts); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("create namespace %s", a.Name))
		}
		verb := "created"
		if a.DryRun {
			verb = "validated (dry run)"
		}
		return rpc.TextResult(fmt.Sprintf("namespace %q %s\n", a.Name, verb)), nil
	}
}

// ----- create/update configmap -----

type configMapArgs struct {
	Namespace string            `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name      string            `json:"name" jsonschema:"the configmap name"`
	Data      map[string]string `json:"data" jsonschema:"key/value string entries"`
	DryRun    bool              `json:"dry_run,omitempty" jsonschema:"if true, validate without creating"`
}

func createConfigMap(tk *tools.Toolkit) tools.ToolFunc[configMapArgs] {
	return func(ctx context.Context, a configMapArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := policyTarget(tk, ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "ConfigMap", ns, a.Name, a.DryRun)
		opts := metav1.CreateOptions{}
		if a.DryRun {
			opts.DryRun = []string{"All"}
		}
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: a.Name, Namespace: ns}, Data: a.Data}
		if _, err := tk.Clients.Core.CoreV1().ConfigMaps(ns).Create(ctx, cm, opts); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("create configmap %s/%s", ns, a.Name))
		}
		return rpc.TextResult(fmt.Sprintf("configmap %s/%s created (%d keys)%s\n", ns, a.Name, len(a.Data), dryNote(a.DryRun))), nil
	}
}

func updateConfigMap(tk *tools.Toolkit) tools.ToolFunc[configMapArgs] {
	return func(ctx context.Context, a configMapArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := policyTarget(tk, ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "ConfigMap", ns, a.Name, a.DryRun)
		opts := metav1.UpdateOptions{}
		if a.DryRun {
			opts.DryRun = []string{"All"}
		}
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: a.Name, Namespace: ns, ResourceVersion: ""}, Data: a.Data}
		// Preserve resourceVersion to avoid conflicts on full update.
		if cur, err := tk.Clients.Core.CoreV1().ConfigMaps(ns).Get(ctx, a.Name, metav1.GetOptions{}); err == nil {
			cm.ResourceVersion = cur.ResourceVersion
		}
		if _, err := tk.Clients.Core.CoreV1().ConfigMaps(ns).Update(ctx, cm, opts); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("update configmap %s/%s", ns, a.Name))
		}
		return rpc.TextResult(fmt.Sprintf("configmap %s/%s updated (%d keys)%s\n", ns, a.Name, len(a.Data), dryNote(a.DryRun))), nil
	}
}

// ----- create_secret -----

type secretArgs struct {
	Namespace  string            `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name       string            `json:"name" jsonschema:"the secret name"`
	Type       string            `json:"type,omitempty" jsonschema:"the secret type (defaults to Opaque)"`
	StringData map[string]string `json:"string_data" jsonschema:"key/value string entries (stored as secret data)"`
	DryRun     bool              `json:"dry_run,omitempty" jsonschema:"if true, validate without creating"`
}

func createSecret(tk *tools.Toolkit) tools.ToolFunc[secretArgs] {
	return func(ctx context.Context, a secretArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := policyTarget(tk, ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Secret", ns, a.Name, a.DryRun)
		// NOTE: we deliberately do NOT log StringData contents; audit only
		// records that a secret was created.
		st := corev1.SecretTypeOpaque
		if a.Type != "" {
			st = corev1.SecretType(a.Type)
		}
		opts := metav1.CreateOptions{}
		if a.DryRun {
			opts.DryRun = []string{"All"}
		}
		sec := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: a.Name, Namespace: ns},
			Type:       st,
			StringData: a.StringData,
		}
		if _, err := tk.Clients.Core.CoreV1().Secrets(ns).Create(ctx, sec, opts); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("create secret %s/%s", ns, a.Name))
		}
		keys := make([]string, 0, len(a.StringData))
		for k := range a.StringData {
			keys = append(keys, k)
		}
		return rpc.TextResult(fmt.Sprintf("secret %s/%s created (type=%s, keys=%v)%s — values are not echoed\n",
			ns, a.Name, st, keys, dryNote(a.DryRun))), nil
	}
}

func dryRunSuffix(ok bool) string {
	if ok {
		return " [dry run]"
	}
	return ""
}

// dryNote is the trailing message appended for dry-run results.
func dryNote(ok bool) string {
	if ok {
		return " (dry run)"
	}
	return ""
}

var _ = dryRunSuffix // reserved for future use

// ----- create_service -----

type createServiceArgs struct {
	Namespace  string            `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name       string            `json:"name" jsonschema:"the service name"`
	Selector   map[string]string `json:"selector" jsonschema:"label selector mapping pods to expose (e.g. {app: web})"`
	Port       int32             `json:"port" jsonschema:"the port the service exposes"`
	TargetPort int32             `json:"target_port,omitempty" jsonschema:"the container port to forward to (defaults to port)"`
	Type       string            `json:"type,omitempty" jsonschema:"service type: ClusterIP (default), NodePort, or LoadBalancer"`
	DryRun     bool              `json:"dry_run,omitempty" jsonschema:"if true, validate without creating"`
}

func createService(tk *tools.Toolkit) tools.ToolFunc[createServiceArgs] {
	return func(ctx context.Context, a createServiceArgs) (*mcp.CallToolResult, error) {
		if err := tk.Policy.CheckMutating(); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if a.Port == 0 {
			return rpc.ErrorResult("port is required"), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := policyTarget(tk, ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Service", ns, a.Name, a.DryRun)

		svcType := corev1.ServiceTypeClusterIP
		switch strings.ToUpper(a.Type) {
		case "", "CLUSTERIP":
		case "NODEPORT":
			svcType = corev1.ServiceTypeNodePort
		case "LOADBALANCER":
			svcType = corev1.ServiceTypeLoadBalancer
		default:
			return rpc.ErrorResult("invalid service type %q (use ClusterIP, NodePort, or LoadBalancer)", a.Type), nil
		}
		target := a.TargetPort
		if target == 0 {
			target = a.Port
		}

		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: a.Name, Namespace: ns},
			Spec: corev1.ServiceSpec{
				Type:     svcType,
				Selector: a.Selector,
				Ports: []corev1.ServicePort{
					{Port: a.Port, TargetPort: intstr.FromInt(int(target)), Protocol: corev1.ProtocolTCP},
				},
			},
		}
		opts := metav1.CreateOptions{}
		if a.DryRun {
			opts.DryRun = []string{"All"}
		}
		if _, err := tk.Clients.Core.CoreV1().Services(ns).Create(ctx, svc, opts); err != nil {
			return nil, tools.RPCStatusError(err, fmt.Sprintf("create service %s/%s", ns, a.Name))
		}
		return rpc.TextResult(fmt.Sprintf("service %s/%s created (type=%s, port=%d→%d)%s\n",
			ns, a.Name, svcType, a.Port, target, dryNote(a.DryRun))), nil
	}
}
