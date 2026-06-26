package tools

import (
	"errors"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// RPCStatusError maps a Kubernetes API error into an actionable, agent-friendly
// Go error. It is returned (not a CallToolResult) so the Wrap helper surfaces it
// as a tool error. Callers should pass an action describing what was attempted,
// e.g. "get pod foo".
func RPCStatusError(err error, action string) error {
	if err == nil {
		return nil
	}
	var se *apierrors.StatusError
	if errors.As(err, &se) {
		switch se.Status().Reason {
		case "NotFound":
			return fmt.Errorf("%s: resource not found (%s). Call a list tool to see what exists", action, se.Status().Message)
		case "Forbidden":
			return fmt.Errorf("%s: forbidden (403) — the server's identity lacks RBAC permission, or the target is outside policy. Detail: %s", action, se.Status().Message)
		case "Unauthorized":
			return fmt.Errorf("%s: unauthorized (401) — credentials are invalid or expired", action)
		case "Conflict":
			return fmt.Errorf("%s: conflict (409) — %s", action, se.Status().Message)
		case "Timeout", "DeadlineExceed":
			return fmt.Errorf("%s: timed out; increase --default-timeout or retry", action)
		case "TooManyRequests":
			return fmt.Errorf("%s: rate limited (429) by the API server; raise --qps/--burst or retry", action)
		case "Invalid":
			return fmt.Errorf("%s: invalid request — %s", action, se.Status().Message)
		}
		return fmt.Errorf("%s: %s", action, se.Status().Message)
	}
	return fmt.Errorf("%s: %w", action, err)
}
