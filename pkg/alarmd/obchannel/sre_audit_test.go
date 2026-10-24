// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

// auditManager is a Kubernetes writer that records what it was asked.
type auditManager struct{ deleted []string }

func (m *auditManager) DeletePod(_ context.Context, namespace, name, uid string) error {
	m.deleted = append(m.deleted, namespace+"/"+name+"@"+uid)
	return nil
}
func (*auditManager) GetWorkload(context.Context, string, string, string) (pod.OwnerResource, error) {
	return pod.OwnerResource{}, nil
}
func (*auditManager) PatchWorkload(context.Context, string, string, string, []byte) error { return nil }

func auditFixture(t *testing.T) (*Channel, *[]SREAction, *auditManager) {
	t.Helper()
	controller := true
	client := &postObservationClient{target: corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid", Labels: map[string]string{"app": "web"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "web", UID: "workload-uid", Controller: &controller}}},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "web:v1"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "web", ContainerID: "containerd://fixture", ImageID: "sha256:fixture", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}},
	}}
	manager := &auditManager{}
	provider, err := pod.New(pod.Options{Client: client, Manager: manager, Executor: &postObservationExecutor{}, DefaultNamespace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	actions := []SREAction{}
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1", Operations: SREOperations(SREOptions{Pods: provider, Audit: func(action SREAction) {
		mu.Lock()
		defer mu.Unlock()
		actions = append(actions, action)
	}})})
	if err != nil {
		t.Fatal(err)
	}
	return channel, &actions, manager
}

// Every execution and management call is heard once, after it ends, with who
// asked (the admitted session and the request), what, where and how it
// ended; an execution's record carries the digest of its script and its
// stdin's length, never the stdin's text, and the receipt the caller gets
// back names the same request. A read is not an action.
func TestEveryExecutionAndManagementCallIsAudited(t *testing.T) {
	channel, actions, manager := auditFixture(t)
	target := map[string]any{"namespace": "fixture", "pod": "web-0", "uid": "pod-uid", "container": "web", "container_id": "containerd://fixture", "image_id": "sha256:fixture"}
	status, response := call(t, channel, envelope(channel, "invoke", "pod.exec", Params{"target": target, "argv": []any{"/bin/sh"}, "stdin": "secret-script-text"}))
	if status != 200 || response.Error != nil {
		t.Fatalf("exec: HTTP %d %+v", status, response)
	}
	if status, response := call(t, channel, envelope(channel, "invoke", "pod.logs", Params{"target": target})); status != 200 || response.Error != nil {
		t.Fatalf("logs: HTTP %d %+v", status, response)
	}
	if len(*actions) != 1 {
		t.Fatalf("heard %d actions after an exec and a read, want 1: %+v", len(*actions), *actions)
	}
	exec := (*actions)[0]
	if exec.Op != "pod.exec" || exec.Namespace != "fixture" || exec.Name != "web-0/web" || exec.UID != "pod-uid" || exec.Result != SREResultDone ||
		exec.SessionID != "post-observation-session" || exec.RequestID == "" || exec.RequestID != response.Meta.RequestID {
		t.Fatalf("exec record %+v", exec)
	}
	// The receipt the caller holds names the request its record is filed
	// under, so sre.receipts finds it by the id the reply gave.
	var receipt pod.ExecReceipt
	if encoded, err := json.Marshal(response.Result); err != nil || json.Unmarshal(encoded, &receipt) != nil || receipt.RequestID != exec.RequestID {
		t.Fatalf("exec reply request_id %q, the record's %q", receipt.RequestID, exec.RequestID)
	}
	if !strings.Contains(exec.Detail, "sha256:") || !strings.Contains(exec.Detail, "stdin_bytes 18") || strings.Contains(exec.Detail, "secret-script-text") {
		t.Fatalf("exec detail %q, want the digest and stdin length and not its text", exec.Detail)
	}
	// A Pod replaced since it was read: refused, nothing deleted, recorded
	// as target_changed.
	stale := map[string]any{"namespace": "fixture", "pod": "web-0", "uid": "old-pod-uid"}
	if _, response := call(t, channel, envelope(channel, "invoke", pod.OpDeletePod, Params{"target": stale})); response.Error == nil || response.Error.Code != "pod_"+pod.CodeTargetChanged {
		t.Fatalf("stale delete: %+v", response)
	}
	if len(manager.deleted) != 0 || len(*actions) != 2 || (*actions)[1].Op != pod.OpDeletePod || (*actions)[1].Result != SREResultTargetChanged || (*actions)[1].UID != "old-pod-uid" {
		t.Fatalf("stale delete deleted %v or was recorded as %+v", manager.deleted, *actions)
	}
	if _, response := call(t, channel, envelope(channel, "invoke", pod.OpDeletePod, Params{"target": map[string]any{"namespace": "fixture", "pod": "web-0", "uid": "pod-uid"}})); response.Error != nil {
		t.Fatalf("delete: %+v", response)
	}
	if len(manager.deleted) != 1 || manager.deleted[0] != "fixture/web-0@pod-uid" || (*actions)[2].Result != SREResultDone {
		t.Fatalf("delete deleted %v recorded %+v", manager.deleted, (*actions)[2])
	}
}

