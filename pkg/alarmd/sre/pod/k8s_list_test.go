// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// resourceFixture answers a list with the objects it holds, remembering what
// it was asked.
type resourceFixture struct {
	objects   []map[string]any
	continue_ string
	asked     []string
	options   []metav1.ListOptions
}

func (f *resourceFixture) ListResources(_ context.Context, resource schema.GroupVersionResource, namespace string, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	f.asked = append(f.asked, fmt.Sprintf("list %s %s ns=%q", resource.GroupVersion(), resource.Resource, namespace))
	f.options = append(f.options, options)
	list := &unstructured.UnstructuredList{}
	list.SetContinue(f.continue_)
	list.SetResourceVersion("rv-7")
	for _, object := range f.objects {
		list.Items = append(list.Items, unstructured.Unstructured{Object: deepCopyJSON(object)})
	}
	return list, nil
}

func (f *resourceFixture) GetResource(_ context.Context, resource schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	f.asked = append(f.asked, fmt.Sprintf("get %s %s ns=%q %s", resource.GroupVersion(), resource.Resource, namespace, name))
	return &unstructured.Unstructured{Object: deepCopyJSON(f.objects[0])}, nil
}

func deepCopyJSON(object map[string]any) map[string]any {
	encoded, _ := json.Marshal(object)
	var copied map[string]any
	_ = json.Unmarshal(encoded, &copied)
	return copied
}

func node(name, internal string) map[string]any {
	return map[string]any{"apiVersion": "v1", "kind": "Node",
		"metadata": map[string]any{"name": name, "uid": "uid-" + name, "labels": map[string]any{"kubernetes.io/hostname": name},
			"managedFields": []any{map[string]any{"manager": "kubelet"}}},
		"status": map[string]any{"addresses": []any{
			map[string]any{"type": "InternalIP", "address": internal},
			map[string]any{"type": "Hostname", "address": name}},
			"images": []any{map[string]any{"names": []any{"registry.example/image:1"}, "sizeBytes": 1}}}}
}

func resourceProvider(t *testing.T, objects ...map[string]any) (*Provider, *resourceFixture) {
	t.Helper()
	client, _ := fixture()
	resources := &resourceFixture{objects: objects}
	return mustProvider(t, Options{Client: client, Resources: resources, DefaultNamespace: "own", LiteralSecrets: []string{"deployment-literal"}}), resources
}

func decodeItems(t *testing.T, receipt ResourceReceipt) []map[string]any {
	t.Helper()
	items := make([]map[string]any, 0, len(receipt.Items))
	for _, raw := range receipt.Items {
		var item map[string]any
		if err := json.Unmarshal(raw, &item); err != nil {
			t.Fatal(err)
		}
		items = append(items, item)
	}
	return items
}

// A kind is named by its Kind or its plural, in any case; a kind outside the
// list is refused with the list, and Secrets and ConfigMaps are sent to the
// operations that mask them.
func TestAResourceReadServesTheGrantedKindsOnly(t *testing.T) {
	provider, resources := resourceProvider(t, node("n1", "192.0.2.1"))
	for _, kind := range []string{"Node", "nodes", "NODE"} {
		if receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: kind}); err != nil || receipt.Kind != "Node" || receipt.APIVersion != "v1" {
			t.Fatalf("kind %q: %+v err=%v", kind, receipt, err)
		}
	}
	for kind, want := range map[string]string{"Secret": OpReadSecret, "configmaps": OpReadConfigMap, "CustomResourceDefinition": "Node, Namespace, Pod"} {
		_, err := provider.ListResources(context.Background(), ResourceRequest{Kind: kind})
		if errorCode(err) != CodeNotInScope || !strings.Contains(err.Error(), want) {
			t.Fatalf("kind %q: err=%v, want not_in_scope naming %q", kind, err, want)
		}
	}
	if len(resources.asked) != 3 {
		t.Fatalf("asked %v, want only the three granted reads", resources.asked)
	}
	if _, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Node", Namespace: "own"}); errorCode(err) != CodeInvalidRequest {
		t.Fatalf("a cluster-scoped kind with a namespace: err=%v, want invalid_request", err)
	}
}

