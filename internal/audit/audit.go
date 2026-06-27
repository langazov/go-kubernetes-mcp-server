// Package audit records every tool call as a structured log line, classifying it
// by verb (read/write/destructive/debug) and flagging mutating calls. It never
// records secret contents.
package audit

import (
	"context"
	"log/slog"
	"time"

	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
)

// key is an unexported context key carrying the in-flight audit record.
type key int

const recordKey key = 0

// Record describes a single tool invocation for audit purposes.
type Record struct {
	Tool      string         `json:"tool"`
	Verb      security.Verb  `json:"verb"`
	Kind      string         `json:"kind,omitempty"`
	Namespace string         `json:"namespace,omitempty"`
	Name      string         `json:"name,omitempty"`
	DryRun    bool           `json:"dry_run,omitempty"`
	Success   bool           `json:"success"`
	Duration  time.Duration  `json:"duration_ms"`
	Error     string         `json:"error,omitempty"`
	Args      map[string]any `json:"args,omitempty"`
	Extra     map[string]any `json:"-"`
}

// Logger wraps an slog.Logger to emit audit records.
type Logger struct {
	log *slog.Logger
}

// NewLogger builds an audit Logger.
func NewLogger(l *slog.Logger) *Logger { return &Logger{log: l} }

// Begin starts an audit record and returns a context carrying it, plus a Finish
// function to call when the tool completes.
func (l *Logger) Begin(ctx context.Context, tool string, verb security.Verb) (context.Context, func(err error)) {
	rec := &Record{Tool: tool, Verb: verb}
	ctx = context.WithValue(ctx, recordKey, rec)
	start := time.Now()
	return ctx, func(err error) {
		rec.Duration = time.Since(start)
		rec.Success = err == nil
		if err != nil {
			rec.Error = err.Error()
		}
		l.emit(rec)
	}
}

// Attach augments the active record (set on the context by Begin) with extra
// metadata (kind/namespace/name/dry-run). It is a no-op if no record is active.
func Attach(ctx context.Context, kind, namespace, name string, dryRun bool) {
	rec, ok := ctx.Value(recordKey).(*Record)
	if !ok {
		return
	}
	rec.Kind = kind
	rec.Namespace = namespace
	rec.Name = name
	rec.DryRun = dryRun
}

// AttachArgs records a redacted summary of call arguments for forensic purposes
// (e.g. the command run by exec_command, the image used by a debug pod). Never
// pass secret payloads here. It is a no-op if no record is active.
func AttachArgs(ctx context.Context, args map[string]any) {
	rec, ok := ctx.Value(recordKey).(*Record)
	if !ok {
		return
	}
	if rec.Args == nil {
		rec.Args = make(map[string]any, len(args))
	}
	for k, v := range args {
		rec.Args[k] = v
	}
}

func (l *Logger) emit(rec *Record) {
	attrs := []any{
		slog.String("tool", rec.Tool),
		slog.String("verb", string(rec.Verb)),
		slog.Bool("success", rec.Success),
		slog.Duration("duration", rec.Duration),
	}
	if rec.Kind != "" {
		attrs = append(attrs, slog.String("kind", rec.Kind))
	}
	if rec.Namespace != "" {
		attrs = append(attrs, slog.String("namespace", rec.Namespace))
	}
	if rec.Name != "" {
		attrs = append(attrs, slog.String("name", rec.Name))
	}
	if rec.DryRun {
		attrs = append(attrs, slog.Bool("dry_run", true))
	}
	if rec.Error != "" {
		attrs = append(attrs, slog.String("error", rec.Error))
	}
	if len(rec.Args) > 0 {
		attrs = append(attrs, slog.Any("args", rec.Args))
	}
	switch rec.Verb {
	case security.VerbDestructive:
		l.log.Warn("tool call (destructive)", attrs...)
	case security.VerbWrite, security.VerbDebug:
		l.log.Info("tool call (mutating)", attrs...)
	default:
		l.log.Info("tool call", attrs...)
	}
}
