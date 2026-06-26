package tools

// NamespaceNameArgs is the common shape for "get one namespaced resource" tools.
type NamespaceNameArgs struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"the namespace (defaults to 'default')"`
	Name      string `json:"name" jsonschema:"the resource name"`
}

// ListArgs is the common shape for list tools.
type ListArgs struct {
	Namespace     string `json:"namespace,omitempty" jsonschema:"the namespace (omit or empty for all namespaces)"`
	Selector      string `json:"selector,omitempty" jsonschema:"a Kubernetes label selector to filter results"`
	FieldSelector string `json:"field_selector,omitempty" jsonschema:"a Kubernetes field selector to filter results"`
	AllNamespaces bool   `json:"all_namespaces,omitempty" jsonschema:"if true, list across all namespaces"`
	Limit         int64  `json:"limit,omitempty" jsonschema:"maximum number of results to return (0 = server default)"`
}

// noArgs is the empty argument object for parameterless tools.
type noArgs struct{}
