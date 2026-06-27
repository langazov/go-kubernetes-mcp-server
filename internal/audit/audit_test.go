package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"log/slog"

	"github.com/langazov/go-kubernetes-mcp-server/internal/security"
)

// runEmit drives Begin -> mutate -> Finish and returns the single emitted JSON
// audit record parsed into a map. It fails the test if nothing or more than one
// record is emitted.
func runEmit(t *testing.T, verb security.Verb, mutate func(context.Context), finishErr error) map[string]any {
	t.Helper()
	var buf bytes.Buffer
	logger := NewLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	ctx, finish := logger.Begin(context.Background(), "exec_command", verb)
	if mutate != nil {
		mutate(ctx)
	}
	finish(finishErr)
	if buf.Len() == 0 {
		t.Fatalf("expected an audit record, got no output")
	}
	var out map[string]any
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal audit output %q: %v", buf.String(), err)
	}
	return out
}

func recordOf(ctx context.Context) *Record {
	return ctx.Value(recordKey).(*Record)
}

func TestBeginCarriesRecordOnContext(t *testing.T) {
	logger := NewLogger(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	ctx, finish := logger.Begin(context.Background(), "get_pod", security.VerbRead)
	defer finish(nil)
	if recordOf(ctx) == nil {
		t.Fatal("Begin must attach a Record to the returned context")
	}
	if got := recordOf(ctx).Tool; got != "get_pod" {
		t.Errorf("record.Tool = %q, want get_pod", got)
	}
	if got := recordOf(ctx).Verb; got != security.VerbRead {
		t.Errorf("record.Verb = %q, want %q", got, security.VerbRead)
	}
}

func TestBeginContextDerivedFromParent(t *testing.T) {
	logger := NewLogger(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	parent, cancel := context.WithCancel(context.Background())
	ctx, finish := logger.Begin(parent, "get_pod", security.VerbRead)
	defer finish(nil)
	if ctx.Done() == nil {
		t.Fatal("returned context should be cancellable")
	}
	cancel()
	if err := ctx.Err(); err == nil {
		t.Fatal("returned context should propagate parent cancellation")
	}
}

func TestFinishSuccess(t *testing.T) {
	out := runEmit(t, security.VerbRead, nil, nil)
	if success, _ := out["success"].(bool); !success {
		t.Errorf("success = false, want true (record=%v)", out)
	}
	if _, present := out["error"]; present {
		t.Errorf("error field must be absent on success (record=%v)", out)
	}
	if _, present := out["duration"]; !present {
		t.Errorf("duration should be recorded (record=%v)", out)
	}
}

func TestFinishError(t *testing.T) {
	out := runEmit(t, security.VerbRead, nil, errors.New("boom"))
	if success, _ := out["success"].(bool); success {
		t.Errorf("success = true, want false on error (record=%v)", out)
	}
	if got, _ := out["error"].(string); got != "boom" {
		t.Errorf("error = %q, want boom (record=%v)", got, out)
	}
}

func TestFinishDurationNonNegative(t *testing.T) {
	logger := NewLogger(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	ctx, finish := logger.Begin(context.Background(), "get_pod", security.VerbRead)
	finish(nil)
	d := recordOf(ctx).Duration
	if d < 0 {
		t.Errorf("duration = %v, want >= 0", d)
	}
}

func TestAttachPopulatesFields(t *testing.T) {
	logger := NewLogger(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	ctx, finish := logger.Begin(context.Background(), "get_secret", security.VerbRead)
	defer finish(nil)
	Attach(ctx, "Secret", "team-a", "db", true)
	rec := recordOf(ctx)
	if rec.Kind != "Secret" || rec.Namespace != "team-a" || rec.Name != "db" || !rec.DryRun {
		t.Errorf("Attach did not populate record: %+v", rec)
	}
}

func TestAttachOverwrites(t *testing.T) {
	logger := NewLogger(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	ctx, finish := logger.Begin(context.Background(), "get_pod", security.VerbRead)
	defer finish(nil)
	Attach(ctx, "Pod", "ns-1", "p-1", false)
	Attach(ctx, "Pod", "ns-2", "p-2", true)
	rec := recordOf(ctx)
	if rec.Namespace != "ns-2" || rec.Name != "p-2" || !rec.DryRun {
		t.Errorf("Attach should overwrite prior values: %+v", rec)
	}
}

func TestAttachNoOpWithoutActiveRecord(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Attach must be a no-op without an active record, panicked: %v", r)
		}
	}()
	Attach(context.Background(), "Pod", "default", "p", false)
}

func TestAttachArgsMerges(t *testing.T) {
	logger := NewLogger(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	ctx, finish := logger.Begin(context.Background(), "exec_command", security.VerbDebug)
	defer finish(nil)
	AttachArgs(ctx, map[string]any{"command": "ls -la", "container": "app"})
	AttachArgs(ctx, map[string]any{"timeout": "30s", "command": "id"})
	rec := recordOf(ctx)
	if rec.Args["command"] != "id" {
		t.Errorf("expected later AttachArgs to overwrite command, got %v", rec.Args["command"])
	}
	if rec.Args["container"] != "app" || rec.Args["timeout"] != "30s" {
		t.Errorf("expected keys from both calls merged, got %v", rec.Args)
	}
}

func TestAttachArgsAppearsInOutput(t *testing.T) {
	out := runEmit(t, security.VerbDebug,
		func(ctx context.Context) { AttachArgs(ctx, map[string]any{"command": "whoami"}) },
		nil)
	args, ok := out["args"].(map[string]any)
	if !ok {
		t.Fatalf("expected args object in output, got %v", out)
	}
	if args["command"] != "whoami" {
		t.Errorf("args.command = %v, want whoami", args["command"])
	}
}

func TestAttachArgsNoOpWithoutActiveRecord(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AttachArgs must be a no-op without an active record, panicked: %v", r)
		}
	}()
	AttachArgs(context.Background(), map[string]any{"x": "y"})
}

func TestEmitVerbLevelsAndMessages(t *testing.T) {
	for _, tc := range []struct {
		name      string
		verb      security.Verb
		wantLevel string
		wantMsg   string
	}{
		{"read", security.VerbRead, "INFO", "tool call"},
		{"write", security.VerbWrite, "INFO", "tool call (mutating)"},
		{"debug", security.VerbDebug, "INFO", "tool call (mutating)"},
		{"destructive", security.VerbDestructive, "WARN", "tool call (destructive)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := runEmit(t, tc.verb, nil, nil)
			if got, _ := out["level"].(string); got != tc.wantLevel {
				t.Errorf("level = %q, want %q", got, tc.wantLevel)
			}
			if got, _ := out["msg"].(string); got != tc.wantMsg {
				t.Errorf("msg = %q, want %q", got, tc.wantMsg)
			}
			if got, _ := out["verb"].(string); got != string(tc.verb) {
				t.Errorf("verb = %q, want %q", got, tc.verb)
			}
		})
	}
}

func TestEmitIncludesAllPopulatedFields(t *testing.T) {
	out := runEmit(t, security.VerbWrite,
		func(ctx context.Context) {
			Attach(ctx, "ConfigMap", "default", "cfg", true)
			AttachArgs(ctx, map[string]any{"patch_bytes": 12})
		},
		errors.New("conflict"))
	for _, key := range []string{"tool", "verb", "success", "duration", "kind", "namespace", "name", "dry_run", "error", "args"} {
		if _, present := out[key]; !present {
			t.Errorf("expected key %q in audit output (got %v)", key, out)
		}
	}
	if got, _ := out["kind"].(string); got != "ConfigMap" {
		t.Errorf("kind = %q, want ConfigMap", got)
	}
	if got, _ := out["dry_run"].(bool); !got {
		t.Errorf("dry_run should be true, got %v", out["dry_run"])
	}
}

func TestEmitOmitsEmptyOptionalFields(t *testing.T) {
	out := runEmit(t, security.VerbRead, nil, nil)
	for _, key := range []string{"kind", "namespace", "name", "dry_run", "error", "args"} {
		if _, present := out[key]; present {
			t.Errorf("optional field %q should be absent when unset (got %v)", key, out)
		}
	}
}

func TestEmitOutputIsSingleJSONLine(t *testing.T) {
	var buf bytes.Buffer
	logger := NewLogger(slog.New(slog.NewJSONHandler(&buf, nil)))
	ctx, finish := logger.Begin(context.Background(), "get_pod", security.VerbRead)
	Attach(ctx, "Pod", "default", "p", false)
	finish(nil)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Errorf("expected exactly one audit line, got %d: %q", len(lines), buf.String())
	}
}
