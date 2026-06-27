package mcpserver

import (
	"context"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// OTelMiddleware returns an MCP receiving middleware that wraps each method
// handler in an OpenTelemetry span. The tool/call method additionally records
// the tool name and whether it errored.
func OTelMiddleware(tracer trace.Tracer, _ *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx, span := tracer.Start(ctx, spanName(method),
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(attribute.String("mcp.method", method)),
			)
			defer span.End()

			if tc, ok := req.(*mcp.CallToolRequest); ok {
				if tc.Params != nil {
					span.SetAttributes(attribute.String("mcp.tool", tc.Params.Name))
				}
			}

			result, err := next(ctx, method, req)
			if err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
			} else if result != nil {
				if cr, ok := result.(*mcp.CallToolResult); ok && cr != nil && cr.IsError {
					span.SetStatus(codes.Error, "tool error")
					span.SetAttributes(attribute.Bool("mcp.tool.is_error", true))
				} else {
					span.SetStatus(codes.Ok, "")
				}
			}
			return result, err
		}
	}
}

func spanName(method string) string {
	switch method {
	case "tools/call", "tools/list", "initialize", "resources/read", "prompts/get":
		return "mcp." + method
	default:
		return "mcp." + method
	}
}

// SetTracer installs tracing middleware on the server. It is a no-op when the
// tracer is the global no-op tracer (i.e. --otel-endpoint was not set).
func (a *App) SetTracer(tracer trace.Tracer) {
	if tracer == nil {
		return
	}
	a.Server.AddReceivingMiddleware(OTelMiddleware(tracer, a.Log))
}
