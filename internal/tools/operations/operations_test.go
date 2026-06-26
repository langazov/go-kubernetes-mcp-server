package operations

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/testutil"
)

func withWrites(c *config.Config) { c.AllowWrites = true }

// ----- apply_manifest -----

func TestApplyManifestDryRun(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withWrites))
	testutil.RegisterApplyReactor(testutil.ClientsFor(tk).Dynamic, "configmaps")
	yes := true
	res, err := applyManifest(tk)(context.Background(), applyArgs{
		Manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: m\n  namespace: default\ndata:\n  k: v\n",
		DryRun:   &yes,
	})
	if err != nil {
		t.Fatalf("applyManifest: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "dry run") {
		t.Errorf("expected dry-run note:\n%s", out)
	}
}

func TestApplyManifestForReal(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withWrites))
	testutil.RegisterApplyReactor(testutil.ClientsFor(tk).Dynamic, "configmaps")
	no := false
	res, err := applyManifest(tk)(context.Background(), applyArgs{
		Manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: real\n  namespace: default\ndata:\n  k: v\n",
		DryRun:   &no,
	})
	if err != nil {
		t.Fatalf("applyManifest: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("apply failed: %s", testutil.TextOf(res))
	}
}

func TestApplyManifestBlockedWithoutWriteFlag(t *testing.T) {
	tk := testutil.NewToolkit(t) // read-only
	no := false
	res, _ := applyManifest(tk)(context.Background(), applyArgs{Manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n", DryRun: &no})
	if !testutil.IsError(res) {
		t.Fatal("apply must be blocked without --allow-writes")
	}
}

func TestApplyManifestPrivilegedTargetBlocked(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withWrites))
	no := false
	res, err := applyManifest(tk)(context.Background(), applyArgs{
		Manifest: "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: x\n  namespace: kube-system\n",
		DryRun:   &no,
	})
	if err != nil {
		t.Fatalf("unexpected go error: %v", err)
	}
	if !testutil.IsError(res) || !strings.Contains(testutil.TextOf(res), "control-plane") {
		t.Errorf("expected privileged-target block:\n%s", testutil.TextOf(res))
	}
}

// ----- scale -----

func TestScaleDeployment(t *testing.T) {
	one := int32(1)
	tk := testutil.NewToolkit(t, testutil.WithConfig(withWrites),
		testutil.WithObjs(&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
			Spec:       appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}}},
		}),
	)
	testutil.RegisterScaleReactors(testutil.ClientsFor(tk).Typed)
	res, err := scale(tk)(context.Background(), scaleArgs{Kind: "Deployment", Name: "api", Replicas: 5})
	if err != nil {
		t.Fatalf("scale: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("scale failed: %s", testutil.TextOf(res))
	}
	got, err := tk.Clients.Core.AppsV1().Deployments("default").GetScale(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get scale: %v", err)
	}
	if got.Spec.Replicas != 5 {
		t.Errorf("replicas = %d, want 5", got.Spec.Replicas)
	}
}

func TestScaleUnsupportedKind(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withWrites))
	res, _ := scale(tk)(context.Background(), scaleArgs{Kind: "Pod", Name: "x", Replicas: 3})
	if !testutil.IsError(res) {
		t.Error("expected error for unsupported kind")
	}
}

// ----- rollout restart -----

func TestRolloutRestart(t *testing.T) {
	one := int32(1)
	tk := testutil.NewToolkit(t, testutil.WithConfig(withWrites),
		testutil.WithObjs(&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "default"},
			Spec:       appsv1.DeploymentSpec{Replicas: &one, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"a": "b"}}},
		}),
	)
	res, err := rolloutRestart(tk)(context.Background(), rolloutMutArgs{Kind: "Deployment", Name: "api"})
	if err != nil {
		t.Fatalf("rolloutRestart: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("restart failed: %s", testutil.TextOf(res))
	}
	d, err := tk.Clients.Core.AppsV1().Deployments("default").Get(context.Background(), "api", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] == "" {
		t.Error("expected restartedAt annotation after restart")
	}
}

// ----- create_namespace / create_configmap / create_secret -----

func TestCreateNamespace(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withWrites))
	res, err := createNamespace(tk)(context.Background(), createNamespaceArgs{Name: "team-x"})
	if err != nil {
		t.Fatalf("createNamespace: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("create failed: %s", testutil.TextOf(res))
	}
	if _, err := tk.Clients.Core.CoreV1().Namespaces().Get(context.Background(), "team-x", metav1.GetOptions{}); err != nil {
		t.Errorf("namespace not created: %v", err)
	}
}

func TestCreateConfigMap(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withWrites))
	res, err := createConfigMap(tk)(context.Background(), configMapArgs{Name: "cfg", Data: map[string]string{"k": "v"}})
	if err != nil {
		t.Fatalf("createConfigMap: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("create failed: %s", testutil.TextOf(res))
	}
}

func TestCreateSecretDoesNotEchoValues(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withWrites))
	res, err := createSecret(tk)(context.Background(), secretArgs{Name: "s", StringData: map[string]string{"password": "hunter2"}})
	if err != nil {
		t.Fatalf("createSecret: %v", err)
	}
	if strings.Contains(testutil.TextOf(res), "hunter2") {
		t.Errorf("createSecret must not echo values:\n%s", testutil.TextOf(res))
	}
}

// ----- label / patch -----

func TestLabel(t *testing.T) {
	tk := testutil.NewToolkit(t, testutil.WithConfig(withWrites),
		testutil.WithObjs(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default"}, Data: map[string]string{}}),
		testutil.WithDynamicObjs(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default"}}),
	)
	res, err := label(tk)(context.Background(), metaPatchArgs{Kind: "ConfigMap", APIVersion: "v1", Name: "cfg", Items: map[string]string{"team": "ops"}})
	if err != nil {
		t.Fatalf("label: %v", err)
	}
	if testutil.IsError(res) {
		t.Fatalf("label failed: %s", testutil.TextOf(res))
	}
}
