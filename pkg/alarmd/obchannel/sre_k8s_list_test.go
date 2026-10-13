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
}
