// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

type nodeList struct {
	continueToken string
	options       []metav1.ListOptions
}

func (l *nodeList) ListResources(_ context.Context, _ schema.GroupVersionResource, _ string, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	l.options = append(l.options, options)
	list := &unstructured.UnstructuredList{}
	list.SetContinue(l.continueToken)
	list.Items = []unstructured.Unstructured{{Object: map[string]any{"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{"name": "n1", "uid": "u1"},
		"spec":     map[string]any{"podCIDR": "198.51.100.0/24"},
		"status":   map[string]any{"addresses": []any{map[string]any{"type": "InternalIP", "address": "192.0.2.36"}}}}}}
	return list, nil
}

func (l *nodeList) GetResource(context.Context, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error) {
	return nil, &pod.Error{Code: pod.CodeNotFound, Message: "not used"}
}

// The channel offers k8s.list: discover lists it, an invoke reads through the
// provider with the request's paths and selectors, and a page with more
// after it says how to read the next one.
func TestTheChannelListsClusterResourcesAndPointsAtTheNextPage(t *testing.T) {
	client := &postObservationClient{target: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid"}}}
	resources := &nodeList{continueToken: "page-2"}
	provider, err := pod.New(pod.Options{Client: client, Resources: resources, DefaultNamespace: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1", Operations: SREOperations(SREOptions{Pods: provider})})
	if err != nil {
		t.Fatal(err)
	}
	status, response := call(t, channel, envelope(channel, "discover", "", Params{}))
	if encoded, _ := json.Marshal(response.Result); status != 200 || !strings.Contains(string(encoded), `"k8s.list"`) {
		t.Fatalf("discover: HTTP %d %s", status, encoded)
	}
	status, response = call(t, channel, envelope(channel, "invoke", pod.OpListResources, Params{"kind": "Node", "paths": []any{"status.addresses"}, "label_selector": "role=worker", "limit": 10}))
	if status != 200 || response.Error != nil {
		t.Fatalf("invoke: HTTP %d %+v", status, response)
	}
	encoded, _ := json.Marshal(response.Result)
	if !strings.Contains(string(encoded), `"address":"192.0.2.36"`) || !strings.Contains(string(encoded), `"type":"InternalIP"`) ||
		strings.Contains(string(encoded), "podCIDR") {
		t.Fatalf("result %s, want status.addresses and not what the paths left out", encoded)
	}
	if len(resources.options) != 1 || resources.options[0].LabelSelector != "role=worker" || resources.options[0].Limit != 10 {
		t.Fatalf("options %+v", resources.options)
	}
	if len(response.Next) != 1 || response.Next[0].Operation != pod.OpListResources || response.Next[0].Params["continue"] != "page-2" ||
		response.Next[0].Params["kind"] != "Node" {
		t.Fatalf("next %+v, want the same read from page-2", response.Next)
	}
	if !strings.Contains(response.Summary, "还有更多页") {
		t.Fatalf("summary %q, want it to say more pages follow", response.Summary)
	}
}

// secretList answers a Secret list with two Secrets, or the apiserver's
// refusal.
type secretList struct{ refuse bool }

func (l secretList) ListResources(_ context.Context, resource schema.GroupVersionResource, _ string, _ metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if l.refuse {
		return nil, &pod.Error{Code: pod.CodeForbidden, Message: "refused"}
	}
	list := &unstructured.UnstructuredList{}
	for _, name := range []string{"s1", "s2"} {
		list.Items = append(list.Items, unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": resource.Resource,
			"metadata": map[string]any{"name": name, "namespace": "ns1", "uid": "uid-" + name},
			"data":     map[string]any{"password": "c2VudGluZWwtbGlzdGVk"}}})
	}
	return list, nil
}

func (secretList) GetResource(context.Context, schema.GroupVersionResource, string, string) (*unstructured.Unstructured, error) {
	return nil, &pod.Error{Code: pod.CodeNotFound, Message: "not used"}
}

// Listing Secrets is audited with the Secrets it returned, also when the
// apiserver refuses; neither the answer nor the audit carries a value. A
// list of another kind is not an SRE action.
func TestASecretListingIsAuditedWithTheNamesItReturned(t *testing.T) {
	for _, refuse := range []bool{false, true} {
		client := &postObservationClient{target: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid"}}}
		provider, err := pod.New(pod.Options{Client: client, Resources: secretList{refuse: refuse}, DefaultNamespace: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		var actions []SREAction
		channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1", Operations: SREOperations(SREOptions{Pods: provider, Audit: func(action SREAction) { actions = append(actions, action) }})})
		if err != nil {
			t.Fatal(err)
		}
		_, response := call(t, channel, envelope(channel, "invoke", pod.OpListResources, Params{"kind": "Secret", "namespace": "ns1"}))
		encoded, _ := json.Marshal(response)
		if strings.Contains(string(encoded), "sentinel") || strings.Contains(string(encoded), "c2VudGluZWwtbGlzdGVk") {
			t.Fatalf("refuse=%v answer carries a value: %s", refuse, encoded)
		}
		want := SREAction{Op: pod.OpListResources, Namespace: "ns1", Kind: "Secret", Name: "ns1/s1,ns1/s2", Detail: "secrets 2, no values", Result: SREResultDone}
		if refuse {
			want.Name, want.Detail, want.Result, want.Code = "", "secrets 0, no values", SREResultForbidden, pod.CodeForbidden
		}
		if len(actions) != 1 || actions[0].Op != want.Op || actions[0].Namespace != want.Namespace || actions[0].Kind != want.Kind ||
			actions[0].Name != want.Name || actions[0].Detail != want.Detail || actions[0].Result != want.Result || actions[0].Code != want.Code {
			t.Fatalf("refuse=%v audit %+v, want %+v", refuse, actions, want)
		}
		if encoded, _ := json.Marshal(actions[0]); strings.Contains(string(encoded), "sentinel") {
			t.Fatalf("the audit carries a value: %s", encoded)
		}
		call(t, channel, envelope(channel, "invoke", pod.OpListResources, Params{"kind": "ConfigMap", "namespace": "ns1"}))
		if len(actions) != 1 {
			t.Fatalf("a ConfigMap list was audited: %+v", actions)
		}
	}
}