// No namespace is the whole cluster, not this process's own namespace; one
// named is that one. Selectors, the continue token and the limit go to the
// apiserver as given, and its continue token and version come back.
func TestAResourceListIsClusterWideUnlessANamespaceIsNamed(t *testing.T) {
	provider, resources := resourceProvider(t, map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "p1", "namespace": "ns1", "uid": "u1"}})
	resources.continue_ = "next-token"
	receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "pods", FieldSelector: "spec.nodeName=n1", LabelSelector: "app=x", Continue: "token-1", Limit: 7})
	if err != nil || !receipt.ClusterWide || receipt.Namespace != "" || receipt.Continue != "next-token" || receipt.ResourceVersion != "rv-7" || receipt.Count != 1 {
		t.Fatalf("receipt %+v err=%v", receipt, err)
	}
	if resources.asked[0] != `list v1 pods ns=""` {
		t.Fatalf("asked %v, want a cluster-wide list", resources.asked)
	}
	options := resources.options[0]
	if options.FieldSelector != "spec.nodeName=n1" || options.LabelSelector != "app=x" || options.Continue != "token-1" || options.Limit != 7 {
		t.Fatalf("options %+v, want the request's selectors, token and limit", options)
	}
	if _, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Deployment", Namespace: "ns2"}); err != nil || resources.asked[1] != `list apps/v1 deployments ns="ns2"` {
		t.Fatalf("asked %v err=%v, want the named namespace", resources.asked, err)
	}
	if _, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Pod"}); err != nil || resources.options[2].Limit != DefaultResourceLimit {
		t.Fatalf("default limit %d err=%v, want %d", resources.options[2].Limit, err, DefaultResourceLimit)
	}
	for _, request := range []ResourceRequest{
		{Kind: "Pod", Namespace: "ns1", Name: "p1", LabelSelector: "app=x"},
		{Kind: "Pod", Name: "p1"},
		{Kind: "Pod", Limit: 101},
		{Kind: "Pod", LabelSelector: "app in (x"},
		{Kind: "Pod", Namespace: "Not_A_Namespace"},
		{Kind: "Pod", Paths: []string{"status..phase"}},
	} {
		if _, err := provider.ListResources(context.Background(), request); errorCode(err) != CodeInvalidRequest {
			t.Fatalf("request %+v: err=%v, want invalid_request", request, err)
		}
	}
	if len(resources.asked) != 3 {
		t.Fatalf("an invalid request reached the apiserver: %v", resources.asked)
	}
}

// A name reads one object with a GET.
func TestAResourceReadByNameGetsOneObject(t *testing.T) {
	provider, resources := resourceProvider(t, node("192.0.2.36", "192.0.2.36"))
	receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Node", Name: "192.0.2.36"})
	if err != nil || receipt.Count != 1 || resources.asked[0] != `get v1 nodes ns="" 192.0.2.36` {
		t.Fatalf("receipt %+v err=%v asked=%v", receipt, err, resources.asked)
	}
}

