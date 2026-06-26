package core

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/langazov/go-kubernetes-mcp-server/internal/rpc"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
)

func describeNamespace(ns *corev1.Namespace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name:         %s\n", ns.Name)
	fmt.Fprintf(&b, "Status:       %s\n", ns.Status.Phase)
	fmt.Fprintf(&b, "Age:          %s\n", tools.AgeStr(ns.CreationTimestamp))
	if len(ns.Labels) > 0 {
		fmt.Fprintf(&b, "Labels:       %s\n", formatMap(ns.Labels))
	}
	if len(ns.Annotations) > 0 {
		fmt.Fprintf(&b, "Annotations:  %s\n", formatMap(ns.Annotations))
	}
	return b.String()
}

func describeNodeObject(n *corev1.Node) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name:               %s\n", n.Name)
	fmt.Fprintf(&b, "Status:             %s\n", nodeStatus(*n))
	fmt.Fprintf(&b, "Roles:              %s\n", nodeRoles(*n))
	fmt.Fprintf(&b, "Age:                %s\n", tools.AgeStr(n.CreationTimestamp))
	fmt.Fprintf(&b, "Version:            %s\n", nodeVersion(*n))
	fmt.Fprintf(&b, "Internal IP:        %s\n", nodeInternalIP(n))
	fmt.Fprintf(&b, "OS/Image:           %s / %s\n", n.Status.NodeInfo.OperatingSystem, n.Status.NodeInfo.OSImage)
	fmt.Fprintf(&b, "Kernel:             %s\n", n.Status.NodeInfo.KernelVersion)
	fmt.Fprintf(&b, "Container runtime:  %s\n", n.Status.NodeInfo.ContainerRuntimeVersion)

	if len(n.Spec.Taints) > 0 {
		fmt.Fprintf(&b, "Taints:             ")
		for i, t := range n.Spec.Taints {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s=%s:%s", t.Key, t.Value, t.Effect)
		}
		b.WriteString("\n")
	} else {
		fmt.Fprintf(&b, "Taints:             <none>\n")
	}

	b.WriteString("\nConditions:\n")
	t := rpc.NewTable("TYPE", "STATUS", "REASON", "MESSAGE")
	for _, c := range n.Status.Conditions {
		t.AddRow(string(c.Type), string(c.Status), c.Reason, tools.TruncLen(c.Message, 70))
	}
	b.WriteString(t.Render())

	b.WriteString("\nAllocatable / Capacity:\n")
	ac := rpc.NewTable("RESOURCE", "CAPACITY", "ALLOCATABLE")
	keys := sortedKeys(mergeKeys(n.Status.Capacity, n.Status.Allocatable))
	for _, k := range keys {
		cap, alc := n.Status.Capacity[k], n.Status.Allocatable[k]
		ac.AddRow(string(k), cap.String(), alc.String())
	}
	b.WriteString(ac.Render())
	return b.String()
}

func nodeStatus(n corev1.Node) string {
	ready := ""
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			if c.Status == corev1.ConditionTrue {
				ready = "Ready"
			} else {
				ready = "NotReady"
			}
		}
	}
	if ready == "" {
		ready = "Unknown"
	}
	// Surface any non-ready conditions as extra states.
	var extra []string
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			continue
		}
		if c.Status == corev1.ConditionTrue {
			extra = append(extra, string(c.Type))
		}
	}
	if len(extra) > 0 {
		return ready + "," + strings.Join(extra, ",")
	}
	return ready
}

func nodeRoles(n corev1.Node) string {
	roles := sets.New[string]()
	for k := range n.Labels {
		if strings.HasPrefix(k, "node-role.kubernetes.io/") {
			roles.Insert(strings.TrimPrefix(k, "node-role.kubernetes.io/"))
		}
	}
	if roles.Len() == 0 {
		return "<none>"
	}
	return strings.Join(sets.List(roles), ",")
}

func nodeVersion(n corev1.Node) string {
	if n.Status.NodeInfo.KubeletVersion == "" {
		return "<none>"
	}
	return n.Status.NodeInfo.KubeletVersion
}

func nodeInternalIP(n *corev1.Node) string {
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			return a.Address
		}
	}
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeExternalIP {
			return a.Address
		}
	}
	return "<none>"
}

func ptrStr(p *string) string {
	if p == nil {
		return "<none>"
	}
	return *p
}

func formatMap(m map[string]string) string {
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

func mergeKeys(a, b corev1.ResourceList) []corev1.ResourceName {
	set := sets.New[corev1.ResourceName]()
	for k := range a {
		set.Insert(k)
	}
	for k := range b {
		set.Insert(k)
	}
	return sets.List(set)
}

func sortedKeys(s []corev1.ResourceName) []corev1.ResourceName {
	out := append([]corev1.ResourceName(nil), s...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// eventTime returns the best timestamp for an event.
func eventTime(e corev1.Event) metav1.Time {
	if e.LastTimestamp.IsZero() {
		return e.CreationTimestamp
	}
	return e.LastTimestamp
}
