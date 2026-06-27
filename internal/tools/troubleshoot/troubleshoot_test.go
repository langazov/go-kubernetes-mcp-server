package troubleshoot

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8stesting "k8s.io/client-go/testing"

	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/testutil"
)

func TestGetLogs(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(podWithLogs("app", "default")),
	)
	// The fake clientset's GetLogs returns an empty stream by default, so the
	// handler produces "(no logs found)".
	cs := testutil.ClientsFor(tk).Typed
	cs.PrependReactor("get", "pods/log", func(_ k8stesting.Action) (bool, runtime.Object, error) {
		return true, &runtime.Unknown{Raw: []byte("line1\nline2\n")}, nil
	})
	res, err := getLogs(tk)(context.Background(), logArgs{Pod: "app", Namespace: "default"})
	if err != nil {
		t.Fatalf("getLogs: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "line1") {
		t.Errorf("expected log content, got:\n%s", out)
	}
}

func TestGetLogsInvalidName(t *testing.T) {
	tk := testutil.NewToolkit(t)
	res, _ := getLogs(tk)(context.Background(), logArgs{Pod: ""})
	if !testutil.IsError(res) {
		t.Error("expected error for empty pod name")
	}
}

func TestDescribeAnyKind(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default"}, Data: map[string]string{"k": "v"}}),
		testutil.WithDynamicObjs(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default"}, Data: map[string]string{"k": "v"}}),
	)
	res, err := describe(tk)(context.Background(), describeArgs{Kind: "ConfigMap", APIVersion: "v1", Namespace: "default", Name: "cfg"})
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "cfg") {
		t.Errorf("expected cfg in describe output:\n%s", out)
	}
}

func TestDescribeUnknownKind(t *testing.T) {
	tk := testutil.NewToolkit(t)
	res, err := describe(tk)(context.Background(), describeArgs{Kind: "Frobnicator", Name: "x"})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !testutil.IsError(res) {
		t.Error("expected tool error for unknown kind")
	}
}

func TestDiagnosePodCrashLoop(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "app:v1"}}},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "c", Ready: false, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff", Message: "back-off"}}},
				},
			},
		}),
	)
	res, err := diagnosePod(tk)(context.Background(), diagnosePodArgs{Name: "p", Namespace: "default"})
	if err != nil {
		t.Fatalf("diagnosePod: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "CrashLoopBackOff") {
		t.Errorf("expected CrashLoopBackOff finding:\n%s", out)
	}
}

func TestDiagnosePodOOMKilled(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "app:v1"}}},
			Status: corev1.PodStatus{
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "c", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137}}},
				},
			},
		}),
	)
	res, err := diagnosePod(tk)(context.Background(), diagnosePodArgs{Name: "p", Namespace: "default"})
	if err != nil {
		t.Fatalf("diagnosePod: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "OOMKilled") {
		t.Errorf("expected OOMKilled finding:\n%s", out)
	}
}

func TestDiagnosePodHealthy(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "default"},
			Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "app:v1"}}},
			Status: corev1.PodStatus{
				Phase:      corev1.PodRunning,
				Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
				ContainerStatuses: []corev1.ContainerStatus{
					{Name: "c", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
				},
			},
		}),
	)
	res, err := diagnosePod(tk)(context.Background(), diagnosePodArgs{Name: "p", Namespace: "default"})
	if err != nil {
		t.Fatalf("diagnosePod: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "No issues detected") {
		t.Errorf("expected healthy finding:\n%s", out)
	}
}

func TestDiagnoseNodeNotReady(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "n1"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse, Reason: "KubeletNotResponding", Message: "kubelet down"},
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue, Reason: "LowMem"},
			}},
		}),
	)
	res, err := diagnoseNode(tk)(context.Background(), tools.NamespaceNameArgs{Name: "n1"})
	if err != nil {
		t.Fatalf("diagnoseNode: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "NotReady") || !strings.Contains(out, "MemoryPressure") {
		t.Errorf("expected NotReady + MemoryPressure:\n%s", out)
	}
}

// podWithLogs returns a pod; the fake CoreV1 GetLogs returns whatever the fake
// is configured with. The fake clientset doesn't stream real logs, so we rely on
// the request returning empty (the handler prints "(no logs found)") — but that
// still proves the wiring works. For a content check we use a non-empty check on
// the result being non-error.
func podWithLogs(name, ns string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: "app:v1"}}},
	}
}

func TestDescribeSecretMasksValues(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "admin-creds", Namespace: "default"},
		Type:       corev1.SecretTypeOpaque,
		Data:       map[string][]byte{"token": []byte("SUPERSECRETTOKEN")},
	}
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(secret),
		testutil.WithDynamicObjs(secret),
	)
	res, err := describe(tk)(context.Background(), describeArgs{Kind: "Secret", APIVersion: "v1", Namespace: "default", Name: "admin-creds"})
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("describe secret failed: %s", testutil.TextOf(res))
	}
	out := testutil.TextOf(res)
	if strings.Contains(out, "SUPERSECRETTOKEN") {
		t.Errorf("describe must not leak plaintext secret values:\n%s", out)
	}
	if strings.Contains(out, "U1VQRVJTRUNSRVRUT0tFTg==") {
		t.Errorf("describe must not leak raw base64 secret values:\n%s", out)
	}
	if !strings.Contains(out, "••••") {
		t.Errorf("expected masked secret data:\n%s", out)
	}
}

func TestDescribeSecretKubeSystemBlocked(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "admin", Namespace: "kube-system"},
		Data:       map[string][]byte{"token": []byte("x")},
	}
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(secret),
		testutil.WithDynamicObjs(secret),
	)
	res, err := describe(tk)(context.Background(), describeArgs{Kind: "Secret", APIVersion: "v1", Namespace: "kube-system", Name: "admin"})
	if err != nil {
		t.Fatalf("describe: %v", err)
	}
	if !testutil.IsError(res) || !strings.Contains(testutil.TextOf(res), "privileged") {
		t.Fatalf("describing a kube-system resource must require --allow-privileged-targets:\n%s", testutil.TextOf(res))
	}
}
