// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

// ingressDenier serves ConfigMap and Ingress, and denies every Ingress the
// way the ingress controller's admission webhook does.
type ingressDenier struct{}

func (ingressDenier) Mapping(apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
	if kind == "Ingress" {
		return schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, true, nil
	}
	return schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, true, nil
}

func (ingressDenier) GetResource(_ context.Context, resource schema.GroupVersionResource, _, name string) (*unstructured.Unstructured, error) {
	return nil, apierrors.NewNotFound(resource.GroupResource(), name)
}

func (ingressDenier) ApplyDryRun(_ context.Context, resource schema.GroupVersionResource, _, _ string, data []byte) (*unstructured.Unstructured, error) {
	if resource.Resource == "ingresses" {
		return nil, &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: 400, Reason: metav1.StatusReasonBadRequest,
			Message: `admission webhook "validate.nginx.ingress.kubernetes.io" denied the request: host "uq.example.test" and path "/" is already defined`}}
	}
	object := &unstructured.Unstructured{}
	return object, object.UnmarshalJSON(data)
}

// The channel offers k8s.dry_run with its fixed dry run stated, answers a
// denied Ingress as not clean with the webhook's words, and audits the call
// with the objects it asked about and how each came out -- never their
// content.
func TestTheChannelDryRunsManifestsAndAuditsTheObjects(t *testing.T) {
	client := &postObservationClient{target: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid"}}}
	provider, err := pod.New(pod.Options{Client: client, DryRun: ingressDenier{}, DefaultNamespace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var actions []SREAction
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1",
		Operations: SREOperations(SREOptions{Pods: provider, Audit: func(action SREAction) { actions = append(actions, action) }})})
	if err != nil {
		t.Fatal(err)
	}
	status, response := call(t, channel, envelope(channel, "describe", pod.OpDryRun, Params{}))
	if encoded, _ := json.Marshal(response.Result); status != 200 || !strings.Contains(string(encoded), `"dry_run":"All (fixed)"`) {
		t.Fatalf("describe: HTTP %d %s", status, encoded)
	}
	manifests := "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata: {name: uq, namespace: ns1}\n---\n" +
		"apiVersion: v1\nkind: ConfigMap\nmetadata: {name: env}\ndata: {mode: content-not-for-the-audit}\n"
	status, response = call(t, channel, envelope(channel, "invoke", pod.OpDryRun, Params{"manifests": manifests, "namespace": "ns1"}))
	if status != 200 || response.Error != nil {
		t.Fatalf("invoke: HTTP %d %+v", status, response)
	}
	encoded, _ := json.Marshal(response.Result)
	if !strings.Contains(string(encoded), `"clean":false`) || !strings.Contains(string(encoded), `is already defined`) ||
		!strings.HasPrefix(response.Summary, "dry run not clean: would_create 1, denied 1") {
		t.Fatalf("summary %q result %s", response.Summary, encoded)
	}
	if len(actions) != 1 || actions[0].Op != pod.OpDryRun || actions[0].Kind != "objects" || actions[0].Namespace != "ns1" ||
		actions[0].Name != "Ingress/ns1/uq,ConfigMap/ns1/env" || actions[0].Detail != "would_create 1, denied 1" || actions[0].Result != SREResultDone {
		t.Fatalf("audit %+v", actions)
	}
	if line, _ := json.Marshal(actions[0]); strings.Contains(string(line), "content-not-for-the-audit") {
		t.Fatalf("the audit carries content: %s", line)
	}
	// A render of several hundred KiB is one call: the op takes requests up to
	// the envelope, not the 64 KiB a read takes.
	big := manifests + "---\napiVersion: v1\nkind: ConfigMap\nmetadata: {name: big}\ndata: {k: " + strings.Repeat("x", 600<<10) + "}\n"
	status, response = call(t, channel, envelope(channel, "invoke", pod.OpDryRun, Params{"manifests": big, "namespace": "ns1"}))
	if status != 200 || response.Error != nil {
		t.Fatalf("a 600 KiB render: HTTP %d %+v", status, response.Error)
	}
	found := false
	for _, op := range SREActionOps {
		found = found || op == pod.OpDryRun
	}
	if !found {
		t.Fatal("k8s.dry_run is not among the counted SRE actions")
	}
}

// The audit line names at most twenty objects, then how many more.
func TestADryRunAuditNamesTwentyObjectsThenHowManyMore(t *testing.T) {
	client := &postObservationClient{target: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid"}}}
	provider, err := pod.New(pod.Options{Client: client, DryRun: ingressDenier{}, DefaultNamespace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var actions []SREAction
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1",
		Operations: SREOperations(SREOptions{Pods: provider, Audit: func(action SREAction) { actions = append(actions, action) }})})
	if err != nil {
		t.Fatal(err)
	}
	var manifests strings.Builder
	for index := range 25 {
		fmt.Fprintf(&manifests, "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: cm-%02d, namespace: ns1}\n---\n", index)
	}
	call(t, channel, envelope(channel, "invoke", pod.OpDryRun, Params{"manifests": manifests.String()}))
	if len(actions) != 1 {
		t.Fatalf("audit %+v", actions)
	}
	names := strings.Split(actions[0].Name, ",")
	if len(names) != 21 || names[0] != "ConfigMap/ns1/cm-00" || names[19] != "ConfigMap/ns1/cm-19" || names[20] != "+5 more" {
		t.Fatalf("audit names %v, want twenty then +5 more", names)
	}
}
