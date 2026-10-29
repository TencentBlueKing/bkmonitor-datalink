// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

// configMapWriter writes every ConfigMap it is given and deletes by uid.
type configMapWriter struct{ deleted []string }

func (*configMapWriter) Apply(_ context.Context, _ schema.GroupVersionResource, _, name string, data []byte, _ bool) (*unstructured.Unstructured, error) {
	object := &unstructured.Unstructured{}
	if err := object.UnmarshalJSON(data); err != nil {
		return nil, err
	}
	object.SetUID(types.UID("uid-" + name))
	return object, nil
}

func (w *configMapWriter) JSONPatch(_ context.Context, _ schema.GroupVersionResource, _, _ string, _ []byte) (*unstructured.Unstructured, error) {
	return nil, errors.New("not served")
}

func (w *configMapWriter) Delete(_ context.Context, _ schema.GroupVersionResource, namespace, name string, uid types.UID, _ string) error {
	w.deleted = append(w.deleted, namespace+"/"+name+" "+string(uid))
	return nil
}

// The writes are exec operations, audited with the objects they touched and
// how each came out, never their content; a Secret apply's answer carries
// fingerprints, not values.
func TestTheChannelWritesAndAuditsTheObjects(t *testing.T) {
	client := &postObservationClient{target: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid"}}}
	writer := &configMapWriter{}
	provider, err := pod.New(pod.Options{Client: client, DryRun: ingressDenier{}, Writer: writer, DefaultNamespace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var actions []SREAction
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1",
		Operations: SREOperations(SREOptions{Pods: provider, Audit: func(action SREAction) { actions = append(actions, action) }})})
	if err != nil {
		t.Fatal(err)
	}
	status, response := call(t, channel, envelope(channel, "discover", "", Params{}))
	encoded, _ := json.Marshal(response.Result)
	if status != 200 || !strings.Contains(string(encoded), `"operation":"k8s.apply"`) || !strings.Contains(string(encoded), `"operation":"k8s.delete"`) {
		t.Fatalf("discover: %s", encoded)
	}
	for _, row := range response.Result.(map[string]any)["operations"].([]any) {
		op := row.(map[string]any)
		if (op["operation"] == pod.OpApply || op["operation"] == pod.OpDelete) && op["effect"] != "exec" {
			t.Fatalf("%v is not an exec operation: %v", op["operation"], op)
		}
	}
	manifests := "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: env}\ndata: {mode: content-not-for-the-audit}\n---\n" +
		"apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRoleBinding\nmetadata: {name: grab}\n"
	status, response = call(t, channel, envelope(channel, "invoke", pod.OpApply, Params{"manifests": manifests, "namespace": "ns1", "force": true}))
	if status != 200 || response.Error != nil {
		t.Fatalf("apply: HTTP %d %+v", status, response)
	}
	status, response = call(t, channel, envelope(channel, "invoke", pod.OpDelete, Params{"api_version": "v1", "kind": "ConfigMap", "namespace": "ns1", "name": "env", "uid": "uid-env"}))
	if status != 200 || response.Error != nil {
		t.Fatalf("delete: HTTP %d %+v", status, response)
	}
	if len(actions) != 2 {
		t.Fatalf("audit %+v", actions)
	}
	apply, deletion := actions[0], actions[1]
	if apply.Op != pod.OpApply || apply.Name != "ConfigMap/ns1/env,ClusterRoleBinding//grab" || apply.Detail != "created 1, rbac_not_writable 1, force" {
		t.Fatalf("apply audit %+v", apply)
	}
	if deletion.Op != pod.OpDelete || deletion.Kind != "ConfigMap" || deletion.Name != "env" || deletion.UID != "uid-env" || deletion.Detail != "deleted 1" {
		t.Fatalf("delete audit %+v", deletion)
	}
	for _, action := range actions {
		if line, _ := json.Marshal(action); strings.Contains(string(line), "content-not-for-the-audit") {
			t.Fatalf("the audit carries content: %s", line)
		}
	}
	if len(writer.deleted) != 1 || writer.deleted[0] != "ns1/env uid-env" {
		t.Fatalf("deleted %v", writer.deleted)
	}
	for _, op := range []string{pod.OpApply, pod.OpDelete} {
		found := false
		for _, counted := range SREActionOps {
			found = found || counted == op
		}
		if !found {
			t.Fatalf("%s is not among the counted SRE actions", op)
		}
	}
}
