package tools

import "github.com/modelcontextprotocol/go-sdk/mcp"

// ptrBool returns a pointer to b (for optional annotation fields).
func ptrBool(b bool) *bool { return &b }

// NewReadTool builds a tool annotated read-only and closed-world.
func NewReadTool(name, desc string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Description: desc,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptrBool(false)},
	}
}

// NewWriteTool builds a tool annotated mutating, non-destructive, closed-world.
func NewWriteTool(name, desc string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Description: desc,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: ptrBool(false), OpenWorldHint: ptrBool(false)},
	}
}

// NewDestructiveTool builds a tool annotated destructive, closed-world.
func NewDestructiveTool(name, desc string) *mcp.Tool {
	return &mcp.Tool{
		Name:        name,
		Description: desc,
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: false, DestructiveHint: ptrBool(true), OpenWorldHint: ptrBool(false)},
	}
}
