// Package configstore implements read tools for configmaps, secrets, persistent
// volume claims, and storage classes. Secret values are masked unless the server
// is started with --reveal-secrets AND the caller passes reveal=true.
package configstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

var read = security.VerbRead

// Register registers all configstore tools on s and returns the number added.
func Register(tk *tools.Toolkit, s *mcp.Server) int {
	mcp.AddTool(s, tools.NewReadTool("list_configmaps",
		"List configmaps with data key count and age."), tools.Wrap(tk, "list_configmaps", read, listConfigMaps(tk)))
	mcp.AddTool(s, tools.NewReadTool("get_configmap",
		"Get a configmap's data keys and values."), tools.Wrap(tk, "get_configmap", read, getConfigMap(tk)))
	mcp.AddTool(s, tools.NewReadTool("list_secrets",
		"List secrets with type and key names only (values are never shown in lists)."), tools.Wrap(tk, "list_secrets", read, listSecrets(tk)))
	mcp.AddTool(s, tools.NewReadTool("get_secret",
		"Get a secret's keys. Values are masked (••••) with a change-detection hash unless reveal=true and the server permits it."), tools.Wrap(tk, "get_secret", read, getSecret(tk)))
	mcp.AddTool(s, tools.NewReadTool("list_pvcs",
		"List persistent volume claims with status, capacity, storage class, and volume."), tools.Wrap(tk, "list_pvcs", read, listPVCs(tk)))
	mcp.AddTool(s, tools.NewReadTool("list_storageclasses",
		"List storage classes with provisioner, reclaim policy, and default marker."), tools.Wrap(tk, "list_storageclasses", read, listStorageClasses(tk)))

	return 6
}

