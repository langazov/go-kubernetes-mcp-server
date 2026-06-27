package tools

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// ResolveGVR finds the GroupVersionResource (and namespaced flag) for a kind
// using discovery. If apiVersion is given ("group/version" or "v1") it
// restricts the search to that group/version. Returns an error if not found.
func ResolveGVR(_ context.Context, tk *Toolkit, kind, apiVersion string) (schema.GroupVersionResource, bool, error) {
	_, lists, err := tk.Clients.Discovery.ServerGroupsAndResources()
	if err != nil && len(lists) == 0 {
		return schema.GroupVersionResource{}, false, fmt.Errorf("discovery failed: %w", err)
	}
	wantGroup, wantVersion := "", ""
	if apiVersion != "" {
		if i := strings.Index(apiVersion, "/"); i >= 0 {
			wantGroup = apiVersion[:i]
			wantVersion = apiVersion[i+1:]
		} else {
			wantVersion = apiVersion
		}
	}
	for _, rl := range lists {
		group, version := splitGroupVersion(rl.GroupVersion)
		if wantVersion != "" && version != wantVersion {
			continue
		}
		if wantGroup != "" && group != wantGroup {
			continue
		}
		for _, r := range rl.APIResources {
			if !strings.EqualFold(r.Kind, kind) {
				continue
			}
			if strings.Contains(r.Name, "/") { // skip subresources
				continue
			}
			return schema.GroupVersionResource{Group: group, Version: version, Resource: r.Name}, r.Namespaced, nil
		}
	}
	return schema.GroupVersionResource{}, false, fmt.Errorf("kind %q (apiVersion %q) not found; call get_api_resources to list available kinds", kind, apiVersion)
}

func splitGroupVersion(gv string) (string, string) {
	if i := strings.Index(gv, "/"); i >= 0 {
		return gv[:i], gv[i+1:]
	}
	return "", gv
}

var _ metav1.APIResource