// What comes back is cleaned: managedFields and the last-applied annotation
// are gone, a credential-named annotation and env value are masked whole,
// other env values stay, a credential inside any string is redacted, and a
// reference to a Secret by name is kept.
func TestAResourceReadCleansWhatItReturns(t *testing.T) {
	deployment := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "d1", "namespace": "ns1", "uid": "u1",
			"managedFields": []any{map[string]any{"manager": "helm"}},
			"annotations": map[string]any{
				lastAppliedAnnotation:      `{"env":[{"name":"DB_PASSWORD","value":"applied-plain"}]}`,
				"example.com/api-token":    "annotation-plain",
				"example.com/owner":        "team-a",
				"example.com/connect-hint": "redis://user:url-plain@192.0.2.10:6379"}},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{
			"name": "app", "image": "registry.example/app:1", "args": []any{"--password=arg-plain"},
			"env": []any{
				map[string]any{"name": "REDIS_PASSWORD", "value": "env-plain"},
				map[string]any{"name": "LOG_LEVEL", "value": "debug"},
				map[string]any{"name": "LITERAL", "value": "deployment-literal"},
				map[string]any{"name": "FROM_SECRET", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "app-secret", "key": "password"}}},
			}}}}}}}
	provider, _ := resourceProvider(t, deployment)
	receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Deployment", Namespace: "ns1"})
	if err != nil || receipt.Count != 1 {
		t.Fatalf("receipt %+v err=%v", receipt, err)
	}
	text := string(receipt.Items[0])
	for _, gone := range []string{"managedFields", "last-applied-configuration", "applied-plain", "annotation-plain", "env-plain", "url-plain", "arg-plain", "deployment-literal"} {
		if strings.Contains(text, gone) {
			t.Fatalf("item carries %q: %s", gone, text)
		}
	}
	for _, kept := range []string{`"example.com/owner":"team-a"`, `"value":"debug"`, `"name":"app-secret"`, `"key":"password"`, `"image":"registry.example/app:1"`} {
		if !strings.Contains(text, kept) {
			t.Fatalf("item lost %q: %s", kept, text)
		}
	}
}

// Paths keep only what they name, beside the object's identity; a list on
// the way is kept element by element.
func TestAResourceReadProjectsThePathsItIsGiven(t *testing.T) {
	provider, _ := resourceProvider(t, node("n1", "192.0.2.1"), node("n2", "192.0.2.2"))
	receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Node", Paths: []string{"status.addresses.address", "status.addresses.type"}})
	if err != nil {
		t.Fatal(err)
	}
	items := decodeItems(t, receipt)
	want := `{"apiVersion":"v1","kind":"Node","metadata":{"name":"n1","uid":"uid-n1"},"status":{"addresses":[{"address":"192.0.2.1","type":"InternalIP"},{"address":"n1","type":"Hostname"}]}}`
	if got, _ := json.Marshal(items[0]); string(got) != want {
		t.Fatalf("item = %s\nwant   %s", got, want)
	}
}

// A page over the output bound keeps the objects that fit, says how many it
// left out and gives the token it was read with instead of the next one; an
// object alone over the bound comes back as its identity and is named.
func TestAResourceReadStaysWithinTheOutputBound(t *testing.T) {
	big := func(name string, size int) map[string]any {
		object := node(name, "192.0.2.9")
		object["status"].(map[string]any)["images"] = []any{map[string]any{"names": []any{strings.Repeat("x", size)}}}
		return object
	}
	client, _ := fixture()
	resources := &resourceFixture{objects: []map[string]any{big("n1", 300), big("n2", 300), big("n3", 300)}, continue_: "after-n3"}
	provider := mustProvider(t, Options{Client: client, Resources: resources, MaxOutputBytes: 1500})
	receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Node", Continue: "page-2"})
	if err != nil || receipt.Count != 2 || !receipt.Truncated || receipt.ItemsOmitted != 1 || receipt.Continue != "" || receipt.ReadWith != "page-2" {
		t.Fatalf("receipt count=%d truncated=%t omitted=%d continue=%q read_with=%q err=%v", receipt.Count, receipt.Truncated, receipt.ItemsOmitted, receipt.Continue, receipt.ReadWith, err)
	}

	resources.objects = []map[string]any{big("n4", 4000), node("n5", "192.0.2.5")}
	receipt, err = provider.ListResources(context.Background(), ResourceRequest{Kind: "Node"})
	if err != nil || receipt.Count != 2 || receipt.Truncated || receipt.Continue != "after-n3" || len(receipt.OversizedItems) != 1 || receipt.OversizedItems[0] != "/n4" {
		t.Fatalf("receipt %+v err=%v", receipt, err)
	}
	items := decodeItems(t, receipt)
	if _, hasStatus := items[0]["status"]; hasStatus || items[0]["metadata"].(map[string]any)["name"] != "n4" {
		t.Fatalf("oversized item = %v, want its identity only", items[0])
	}
}

func errorCode(err error) string {
	var named *Error
	if errors.As(err, &named) {
		return named.Code
	}
	return ""
}