// The audit record of an execution carries the command line it ran, with
// credentials redacted and the length capped, for the sre_action line; the
// stored receipt keeps only the script's digest, never the command line.
func TestAnExecutionsAuditCarriesItsRedactedCappedArgvAndTheReceiptDoesNot(t *testing.T) {
	channel, actions, _ := auditFixture(t)
	target := map[string]any{"namespace": "fixture", "pod": "web-0", "uid": "pod-uid", "container": "web", "container_id": "containerd://fixture", "image_id": "sha256:fixture"}
	long := strings.Repeat("x", 4096)
	argv := []any{"/bin/sh", "-c", "curl -H 'Authorization: Bearer abc123' http://svc; echo password=hunter2", long}
	if status, response := call(t, channel, envelope(channel, "invoke", "pod.exec", Params{"target": target, "argv": argv})); status != 200 || response.Error != nil {
		t.Fatalf("exec: HTTP %d %+v", status, response)
	}
	exec := (*actions)[0]
	if !strings.HasPrefix(exec.Argv, "/bin/sh -c ") || !strings.Contains(exec.Argv, "<redacted>") ||
		strings.Contains(exec.Argv, "abc123") || strings.Contains(exec.Argv, "hunter2") {
		t.Fatalf("audited argv %q, want the command line with its credentials redacted", exec.Argv)
	}
	if len(exec.Argv) > pod.AuditArgvBytes+len(pod.AuditArgvTruncated) || !strings.HasSuffix(exec.Argv, pod.AuditArgvTruncated) {
		t.Fatalf("audited argv is %d bytes ending %q, want at most %d and marked cut", len(exec.Argv), exec.Argv[max(0, len(exec.Argv)-20):], pod.AuditArgvBytes)
	}
	stored, err := json.Marshal(exec)
	if err != nil || strings.Contains(string(stored), "argv") || strings.Contains(string(stored), "/bin/sh") {
		t.Fatalf("the stored receipt carries the command line: %s (%v)", stored, err)
	}
}

// The result vocabulary is closed: each provider code lands in one result,
// and a failure without a named code is failed.
func TestEverySREOutcomeLandsInOneResult(t *testing.T) {
	cases := map[string]string{
		"":                         SREResultDone,
		pod.CodeTargetChanged:      SREResultTargetChanged,
		pod.CodeForbidden:          SREResultForbidden,
		pod.RemoteUnknown:          SREResultUnknown,
		pod.CodeInvalidRequest:     SREResultRefused,
		pod.CodeCommandStartFailed: SREResultRefused,
		pod.CodeKindNotInstalled:   SREResultRefused,
		pod.CodeAPIError:           SREResultFailed,
	}
	for code, want := range cases {
		var err error
		if code != "" {
			err = &pod.Error{Code: code}
		}
		if got, _ := sreResult(err); got != want {
			t.Fatalf("code %q: result %q, want %q", code, got, want)
		}
	}
	if got, _ := sreResult(context.Canceled); got != SREResultFailed {
		t.Fatalf("an unnamed failure is %q, want failed", got)
	}
	seen := map[string]bool{}
	for _, result := range SREActionResults {
		seen[result] = true
	}
	for _, want := range cases {
		if !seen[want] {
			t.Fatalf("result %q is not in the closed list", want)
		}
	}
}

// A Secret read is audited by namespace and name and never with a value, and
// its answer carries none: neither a credential-named key's nor a plain one's.
// Asking for the values is not a parameter the read has.
func TestASecretReadIsAuditedByNameNeverByValue(t *testing.T) {
	controller := true
	client := &postObservationClient{target: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid", OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "web", UID: "workload-uid", Controller: &controller}}}}}
	secrets := secretStore{"fixture/app-redis": {ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "app-redis", UID: "secret-uid"}, Data: map[string][]byte{"password": []byte("value-never-logged"), "host": []byte("host-never-shown")}}}
	provider, err := pod.New(pod.Options{Client: client, Secrets: secrets, DefaultNamespace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var actions []SREAction
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1", Operations: SREOperations(SREOptions{Pods: provider, Audit: func(action SREAction) { actions = append(actions, action) }})})
	if err != nil {
		t.Fatal(err)
	}
	status, response := call(t, channel, envelope(channel, "invoke", pod.OpReadSecret, Params{"name": "app-redis"}))
	if status != 200 || response.Error != nil {
		t.Fatalf("HTTP %d %+v", status, response)
	}
	encoded, _ := json.Marshal(response)
	if strings.Contains(string(encoded), "value-never-logged") || strings.Contains(string(encoded), "host-never-shown") || !strings.Contains(string(encoded), `"fingerprint"`) {
		t.Fatalf("answer %s", encoded)
	}
	if status, response := call(t, channel, envelope(channel, "invoke", pod.OpReadSecret, Params{"name": "app-redis", "reveal": true})); status != 400 || response.Error == nil {
		t.Fatalf("reveal: HTTP %d %+v, want it refused as a parameter the read does not have", status, response)
	}
	if len(actions) != 1 || actions[0].Op != pod.OpReadSecret || actions[0].Namespace != "fixture" || actions[0].Name != "app-redis" || actions[0].Detail != "keys 2, no values" {
		t.Fatalf("audit %+v", actions)
	}
	for _, action := range actions {
		if encoded, _ := json.Marshal(action); strings.Contains(string(encoded), "value-never-logged") || strings.Contains(string(encoded), "host-never-shown") {
			t.Fatalf("an audit record carries a value: %s", encoded)
		}
	}
}

type secretStore map[string]*corev1.Secret

func (s secretStore) GetSecret(_ context.Context, namespace, name string) (*corev1.Secret, error) {
	if secret, found := s[namespace+"/"+name]; found {
		return secret.DeepCopy(), nil
	}
	return nil, &pod.Error{Code: pod.CodeForbidden, Message: "not on the whitelist"}
}
