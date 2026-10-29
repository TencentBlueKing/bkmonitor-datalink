// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

// liveConfigMap answers the read of one ConfigMap with a uid; the writer
// below applies a patch by dropping its data.
type liveConfigMap struct{ ingressDenier }

func (liveConfigMap) GetResource(_ context.Context, _ schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name, "namespace": namespace, "uid": "uid-env"}, "data": map[string]any{"mode": "content-not-for-the-audit"}}}, nil
}

type patchWriter struct {
	configMapWriter
	sent []string
}

func (w *patchWriter) JSONPatch(_ context.Context, _ schema.GroupVersionResource, namespace, name string, patch []byte) (*unstructured.Unstructured, error) {
	w.sent = append(w.sent, string(patch))
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name, "namespace": namespace, "uid": "uid-env"}}}, nil
}

// k8s.patch is an exec operation offered with the rest of the writes; its
// audit record names the object, the outcome and every op with its path,
// never a value.
func TestThePatchIsAnAuditedWriteWithoutValues(t *testing.T) {
	client := &postObservationClient{target: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid"}}}
	writer := &patchWriter{}
	provider, err := pod.New(pod.Options{Client: client, DryRun: liveConfigMap{}, Writer: writer, DefaultNamespace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var actions []SREAction
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1",
		Operations: SREOperations(SREOptions{Pods: provider, Audit: func(action SREAction) { actions = append(actions, action) }})})
	if err != nil {
		t.Fatal(err)
	}
	status, response := call(t, channel, envelope(channel, "invoke", pod.OpPatch, Params{"api_version": "v1", "kind": "ConfigMap", "namespace": "ns1", "name": "env", "uid": "uid-env",
		"patch": []any{map[string]any{"op": "test", "path": "/data/mode", "value": "content-not-for-the-audit"}, map[string]any{"op": "remove", "path": "/data"}}}))
	if status != 200 || response.Error != nil {
		t.Fatalf("patch: HTTP %d %+v", status, response)
	}
	if len(writer.sent) != 1 || !strings.HasPrefix(writer.sent[0], `[{"op":"test","path":"/metadata/uid","value":"uid-env"}`) {
		t.Fatalf("sent %v", writer.sent)
	}
	if len(actions) != 1 || actions[0].Op != pod.OpPatch || actions[0].UID != "uid-env" ||
		actions[0].Detail != "updated 1; 2 ops: test /data/mode, remove /data" {
		t.Fatalf("audit %+v", actions)
	}
	if line, _ := json.Marshal(actions[0]); strings.Contains(string(line), "content-not-for-the-audit") {
		t.Fatalf("the audit carries a value: %s", line)
	}
	found := false
	for _, counted := range SREActionOps {
		found = found || counted == pod.OpPatch
	}
	if !found {
		t.Fatal("k8s.patch is not among the counted SRE actions")
	}
}
