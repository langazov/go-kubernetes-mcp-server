package workloads

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/testutil"
)

func TestListPods(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}},
			&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "app"}},
		),
	)
	res, err := listPods(tk)(context.Background(), tools.ListArgs{AllNamespaces: true})
	if err != nil {
		t.Fatalf("listPods: %v", err)
	}
	if res.IsError {
		t.Fatalf("unexpected error: %s", testutil.TextOf(res))
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "web") || !strings.Contains(out, "db") {
		t.Errorf("missing pods:\n%s", out)
	}
	if !strings.Contains(out, "Running") && !strings.Contains(out, "Pending") {
		t.Errorf("missing status column:\n%s", out)
	}
}

func TestGetPodNotFound(t *testing.T) {
	tk := testutil.NewToolkit(t)
	res, err := getPod(tk)(context.Background(), tools.NamespaceNameArgs{Name: "nope"})
	// A NotFound surfaces as a Go error (handled by Wrap as a tool error).
	if err == nil && !res.IsError {
		t.Fatal("expected an error for missing pod")
	}
}

func TestListDeployments(t *testing.T) {
	one := int32(1)
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
				Spec:       appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}}},
				Status:     appsv1.DeploymentStatus{ReadyReplicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1, Replicas: 1},
			},
		),
	)
	res, err := listDeployments(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listDeployments: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "api") || !strings.Contains(out, "1/1") {
		t.Errorf("unexpected deployment table:\n%s", out)
	}
}

func TestGetDeployment(t *testing.T) {
	one := int32(3)
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(
			&appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
				Spec: appsv1.DeploymentSpec{
					Replicas: &one,
					Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}},
					Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType},
					Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "api", Image: "nginx:1.2"}}}},
				},
			},
		),
	)
	res, err := getDeployment(tk)(context.Background(), tools.NamespaceNameArgs{Name: "api"})
	if err != nil {
		t.Fatalf("getDeployment: %v", err)
	}
	out := testutil.TextOf(res)
	for _, want := range []string{"api", "nginx:1.2", "RollingUpdate"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in describe:\n%s", want, out)
		}
	}
}

func TestListStatefulSets(t *testing.T) {
	one := int32(1)
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(
			&appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: "cache", Namespace: "default"},
				Spec:       appsv1.StatefulSetSpec{Replicas: &one, ServiceName: "cache"},
				Status:     appsv1.StatefulSetStatus{ReadyReplicas: 1},
			},
		),
	)
	res, err := listStatefulSets(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listStatefulSets: %v", err)
	}
	if !strings.Contains(testutil.TextOf(res), "1/1") {
		t.Errorf("expected ready 1/1:\n%s", testutil.TextOf(res))
	}
}

func TestListDaemonSets(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(
			&appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "default"},
				Status:     appsv1.DaemonSetStatus{DesiredNumberScheduled: 3, CurrentNumberScheduled: 3, NumberReady: 3},
			},
		),
	)
	res, err := listDaemonSets(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listDaemonSets: %v", err)
	}
	out := testutil.TextOf(res)
	for _, want := range []string{"agent", "3"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestListJobs(t *testing.T) {
	one := int32(1)
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(
			batchJob("backfill", "default", &one, 1, 0),
		),
	)
	res, err := listJobs(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listJobs: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "Complete") && !strings.Contains(out, "1") {
		t.Errorf("unexpected job row:\n%s", out)
	}
}

func TestListCronJobs(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(
			cronJob("nightly", "default", "0 * * * *"),
		),
	)
	res, err := listCronJobs(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listCronJobs: %v", err)
	}
	if !strings.Contains(testutil.TextOf(res), "0 * * * *") {
		t.Errorf("expected schedule in output:\n%s", testutil.TextOf(res))
	}
}

// --- helpers ---

func batchJob(name, ns string, completions *int32, succeeded, failed int32) *batchv1.Job {
	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       batchv1.JobSpec{Completions: completions, Parallelism: ptrInt32(1)},
		Status:     batchv1.JobStatus{Succeeded: succeeded, Failed: failed},
	}
}

func cronJob(name, ns, schedule string) *batchv1.CronJob {
	return &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       batchv1.CronJobSpec{Schedule: schedule},
	}
}

func ptrInt32(i int32) *int32 { return &i }
