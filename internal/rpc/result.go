// Package rpc holds shared argument types, validators, and result builders used
// across all tool handlers. It is the single place where input validation and
// output formatting happen, so tool handlers stay focused on Kubernetes calls.
package rpc

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ----- Result builders -----

// TextResult builds a successful text result.
func TextResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// ErrorResult builds a tool-error result (isError=true) with a human-friendly
// message. Tool errors are surfaced to the agent so it can self-correct; they
// are NOT protocol-level errors.
func ErrorResult(format string, args ...any) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf(format, args...)}},
	}
}

// JSONResult marshals v as indented JSON. If marshalling fails it falls back to
// an error result.
func JSONResult(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ErrorResult("failed to encode result as JSON: %v", err)
	}
	return TextResult(string(b))
}

// TruncateText caps a string to maxBytes and appends a visible marker when
// truncation occurred, so context windows are protected.
func TruncateText(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	omitted := len(s) - maxBytes
	// Trim back to the last valid rune boundary to avoid splitting a multi-byte
	// sequence.
	cut := maxBytes
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + fmt.Sprintf("\n…truncated (%d more bytes omitted, raise --max-output-bytes)", omitted)
}

func utf8RuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

// Errorf is a convenience to return (nil result, error) from handlers when a
// genuine protocol-level error is preferred over a tool error. Prefer
// ErrorResult for expected/application errors so agents can recover.
func Errorf(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

// JoinNonEmpty joins non-empty string parts with a separator.
func JoinNonEmpty(sep string, parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
