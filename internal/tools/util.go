package tools

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResolveList returns the effective namespace string ("" for all namespaces)
// and metav1.ListOptions for a ListArgs. When AllNamespaces is true the
// namespace is "" (lists across the cluster).
func ResolveList(a ListArgs) (string, metav1.ListOptions, error) {
	if err := validateSelector(a.Selector); err != nil {
		return "", metav1.ListOptions{}, err
	}
	ns := ""
	if !a.AllNamespaces {
		ns = ResolveNS(a.Namespace)
	}
	opts := metav1.ListOptions{
		LabelSelector:   a.Selector,
		FieldSelector:   a.FieldSelector,
		ResourceVersion: "",
	}
	if a.Limit > 0 {
		opts.Limit = a.Limit
	}
	return ns, opts, nil
}

// ResolveList enforces server policy for a namespaced list. It refuses a
// cluster-wide list while a namespace allowlist is configured (such a list
// cannot be scoped to the allowlisted subset) and applies the privileged-target
// and namespace-allowlist guards to a specific namespace. Use for every list of
// namespaced resources; cluster-scoped lists (nodes, namespaces, ...) should use
// the free ResolveList function and check CheckScope with clusterScoped=true.
func (tk *Toolkit) ResolveList(a ListArgs) (string, metav1.ListOptions, error) {
	ns, opts, err := ResolveList(a)
	if err != nil {
		return "", metav1.ListOptions{}, err
	}
	if ns == "" {
		if len(tk.Policy.Namespaces) > 0 {
			return "", metav1.ListOptions{}, fmt.Errorf("listing all namespaces is not permitted while a namespace allowlist is configured")
		}
		return ns, opts, nil
	}
	if err := tk.CheckScope(ns, false); err != nil {
		return "", metav1.ListOptions{}, err
	}
	return ns, opts, nil
}

// CheckScope enforces both the privileged-target guard and the namespace
// allowlist for a target. Pass clusterScoped=true for resources that are not
// namespaced (nodes, namespaces, PVs, clusterroles, ...).
func (tk *Toolkit) CheckScope(ns string, clusterScoped bool) error {
	if err := tk.Policy.CheckTarget(ns, clusterScoped); err != nil {
		return err
	}
	if !clusterScoped && ns != "" {
		if err := tk.Policy.CheckNamespace(ns); err != nil {
			return err
		}
	}
	return nil
}

func validateSelector(sel string) error {
	if strings.ContainsAny(sel, "\n\r") {
		return fmt.Errorf("invalid selector: must not contain newlines")
	}
	return nil
}

// ResolveNS returns the namespace to use for a namespaced operation, defaulting
// to "default" when empty. For list-across-namespaces, handle that at the call
// site (pass "" explicitly).
func ResolveNS(ns string) string {
	if ns == "" {
		return "default"
	}
	return ns
}

// AgeStr renders the elapsed time since a creation timestamp as a kubectl-style
// age (e.g. "5m", "3d", "2h").
func AgeStr(t metav1.Time) string {
	if t.IsZero() {
		return ""
	}
	return shortDuration(time.Since(t.Time))
}

func shortDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// TruncLen shortens a string to n runes with an ellipsis.
func TruncLen(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
