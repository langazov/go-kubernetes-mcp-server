// Package kube builds and holds the Kubernetes client interfaces used by tools.
package kube

import (
	"fmt"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Clients bundles all Kubernetes client interfaces.
type Clients struct {
	Core       kubernetes.Interface // typed clientset for core/batch/apps/networking/storage
	Dynamic    dynamic.Interface    // generic client for any GVK (CRDs included)
	Discovery  discovery.DiscoveryInterface
	Metrics    metricsv.Interface
	RESTConfig *rest.Config
}

// NewClients constructs a Clients bundle from a rest.Config.
func NewClients(cfg *rest.Config) (*Clients, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil rest config")
	}

	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build typed clientset: %w", err)
	}

	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build dynamic client: %w", err)
	}

	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build discovery client: %w", err)
	}

	metrics, err := metricsv.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build metrics client: %w", err)
	}

	return &Clients{
		Core:       typed,
		Dynamic:    dyn,
		Discovery:  disc,
		Metrics:    metrics,
		RESTConfig: cfg,
	}, nil
}