func listConfigMaps(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "ConfigMap", ns, "", false)
		list, err := tk.Clients.Core.CoreV1().ConfigMaps(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list configmaps")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "DATA", "AGE")
		for _, c := range list.Items {
			t.AddRow(c.Namespace, c.Name, fmt.Sprintf("%d", len(c.Data)), tools.AgeStr(c.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func getConfigMap(tk *tools.Toolkit) tools.ToolFunc[tools.NamespaceNameArgs] {
	return func(ctx context.Context, a tools.NamespaceNameArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "ConfigMap", ns, a.Name, false)
		cm, err := tk.Clients.Core.CoreV1().ConfigMaps(ns).Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "get configmap "+ns+"/"+a.Name)
		}
		return rpc.TextResult(renderData(cm.Data, cm.BinaryData)), nil
	}
}

func listSecrets(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Secret", ns, "", false)
		list, err := tk.Clients.Core.CoreV1().Secrets(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list secrets")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "TYPE", "DATA", "AGE")
		for _, s := range list.Items {
			t.AddRow(s.Namespace, s.Name, string(s.Type), fmt.Sprintf("%d", len(s.Data)), tools.AgeStr(s.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

type secretArgs struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name      string `json:"name" jsonschema:"the secret name"`
	Reveal    bool   `json:"reveal,omitempty" jsonschema:"if true, return plaintext values (requires server --reveal-secrets)"`
}

func getSecret(tk *tools.Toolkit) tools.ToolFunc[secretArgs] {
	return func(ctx context.Context, a secretArgs) (*mcp.CallToolResult, error) {
		if err := rpc.ValidateName(a.Name); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		ns := tools.ResolveNS(a.Namespace)
		if err := tk.CheckScope(ns, false); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		if err := tk.Policy.CheckSecretReveal(a.Reveal); err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "Secret", ns, a.Name, false)
		sec, err := tk.Clients.Core.CoreV1().Secrets(ns).Get(ctx, a.Name, metav1.GetOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "get secret "+ns+"/"+a.Name)
		}
		return rpc.TextResult(renderSecret(sec, a.Reveal)), nil
	}
}

func listPVCs(tk *tools.Toolkit) tools.ToolFunc[tools.ListArgs] {
	return func(ctx context.Context, a tools.ListArgs) (*mcp.CallToolResult, error) {
		ns, opts, err := tk.ResolveList(a)
		if err != nil {
			return rpc.ErrorResult("%v", err), nil
		}
		audit.Attach(ctx, "PersistentVolumeClaim", ns, "", false)
		list, err := tk.Clients.Core.CoreV1().PersistentVolumeClaims(ns).List(ctx, opts)
		if err != nil {
			return nil, tools.RPCStatusError(err, "list pvcs")
		}
		t := rpc.NewTable("NAMESPACE", "NAME", "STATUS", "CAPACITY", "ACCESS MODES", "STORAGECLASS", "VOLUME", "AGE")
		for _, p := range list.Items {
			capStr := "<pending>"
			if p.Spec.Resources.Requests.Storage() != nil {
				capStr = p.Spec.Resources.Requests.Storage().String()
			}
			if p.Status.Capacity.Storage() != nil && !p.Status.Capacity.Storage().IsZero() {
				capStr = p.Status.Capacity.Storage().String()
			}
			acc := accessModesStr(p.Spec.AccessModes)
			sc := "<none>"
			if p.Spec.StorageClassName != nil {
				sc = *p.Spec.StorageClassName
			}
			t.AddRow(p.Namespace, p.Name, string(p.Status.Phase), capStr, acc, sc, p.Spec.VolumeName, tools.AgeStr(p.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

func listStorageClasses(tk *tools.Toolkit) tools.ToolFunc[noneArgs] {
	return func(ctx context.Context, _ noneArgs) (*mcp.CallToolResult, error) {
		audit.Attach(ctx, "StorageClass", "", "", false)
		list, err := tk.Clients.Core.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, tools.RPCStatusError(err, "list storageclasses")
		}
		t := rpc.NewTable("NAME", "PROVISIONER", "RECLAIMPOLICY", "VOLUMEBINDINGMODE", "DEFAULT", "AGE")
		for _, sc := range list.Items {
			def := "no"
			if ann := sc.Annotations; ann != nil && ann["storageclass.kubernetes.io/is-default-class"] == "true" {
				def = "yes"
			}
			policy := string(corev1.PersistentVolumeReclaimDelete)
			if sc.ReclaimPolicy != nil {
				policy = string(*sc.ReclaimPolicy)
			}
			mode := "WaitForFirstConsumer"
			if sc.VolumeBindingMode != nil {
				mode = string(*sc.VolumeBindingMode)
			}
			t.AddRow(sc.Name, sc.Provisioner, policy, mode, def, tools.AgeStr(sc.CreationTimestamp))
		}
		return rpc.TextResult(t.Render()), nil
	}
}

type noneArgs struct{}

// ----- formatting -----

func renderData(data map[string]string, binary map[string][]byte) string {
	if len(data) == 0 && len(binary) == 0 {
		return "(empty)\n"
	}
	var b strings.Builder
	keys := sortedKeys(data)
	for _, k := range keys {
		fmt.Fprintf(&b, "%s: %s\n", k, data[k])
	}
	for k := range binary {
		fmt.Fprintf(&b, "%s: <binary, %d bytes>\n", k, len(binary[k]))
	}
	return b.String()
}

func renderSecret(s *corev1.Secret, reveal bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name:       %s\n", s.Name)
	fmt.Fprintf(&b, "Namespace:  %s\n", s.Namespace)
	fmt.Fprintf(&b, "Type:       %s\n", s.Type)
	if len(s.Data) == 0 {
		b.WriteString("Data:       <empty>\n")
		return b.String()
	}
	b.WriteString("\nData:\n")
	keys := sortedStringKeys(s.Data)
	for _, k := range keys {
		v := s.Data[k]
		if reveal {
			fmt.Fprintf(&b, "  %s: %s\n", k, string(v))
		} else {
			fmt.Fprintf(&b, "  %s: •••• (sha256:%s, %d bytes)\n", k, hash12(v), len(v))
		}
	}
	if !reveal {
		b.WriteString("\nValues are masked. Pass reveal=true (requires server --reveal-secrets) to show plaintext.\n")
	}
	return b.String()
}

func hash12(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:12]
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedStringKeys(m map[string][]byte) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func accessModesStr(modes []corev1.PersistentVolumeAccessMode) string {
	if len(modes) == 0 {
		return "<none>"
	}
	out := make([]string, 0, len(modes))
	for _, m := range modes {
		switch m {
		case corev1.ReadWriteOnce:
			out = append(out, "RWO")
		case corev1.ReadOnlyMany:
			out = append(out, "ROX")
		case corev1.ReadWriteMany:
			out = append(out, "RWX")
		case corev1.ReadWriteOncePod:
			out = append(out, "RWOP")
		default:
			out = append(out, string(m))
		}
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}
