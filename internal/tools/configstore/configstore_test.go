package configstore

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/langazov/go-kubernetes-mcp-server/internal/config"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools"
	"github.com/langazov/go-kubernetes-mcp-server/internal/tools/testutil"
)

func TestListConfigMaps(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "app-config", Namespace: "default"}, Data: map[string]string{"a": "1", "b": "2"}}),
	)
	res, err := listConfigMaps(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listConfigMaps: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "app-config") || !strings.Contains(out, "2") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestGetConfigMap(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "default"}, Data: map[string]string{"env": "prod"}}),
	)
	res, err := getConfigMap(tk)(context.Background(), tools.NamespaceNameArgs{Name: "cfg"})
	if err != nil {
		t.Fatalf("getConfigMap: %v", err)
	}
	if !strings.Contains(testutil.TextOf(res), "env: prod") {
		t.Errorf("expected data:\n%s", testutil.TextOf(res))
	}
}

func TestListSecretsHidesValues(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{"password": []byte("supersecret")},
		}),
	)
	res, err := listSecrets(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listSecrets: %v", err)
	}
	out := testutil.TextOf(res)
	if strings.Contains(out, "supersecret") {
		t.Errorf("list must never show secret values:\n%s", out)
	}
	if !strings.Contains(out, "db") {
		t.Errorf("missing secret name:\n%s", out)
	}
}

func TestGetSecretMaskedByDefault(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
			Type:       corev1.SecretTypeOpaque,
			Data:       map[string][]byte{"password": []byte("supersecret")},
		}),
	)
	res, err := getSecret(tk)(context.Background(), secretArgs{Name: "db"})
	if err != nil {
		t.Fatalf("getSecret: %v", err)
	}
	out := testutil.TextOf(res)
	if strings.Contains(out, "supersecret") {
		t.Errorf("secret must be masked by default:\n%s", out)
	}
	if !strings.Contains(out, "••••") {
		t.Errorf("expected masked marker:\n%s", out)
	}
	if !strings.Contains(out, "sha256:") {
		t.Errorf("expected change-detection hash:\n%s", out)
	}
}

func TestGetSecretRevealBlockedWithoutFlag(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
			Data:       map[string][]byte{"password": []byte("supersecret")},
		}),
	)
	res, _ := getSecret(tk)(context.Background(), secretArgs{Name: "db", Reveal: true})
	if !testutil.IsError(res) {
		t.Errorf("reveal must be blocked without --reveal-secrets:\n%s", testutil.TextOf(res))
	}
}

func TestGetSecretRevealWithFlag(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithConfig(func(c *config.Config) { c.RevealSecrets = true }),
		testutil.WithObjs(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "default"},
			Data:       map[string][]byte{"password": []byte("supersecret")},
		}),
	)
	res, err := getSecret(tk)(context.Background(), secretArgs{Name: "db", Reveal: true})
	if err != nil {
		t.Fatalf("getSecret: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "supersecret") {
		t.Errorf("reveal=true with flag should show plaintext:\n%s", out)
	}
}

func TestListPVCs(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: "data", Namespace: "default"},
			Spec:       corev1.PersistentVolumeClaimSpec{StorageClassName: strPtr("standard")},
			Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
		}),
	)
	res, err := listPVCs(tk)(context.Background(), tools.ListArgs{})
	if err != nil {
		t.Fatalf("listPVCs: %v", err)
	}
	out := testutil.TextOf(res)
	if !strings.Contains(out, "data") || !strings.Contains(out, "Bound") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func strPtr(s string) *string { return &s }

func TestListSecretsAllNamespacesBlockedByAllowlist(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithConfig(func(c *config.Config) { c.Namespaces = []string{"team-a"} }),
		testutil.WithObjs(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "team-b"},
			Data:       map[string][]byte{"password": []byte("x")},
		}),
	)
	res, err := listSecrets(tk)(context.Background(), tools.ListArgs{AllNamespaces: true})
	if err != nil {
		t.Fatalf("listSecrets: %v", err)
	}
	if !testutil.IsError(res) || !strings.Contains(testutil.TextOf(res), "allowlist") {
		t.Fatalf("all_namespaces must be blocked when an allowlist is configured:\n%s", testutil.TextOf(res))
	}
}

func TestListSecretsBlockedOutsideAllowlist(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithConfig(func(c *config.Config) { c.Namespaces = []string{"team-a"} }),
		testutil.WithObjs(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "team-b"},
			Data:       map[string][]byte{"password": []byte("x")},
		}),
	)
	res, err := listSecrets(tk)(context.Background(), tools.ListArgs{Namespace: "team-b"})
	if err != nil {
		t.Fatalf("listSecrets: %v", err)
	}
	if !testutil.IsError(res) {
		t.Fatalf("listing a namespace outside the allowlist must be blocked:\n%s", testutil.TextOf(res))
	}
}

func TestGetSecretKubeSystemBlocked(t *testing.T) {
	tk := testutil.NewToolkit(t,
		testutil.WithObjs(&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "admin", Namespace: "kube-system"},
			Data:       map[string][]byte{"token": []byte("x")},
		}),
	)
	res, err := getSecret(tk)(context.Background(), secretArgs{Name: "admin", Namespace: "kube-system"})
	if err != nil {
		t.Fatalf("getSecret: %v", err)
	}
	if !testutil.IsError(res) || !strings.Contains(testutil.TextOf(res), "privileged") {
		t.Fatalf("kube-system secret access must require --allow-privileged-targets:\n%s", testutil.TextOf(res))
	}
}
