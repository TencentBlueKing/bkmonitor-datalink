// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

type secretFixture struct {
	secrets map[string]*corev1.Secret
	asked   []string
}

func (f *secretFixture) GetSecret(_ context.Context, namespace, name string) (*corev1.Secret, error) {
	f.asked = append(f.asked, namespace+"/"+name)
	if secret, found := f.secrets[namespace+"/"+name]; found {
		return secret.DeepCopy(), nil
	}
	return nil, apierrors.NewForbidden(schema.GroupResource{Resource: "secrets"}, name, nil)
}

func secretProvider(t *testing.T) (*Provider, *secretFixture) {
	t.Helper()
	client, _ := fixture()
	secrets := &secretFixture{secrets: map[string]*corev1.Secret{
		"own/app-redis": {ObjectMeta: metav1.ObjectMeta{Namespace: "own", Name: "app-redis", UID: "secret-uid"}, Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{"password": []byte("plain-value"), "url": []byte("redis://user:inline-password@192.0.2.10:6379"), "known": []byte("deployment-literal")}},
	}}
	return mustProvider(t, Options{Client: client, Secrets: secrets, DefaultNamespace: "own", LiteralSecrets: []string{"deployment-literal"}}), secrets
}

// A Secret read returns each key's size and SHA-256 and no value unless the
// request reveals it; the namespace is the request's, this process's own when
// it names none.
func TestASecretReadReturnsNoValueUnlessRevealed(t *testing.T) {
	provider, secrets := secretProvider(t)
	receipt, err := provider.ReadSecret(context.Background(), SecretRequest{Name: "app-redis"})
	if err != nil || receipt.Revealed || receipt.UID != "secret-uid" || len(receipt.Keys) != 3 || secrets.asked[0] != "own/app-redis" {
		t.Fatalf("read: %+v err=%v asked=%v", receipt, err, secrets.asked)
	}
	digest := sha256.Sum256([]byte("plain-value"))
	for _, key := range receipt.Keys {
		if key.Value != "" {
			t.Fatalf("key %s returned a value without reveal", key.Key)
		}
		if key.Key == "password" && (key.Bytes != len("plain-value") || key.SHA256 != hex.EncodeToString(digest[:])) {
			t.Fatalf("password key %+v", key)
		}
	}
	encoded, _ := json.Marshal(receipt)
	for _, value := range []string{"plain-value", "inline-password", "deployment-literal"} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("receipt without reveal carries %q: %s", value, encoded)
		}
	}
}

// Revealed, the values come back masked as a ConfigMap read masks them: a
// credential-named key whole, a credential embedded in a URL and one the
// deployment knows through the output redaction; a host shows, a masked
// credential keeps its size and digest for comparison, and the receipt
// records that the values were asked for.
func TestARevealedSecretGoesThroughTheRedaction(t *testing.T) {
	provider, _ := secretProvider(t)
	receipt, err := provider.ReadSecret(context.Background(), SecretRequest{Namespace: "own", Name: "app-redis", Reveal: true})
	if err != nil || !receipt.Revealed {
		t.Fatalf("reveal: %+v err=%v", receipt, err)
	}
	values := map[string]string{}
	for _, key := range receipt.Keys {
		values[key.Key] = key.Value
	}
	if values["password"] != "<redacted>" || strings.Contains(values["url"], "inline-password") || !strings.Contains(values["url"], "192.0.2.10:6379") || strings.Contains(values["known"], "deployment-literal") {
		t.Fatalf("revealed values %v, want the credential-named key masked whole, URL userinfo masked, the host kept", values)
	}
	digest := sha256.Sum256([]byte("plain-value"))
	for _, key := range receipt.Keys {
		if key.Key == "password" && (key.SHA256 != hex.EncodeToString(digest[:]) || key.Bytes != len("plain-value")) {
			t.Fatalf("a masked credential lost its size or digest: %+v", key)
		}
	}
}

// A Secret outside the whitelist is the apiserver's refusal, named as such;
// a name that is not a Secret name is refused before any read.
func TestASecretOutsideTheWhitelistIsForbidden(t *testing.T) {
	provider, secrets := secretProvider(t)
	_, err := provider.ReadSecret(context.Background(), SecretRequest{Namespace: "kube-system", Name: "other"})
	requireCode(t, err, CodeForbidden)
	_, err = provider.ReadSecret(context.Background(), SecretRequest{Name: "Not_A_Name"})
	requireCode(t, err, CodeInvalidRequest)
	if len(secrets.asked) != 1 {
		t.Fatalf("asked %v, want only the whitelisted-shape read", secrets.asked)
	}
}

type configMapFixture map[string]*corev1.ConfigMap

func (f configMapFixture) GetConfigMap(_ context.Context, namespace, name string) (*corev1.ConfigMap, error) {
	if configMap, found := f[namespace+"/"+name]; found {
		return configMap.DeepCopy(), nil
	}
	return nil, apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, name)
}

// A ConfigMap's values come back through the output redaction, always: the
// plain-text credentials modules render into their environment ConfigMaps -
// a password assignment, a connection string's userinfo - are masked, and
// everything else is kept.
func TestAConfigMapIsAlwaysRedacted(t *testing.T) {
	client, _ := fixture()
	configMaps := configMapFixture{"blueking/bk-monitor-unify-query-env": {ObjectMeta: metav1.ObjectMeta{Namespace: "blueking", Name: "bk-monitor-unify-query-env", UID: "cm-uid"},
		Data: map[string]string{"REDIS_PASSWORD": "plain-redis-credential", "MYSQL_URL": "mysql://root:mysql-credential@db:3306/x", "LOG_LEVEL": "info"}}}
	provider := mustProvider(t, Options{Client: client, ConfigMaps: configMaps, DefaultNamespace: "own"})
	receipt, err := provider.ReadConfigMap(context.Background(), ConfigMapRequest{Namespace: "blueking", Name: "bk-monitor-unify-query-env"})
	if err != nil || receipt.UID != "cm-uid" || len(receipt.Keys) != 3 {
		t.Fatalf("read: %+v err=%v", receipt, err)
	}
	encoded, _ := json.Marshal(receipt)
	for _, credential := range []string{"plain-redis-credential", "mysql-credential"} {
		if strings.Contains(string(encoded), credential) {
			t.Fatalf("ConfigMap credential returned: %s", encoded)
		}
	}
	if !strings.Contains(string(encoded), `"value":"info"`) {
		t.Fatalf("a plain value was changed: %s", encoded)
	}
	_, err = provider.ReadConfigMap(context.Background(), ConfigMapRequest{Name: "missing"})
	requireCode(t, err, CodeNotFound)
}
