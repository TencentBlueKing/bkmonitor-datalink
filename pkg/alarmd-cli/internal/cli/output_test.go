// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

// A reference to a Secret names where a credential is kept, not the
// credential: the volume source, a projected source, a key reference, an
// envFrom reference, an image pull Secret, an Ingress's TLS Secret and a
// ServiceMonitor's credential references all come through as they are, while
// a credential held in place under a credential-named key is still left out,
// and so is a structure that is a credential by what it is.
func TestASecretReferenceIsKeptAndACredentialValueIsNot(t *testing.T) {
	answer := map[string]any{"items": []any{
		map[string]any{"kind": "Deployment", "spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"volumes": []any{
				map[string]any{"name": "trace-runtime", "secret": map[string]any{"secretName": "ref-volume", "defaultMode": 420.0,
					"items": []any{map[string]any{"key": "ref-volume-key", "path": "runtime.json"}}}},
				map[string]any{"name": "bundle", "projected": map[string]any{"sources": []any{
					map[string]any{"secret": map[string]any{"name": "ref-projected"}}}}},
			},
			"imagePullSecrets": []any{map[string]any{"name": "ref-pull"}},
			"containers": []any{map[string]any{"name": "app",
				"envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "ref-env-from"}}},
				"env": []any{map[string]any{"name": "DB_PASSWORD",
					"valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "ref-key", "key": "ref-key-field"}}}}}},
		}}}},
		map[string]any{"kind": "Ingress", "spec": map[string]any{"tls": []any{map[string]any{"secretName": "ref-tls"}}}},
		map[string]any{"kind": "ServiceMonitor", "spec": map[string]any{"endpoints": []any{map[string]any{
			"basicAuth":         map[string]any{"password": map[string]any{"name": "ref-basic", "key": "ref-basic-key"}},
			"bearerTokenSecret": map[string]any{"name": "ref-bearer", "key": "token"}}}}},
		map[string]any{"password": "inline-password", "secret": "inline-secret", "token": "inline-token",
			"headers": map[string]any{"X-Custom-Auth": "inline-header"}, "grant": map[string]any{"code": "inline-grant"},
			"credentials": map[string]any{"user": "inline-credential"}},
	}}
	encoded, _ := json.Marshal(redact(answer, nil))
	text := string(encoded)
	for _, kept := range []string{"ref-volume", "ref-volume-key", `"defaultMode":420`, "ref-projected", "ref-pull", "ref-env-from",
		"ref-key", "ref-key-field", "ref-tls", "ref-basic", "ref-basic-key", "ref-bearer"} {
		if !strings.Contains(text, kept) {
			t.Fatalf("lost the reference %q: %s", kept, text)
		}
	}
	for _, gone := range []string{"inline-password", "inline-secret", "inline-token", "inline-header", "inline-grant", "inline-credential"} {
		if strings.Contains(text, gone) {
			t.Fatalf("kept the credential %q: %s", gone, text)
		}
	}
}

// Under a credential-named key a structure is read as a reference: only the
// fields a reference to a Secret has come through, and every other value -
// a list element, a field with an ordinary name - is left out, at any depth.
func TestUnderACredentialNameOnlyAReferenceComesThrough(t *testing.T) {
	for _, value := range []map[string]any{
		{"token": []any{"sentinel-list"}},
		{"apiKey": map[string]any{"value": "sentinel-value"}},
		{"password": map[string]any{"data": "sentinel-data"}},
		{"secret": map[string]any{"secretName": "ref-kept", "items": []any{map[string]any{"key": "ref-item", "path": "p", "note": "sentinel-nested"}, "sentinel-element"}}},
		{"password": map[string]any{"data": map[string]any{"inner": "sentinel-deep"}}},
	} {
		encoded, _ := json.Marshal(redact(value, nil))
		if strings.Contains(string(encoded), "sentinel") {
			t.Errorf("%v leaked: %s", value, encoded)
		}
		if _, isRef := value["secret"]; isRef && (!strings.Contains(string(encoded), "ref-kept") || !strings.Contains(string(encoded), "ref-item")) {
			t.Errorf("%v lost its reference: %s", value, encoded)
		}
	}
}

// The reference fields are one table, the same one the server holds.
func TestTheReferenceFieldsAreTheSharedTable(t *testing.T) {
	want := "defaultMode,items,key,mode,name,namespace,optional,path,secretName"
	if got := strings.Join(sortedReferenceFields(), ","); got != want {
		t.Fatalf("reference fields %s, want %s", got, want)
	}
}
