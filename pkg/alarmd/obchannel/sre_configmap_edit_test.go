// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obchannel

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

const editedConfig = "server:\n  url: http://old.example.test/\n  password: Hidden9ValueForTest\n"

// editableConfigMap answers the read of one ConfigMap holding editedConfig.
type editableConfigMap struct{ ingressDenier }

func (editableConfigMap) GetResource(_ context.Context, _ schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name, "namespace": namespace, "uid": "uid-app", "resourceVersion": "7"},
		"data":     map[string]any{"config.yaml": editedConfig}}}, nil
}

// configmap.edit_text is a write offered with the others: counted, kept with
// the change receipts, and audited by key, lines and outcome, never a value.
func TestTheConfigMapEditIsAnAuditedWriteWithoutValues(t *testing.T) {
	client := &postObservationClient{target: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid"}}}
	writer := &patchWriter{}
	key := []byte("fixed-test-fingerprint-key")
	provider, err := pod.New(pod.Options{Client: client, DryRun: editableConfigMap{}, Writer: writer, DefaultNamespace: "fixture", FingerprintKey: key})
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(editedConfig))
	fingerprint := hex.EncodeToString(mac.Sum(nil))[:16]
	var actions []SREAction
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1",
		Operations: SREOperations(SREOptions{Pods: provider, Audit: func(action SREAction) { actions = append(actions, action) }})})
	if err != nil {
		t.Fatal(err)
	}
	status, response := call(t, channel, envelope(channel, "invoke", pod.OpEditConfigMapText, Params{"namespace": "ns1", "name": "app", "key": "config.yaml",
		"old_text": "http://old.example.test/", "new_text": "http://new.example.test/", "expected_fingerprint": fingerprint}))
	if status != 200 || response.Error != nil {
		t.Fatalf("edit: HTTP %d %+v", status, response)
	}
	if len(writer.sent) != 1 || !strings.Contains(writer.sent[0], `"path":"/metadata/resourceVersion","value":"7"`) {
		t.Fatalf("sent %v", writer.sent)
	}
	if len(actions) != 1 || actions[0].Op != pod.OpEditConfigMapText || actions[0].UID != "uid-app" ||
		actions[0].Detail != "key config.yaml; lines [2]; outcome updated" {
		t.Fatalf("audit %+v", actions)
	}
	line, _ := json.Marshal(actions[0])
	answer, _ := json.Marshal(response)
	if strings.Contains(string(answer), "Hidden9ValueForTest") || strings.Contains(string(line), "Hidden9ValueForTest") ||
		strings.Contains(string(line), "example.test") {
		t.Fatalf("a value came back: answer %s audit %s", answer, line)
	}
	found := false
	for _, counted := range SREActionOps {
		found = found || counted == pod.OpEditConfigMapText
	}
	if !found || sreEffect(pod.OpEditConfigMapText) != SREEffectChange {
		t.Fatal("configmap.edit_text is not a counted change")
	}
	// A dry run makes every check, writes nothing, and its receipt says so:
	// changes.list lists the edit and not the dry run.
	status, response = call(t, channel, envelope(channel, "invoke", pod.OpEditConfigMapText, Params{"namespace": "ns1", "name": "app", "key": "config.yaml",
		"old_text": "http://old.example.test/", "new_text": "http://new.example.test/", "expected_fingerprint": fingerprint, "dry_run": true}))
	if status != 200 || response.Error != nil || len(writer.sent) != 1 || len(actions) != 2 || actions[0].DryRun || !actions[1].DryRun {
		t.Fatalf("dry run: HTTP %d %+v sent %d audit %+v", status, response.Error, len(writer.sent), actions)
	}
	receipts := NewSREReceipts(redistest.Start(t).Client(), "test.sre:{env}:receipts", nil, nil)
	for _, action := range actions {
		receipts.Record(action)
	}
	changes, _ := runChanges(t, ChangesOptions{Receipts: receipts, Now: func() time.Time { return time.Now().Add(time.Minute) }}, Params{})
	if len(changes.Rows) != 1 || changes.Rows[0].Op != pod.OpEditConfigMapText {
		t.Fatalf("change rows %+v, want the edit only", changes.Rows)
	}
}

// The description a caller reads names the codes a refusal comes back with:
// every word the provider refuses with, under the pod_ prefix the channel
// gives every Pod provider failure, and a refusal comes back under it.
func TestTheConfigMapEditNamesTheCodesItRefusesWith(t *testing.T) {
	client := &postObservationClient{target: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid"}}}
	provider, err := pod.New(pod.Options{Client: client, DryRun: editableConfigMap{}, Writer: &patchWriter{}, DefaultNamespace: "fixture",
		FingerprintKey: []byte("fixed-test-fingerprint-key")})
	if err != nil {
		t.Fatal(err)
	}
	operations := SREOperations(SREOptions{Pods: provider})
	summary := ""
	for _, operation := range operations {
		if operation.ID == pod.OpEditConfigMapText {
			summary = operation.Summary
		}
	}
	for _, word := range []string{pod.EditKeyNotFound, pod.EditFingerprintChanged, pod.EditNewTextNotPlain, pod.EditOldTextNotShown, pod.EditNotShownExactly} {
		if !strings.Contains(summary, "pod_"+word) {
			t.Errorf("the description does not name pod_%s: %s", word, summary)
		}
	}
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1", Operations: operations})
	if err != nil {
		t.Fatal(err)
	}
	_, response := call(t, channel, envelope(channel, "invoke", pod.OpEditConfigMapText, Params{"namespace": "ns1", "name": "app", "key": "config.yaml",
		"old_text": "http://old.example.test/", "new_text": "http://new.example.test/", "expected_fingerprint": "0000000000000000"}))
	if response.Error == nil || response.Error.Code != "pod_"+pod.EditFingerprintChanged {
		t.Fatalf("stale fingerprint answered %+v", response.Error)
	}
}
