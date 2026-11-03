// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	// missing answers every read with the apiserver's not-found, as for a
	// resource whose CRD is not installed or an object that does not exist.
	missing bool
	// paged answers as the apiserver pages: at most Limit objects from where
	// Continue ("at-<index>") says, and the token for the next.
	paged bool
}

func (f *resourceFixture) ListResources(_ context.Context, resource schema.GroupVersionResource, namespace string, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	f.asked = append(f.asked, fmt.Sprintf("list %s %s ns=%q", resource.GroupVersion(), resource.Resource, namespace))
	f.options = append(f.options, options)
	if f.missing {
		return nil, apierrors.NewNotFound(resource.GroupResource(), "")
	}
	list := &unstructured.UnstructuredList{}
	list.SetContinue(f.continue_)
	list.SetResourceVersion("rv-7")
	objects := f.objects
	if f.paged {
		start := 0
		_, _ = fmt.Sscanf(options.Continue, "at-%d", &start)
		end := min(start+int(options.Limit), len(objects))
		objects = objects[start:end]
		list.SetContinue("")
		if end < len(f.objects) {
			list.SetContinue(fmt.Sprintf("at-%d", end))
		}
	}
	for _, object := range objects {
		list.Items = append(list.Items, unstructured.Unstructured{Object: deepCopyJSON(object)})
	}
	return list, nil
}

func (f *resourceFixture) GetResource(_ context.Context, resource schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	f.asked = append(f.asked, fmt.Sprintf("get %s %s ns=%q %s", resource.GroupVersion(), resource.Resource, namespace, name))
	if f.missing {
		return nil, apierrors.NewNotFound(resource.GroupResource(), name)
	}
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
	return mustProvider(t, Options{Client: client, Resources: resources, DefaultNamespace: "own", LiteralSecrets: []string{"deployment-literal"}, FingerprintKey: testFingerprintKey, FingerprintScope: "deployment"}), resources
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
	for kind, want := range map[string]string{"CustomResourceDefinition": "Node, Namespace, Pod"} {
		_, err := provider.ListResources(context.Background(), ResourceRequest{Kind: kind})
		if errorCode(err) != CodeNotInScope || !strings.Contains(err.Error(), want) {
			t.Fatalf("kind %q: err=%v, want not_in_scope naming %q", kind, err, want)
		}
	}
	if len(resources.asked) != 3 {
		t.Fatalf("asked %v, want only the three granted reads", resources.asked)
	}
	if receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "secrets", Namespace: "own"}); err != nil || receipt.Kind != "Secret" {
		t.Fatalf("Secrets are listed: %+v err=%v", receipt, err)
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
		{Kind: "Pod", Limit: MaxResourceLimit + 1},
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

// A Service's EndpointSlices are read in its namespace from discovery.k8s.io,
// the group the channel's ClusterRole grants them in.
func TestAResourceReadListsAServicesEndpointSlices(t *testing.T) {
	provider, resources := resourceProvider(t, map[string]any{"apiVersion": "discovery.k8s.io/v1", "kind": "EndpointSlice", "metadata": map[string]any{"name": "s1-abc", "namespace": "ns3", "uid": "u1"}})
	receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "endpointslices", Namespace: "ns3", LabelSelector: "kubernetes.io/service-name=s1"})
	if err != nil || receipt.Kind != "EndpointSlice" || receipt.APIVersion != "discovery.k8s.io/v1" || receipt.Count != 1 {
		t.Fatalf("receipt %+v err=%v", receipt, err)
	}
	if resources.asked[0] != `list discovery.k8s.io/v1 endpointslices ns="ns3"` || resources.options[0].LabelSelector != "kubernetes.io/service-name=s1" {
		t.Fatalf("asked %v options %+v, want the Service's slices in its namespace", resources.asked, resources.options)
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

// A page over the output bound is listed again at the count that fit; when
// the apiserver's answer still does not fit -- here it ignores the limit,
// as objects that grew between the lists would -- the page keeps the
// objects that fit, says how many it left out and gives the token it was
// read with instead of the next one. An object alone over the bound comes
// back as its identity and is named.
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
	if len(resources.options) != 1+resourceListCuts {
		t.Fatalf("listed %d times, want the page and %d lists again", len(resources.options), resourceListCuts)
	}
	for _, again := range resources.options[1:] {
		if again.Limit != 2 || again.Continue != "page-2" {
			t.Fatalf("listed again with %+v, want the count that fit from the same token", again)
		}
	}
	resources.options = nil

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

// The kinds a release's ownership is read across, each from the group the
// channel's ClusterRole grants it in: service accounts, RBAC objects, volume
// claims, ConfigMaps, and the custom resources on the fixed list.
func TestAResourceReadServesTheOwnershipKinds(t *testing.T) {
	for kind, want := range map[string]string{
		"ServiceAccount":        `list v1 serviceaccounts ns="ns1"`,
		"PersistentVolumeClaim": `list v1 persistentvolumeclaims ns="ns1"`,
		"ConfigMap":             `list v1 configmaps ns="ns1"`,
		"Role":                  `list rbac.authorization.k8s.io/v1 roles ns="ns1"`,
		"RoleBinding":           `list rbac.authorization.k8s.io/v1 rolebindings ns="ns1"`,
		"ServiceMonitor":        `list monitoring.coreos.com/v1 servicemonitors ns="ns1"`,
		"PodMonitor":            `list monitoring.coreos.com/v1 podmonitors ns="ns1"`,
		"BkLogConfig":           `list bk.tencent.com/v1alpha1 bklogconfigs ns="ns1"`,
	} {
		provider, resources := resourceProvider(t)
		if _, err := provider.ListResources(context.Background(), ResourceRequest{Kind: kind, Namespace: "ns1"}); err != nil || resources.asked[0] != want {
			t.Fatalf("kind %s: asked %v err=%v, want %s", kind, resources.asked, err, want)
		}
	}
	for kind, want := range map[string]string{
		"ClusterRole":                    `list rbac.authorization.k8s.io/v1 clusterroles ns=""`,
		"ClusterRoleBinding":             `list rbac.authorization.k8s.io/v1 clusterrolebindings ns=""`,
		"DataID":                         `list monitoring.bk.tencent.com/v1beta1 dataids ns=""`,
		"ValidatingWebhookConfiguration": `list admissionregistration.k8s.io/v1 validatingwebhookconfigurations ns=""`,
		"MutatingWebhookConfiguration":   `list admissionregistration.k8s.io/v1 mutatingwebhookconfigurations ns=""`,
	} {
		provider, resources := resourceProvider(t)
		if _, err := provider.ListResources(context.Background(), ResourceRequest{Kind: kind}); err != nil || resources.asked[0] != want {
			t.Fatalf("kind %s: asked %v err=%v, want %s", kind, resources.asked, err, want)
		}
		if _, err := provider.ListResources(context.Background(), ResourceRequest{Kind: kind, Namespace: "ns1"}); errorCode(err) != CodeInvalidRequest {
			t.Fatalf("kind %s is cluster-scoped: err=%v", kind, err)
		}
	}
}

// A ConfigMap is listed as its metadata and, per key, the size of its value:
// never a value, whether the object is returned whole or projected, and the
// last-applied annotation, which holds the values again, is dropped.
func TestAConfigMapIsListedWithoutAnyValue(t *testing.T) {
	configMap := map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "app-config", "namespace": "ns1", "uid": "cm-uid", "creationTimestamp": "2026-10-01T00:00:00Z",
			"labels": map[string]any{"app.kubernetes.io/managed-by": "Helm"},
			"annotations": map[string]any{"meta.helm.sh/release-name": "release-a", "meta.helm.sh/release-namespace": "ns1",
				lastAppliedAnnotation: `{"data":{"config.yaml":"sentinel-applied"}}`},
			"ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "d1", "uid": "d-uid"}},
			"managedFields":   []any{map[string]any{"manager": "helm"}}},
		"data":       map[string]any{"config.yaml": "sentinel-data-one", "b.txt": "sentinel-data-two"},
		"binaryData": map[string]any{"blob.bin": "c2VudGluZWwtYmluYXJ5"}}
	for _, paths := range [][]string{nil, {"data", "binaryData", "metadata.labels"}} {
		provider, _ := resourceProvider(t, configMap)
		receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "configmaps", Namespace: "ns1", Paths: paths})
		if err != nil || receipt.Count != 1 {
			t.Fatalf("paths %v: receipt %+v err=%v", paths, receipt, err)
		}
		text := string(receipt.Items[0])
		for _, value := range []string{"sentinel", "c2VudGluZWwtYmluYXJ5", "managedFields", "last-applied-configuration"} {
			if strings.Contains(text, value) {
				t.Fatalf("paths %v: the item carries %q: %s", paths, value, text)
			}
		}
		item := decodeItems(t, receipt)[0]
		data, _ := json.Marshal(item["data"])
		binary, _ := json.Marshal(item["binaryData"])
		if string(data) != `[{"bytes":17,"key":"b.txt"},{"bytes":17,"key":"config.yaml"}]` || string(binary) != `[{"bytes":15,"key":"blob.bin"}]` {
			t.Fatalf("paths %v: data %s binaryData %s, want each key with its value's size, sorted", paths, data, binary)
		}
		if paths == nil {
			metadata := item["metadata"].(map[string]any)
			owners, _ := json.Marshal(metadata["ownerReferences"])
			if metadata["creationTimestamp"] != "2026-10-01T00:00:00Z" || !strings.Contains(string(owners), `"name":"d1"`) ||
				metadata["annotations"].(map[string]any)["meta.helm.sh/release-name"] != "release-a" {
				t.Fatalf("metadata %v, want its creation time, owners and Helm ownership", metadata)
			}
		}
	}
}

// What a Helm release owns is a listing across the kinds: every item keeps
// Helm's ownership annotations and label, projected or not, since paths
// cannot name a key with dots in it.
func TestEveryItemKeepsItsHelmOwnership(t *testing.T) {
	service := map[string]any{"apiVersion": "v1", "kind": "Service",
		"metadata": map[string]any{"name": "s1", "namespace": "ns1", "uid": "s-uid",
			"labels":      map[string]any{"app.kubernetes.io/managed-by": "Helm", "app": "x"},
			"annotations": map[string]any{"meta.helm.sh/release-name": "release-a", "meta.helm.sh/release-namespace": "ns1", "example.com/other": "y"}},
		"spec": map[string]any{"clusterIP": "198.51.100.7"}}
	provider, _ := resourceProvider(t, service)
	receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Service", Namespace: "ns1", LabelSelector: "app.kubernetes.io/managed-by=Helm", Paths: []string{"spec.clusterIP"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"apiVersion":"v1","kind":"Service","metadata":{"annotations":{"meta.helm.sh/release-name":"release-a","meta.helm.sh/release-namespace":"ns1"},"labels":{"app.kubernetes.io/managed-by":"Helm"},"name":"s1","namespace":"ns1","uid":"s-uid"},"spec":{"clusterIP":"198.51.100.7"}}`
	if got, _ := json.Marshal(decodeItems(t, receipt)[0]); string(got) != want {
		t.Fatalf("item = %s\nwant   %s", got, want)
	}
}

// A custom resource whose CRD the cluster does not have is said to be not
// installed, not an apiserver failure; one object that does not exist is
// still not found.
func TestACustomResourceNotInstalledIsSaidSo(t *testing.T) {
	provider, resources := resourceProvider(t)
	resources.missing = true
	if _, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "ServiceMonitor"}); errorCode(err) != CodeKindNotInstalled {
		t.Fatalf("list of a missing CRD: err=%v, want %s", err, CodeKindNotInstalled)
	}
	if _, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "ServiceMonitor", Namespace: "ns1", Name: "sm1"}); errorCode(err) != CodeNotFound {
		t.Fatalf("get of a missing object: err=%v, want %s", err, CodeNotFound)
	}
	if _, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Service"}); errorCode(err) != CodeNotFound {
		t.Fatalf("a built-in kind's not-found stays not found: err=%v", err)
	}
}

// A Secret is listed as its metadata and each key's size and fingerprint:
// never a value, listed or read by name, projected or not; the last-applied
// annotation, which holds the values again, is dropped, and the fingerprint
// is the one secret.get shows for the same value.
func TestASecretIsListedWithoutAnyValue(t *testing.T) {
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	secret := func(name, password string) map[string]any {
		return map[string]any{"apiVersion": "v1", "kind": "Secret", "type": "Opaque",
			"metadata": map[string]any{"name": name, "namespace": "ns1", "uid": "uid-" + name, "creationTimestamp": "2026-10-01T00:00:00Z",
				"labels":          map[string]any{"app.kubernetes.io/managed-by": "Helm"},
				"annotations":     map[string]any{"meta.helm.sh/release-name": "release-a", lastAppliedAnnotation: `{"stringData":{"password":"sentinel-applied"}}`},
				"ownerReferences": []any{map[string]any{"kind": "Deployment", "name": "d1"}}},
			"data": map[string]any{"password": encode(password), "url": encode("redis://user:sentinel-url@192.0.2.10:6379")}}
	}
	for _, request := range []ResourceRequest{
		{Kind: "Secret", Namespace: "ns1"},
		{Kind: "Secret", Namespace: "ns1", Paths: []string{"data", "stringData", "metadata.annotations"}},
		{Kind: "Secret", Namespace: "ns1", Name: "s1"},
	} {
		provider, _ := resourceProvider(t, secret("s1", "sentinel-shared"), secret("s2", "sentinel-shared"))
		receipt, err := provider.ListResources(context.Background(), request)
		if err != nil || receipt.FingerprintScope != "deployment" {
			t.Fatalf("request %+v: receipt %+v err=%v", request, receipt, err)
		}
		for _, raw := range receipt.Items {
			text := string(raw)
			for _, value := range []string{"sentinel", encode("sentinel-shared"), "redis://", "last-applied-configuration"} {
				if strings.Contains(text, value) {
					t.Fatalf("request %+v: the item carries %q: %s", request, value, text)
				}
			}
		}
		items := decodeItems(t, receipt)
		data, _ := json.Marshal(items[0]["data"])
		want := fmt.Sprintf(`[{"bytes":15,"fingerprint":"%s","key":"password"},{"bytes":41,"fingerprint":"%s","key":"url"}]`,
			fingerprintOf(testFingerprintKey, "sentinel-shared"), fingerprintOf(testFingerprintKey, "redis://user:sentinel-url@192.0.2.10:6379"))
		if string(data) != want {
			t.Fatalf("request %+v: data %s\nwant %s", request, data, want)
		}
		if request.Name == "" && request.Paths == nil {
			first, _ := json.Marshal(items[0]["data"])
			second, _ := json.Marshal(items[1]["data"])
			if string(first) != string(second) || items[0]["type"] != "Opaque" {
				t.Fatalf("two Secrets with one value: %s and %s, type %v", first, second, items[0]["type"])
			}
		}
	}
}

// An env value under a credential name is masked and carries the value's
// fingerprint, so a password set in place can be compared with the Secret
// that should hold it.
func TestACredentialEnvValueCarriesItsFingerprint(t *testing.T) {
	deployment := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "d1", "namespace": "ns1", "uid": "u1"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "app",
			"env": []any{map[string]any{"name": "REDIS_PASSWORD", "value": "sentinel-env"}, map[string]any{"name": "LOG_LEVEL", "value": "debug"}}}}}}}}
	provider, _ := resourceProvider(t, deployment)
	receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Deployment", Namespace: "ns1"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(receipt.Items[0])
	if strings.Contains(text, "sentinel-env") || !strings.Contains(text, `"valueFingerprint":"`+fingerprintOf(testFingerprintKey, "sentinel-env")+`"`) ||
		strings.Count(text, "valueFingerprint") != 1 {
		t.Fatalf("env = %s", text)
	}
}

// Every form a pod spec names a Secret by - a volume's source, a projected
// source, a key reference, an envFrom reference, an image pull Secret - and
// an Ingress's TLS Secret come back as they are: a reference names where a
// credential is kept, and is how a reader finds a workload's dependencies.
func TestSecretReferencesComeBackAsTheyAre(t *testing.T) {
	deployment := map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "d1", "namespace": "ns1", "uid": "u1"},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
			"volumes": []any{
				map[string]any{"name": "trace-runtime", "secret": map[string]any{"secretName": "ref-volume", "items": []any{map[string]any{"key": "ref-item", "path": "p"}}}},
				map[string]any{"name": "bundle", "projected": map[string]any{"sources": []any{map[string]any{"secret": map[string]any{"name": "ref-projected"}}}}}},
			"imagePullSecrets": []any{map[string]any{"name": "ref-pull"}},
			"containers": []any{map[string]any{"name": "app",
				"envFrom": []any{map[string]any{"secretRef": map[string]any{"name": "ref-env-from"}}},
				"env":     []any{map[string]any{"name": "DB_PASSWORD", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "ref-key", "key": "ref-key-field"}}}}}}}}}}
	ingress := map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "Ingress", "metadata": map[string]any{"name": "i1", "namespace": "ns1", "uid": "u2"},
		"spec": map[string]any{"tls": []any{map[string]any{"secretName": "ref-tls"}}}}
	for kind, object := range map[string]map[string]any{"Deployment": deployment, "Ingress": ingress} {
		provider, _ := resourceProvider(t, object)
		receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: kind, Namespace: "ns1"})
		if err != nil {
			t.Fatal(err)
		}
		var original, returned any
		encoded, _ := json.Marshal(object["spec"])
		_ = json.Unmarshal(encoded, &original)
		returned = decodeItems(t, receipt)[0]["spec"]
		if got, _ := json.Marshal(returned); string(got) != string(encoded) {
			t.Fatalf("%s spec changed:\n got %s\nwant %s", kind, got, encoded)
		}
	}
}

// A walk that follows continue reads every object, however many of each
// page the output bound lets through: a cut page is listed again at the
// count that fit, so its continue resumes after its last item. Before, a cut
// page answered with no continue, and a walker read it as the end of the
// list -- a cluster-wide Deployment list with spec stopped at 81 of several
// hundred.
func TestAWalkFollowingContinueReadsEveryObjectAcrossCutPages(t *testing.T) {
	client, _ := fixture()
	var objects []map[string]any
	for index := range 7 {
		object := node(fmt.Sprintf("n%d", index), "192.0.2.9")
		object["status"].(map[string]any)["images"] = []any{map[string]any{"names": []any{strings.Repeat("x", 300)}}}
		objects = append(objects, object)
	}
	resources := &resourceFixture{objects: objects, paged: true}
	provider := mustProvider(t, Options{Client: client, Resources: resources, MaxOutputBytes: 1500})
	var names []string
	token := ""
	for page := 0; page < 10; page++ {
		receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "Node", Limit: 5, Continue: token})
		if err != nil || receipt.Truncated || receipt.Count == 0 {
			t.Fatalf("page %d: %+v err=%v, want a whole page", page, receipt, err)
		}
		for _, item := range decodeItems(t, receipt) {
			names = append(names, item["metadata"].(map[string]any)["name"].(string))
		}
		if token = receipt.Continue; token == "" {
			break
		}
	}
	if strings.Join(names, ",") != "n0,n1,n2,n3,n4,n5,n6" {
		t.Fatalf("walked %v, want every object once in order", names)
	}
}

// A webhook configuration says which webhook gates what -- its rules,
// selectors, failure policy and the service it calls -- and never the CA
// bundle it is called under.
func TestAWebhookConfigurationComesWithoutItsCABundle(t *testing.T) {
	client, _ := fixture()
	bundle := "LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCmZpeHR1cmUK"
	webhook := map[string]any{"apiVersion": "admissionregistration.k8s.io/v1", "kind": "ValidatingWebhookConfiguration",
		"metadata": map[string]any{"name": "ingress-admission"},
		"webhooks": []any{map[string]any{"name": "validate.nginx.ingress.kubernetes.io", "failurePolicy": "Fail",
			"rules":        []any{map[string]any{"apiGroups": []any{"networking.k8s.io"}, "resources": []any{"ingresses"}, "operations": []any{"CREATE", "UPDATE"}}},
			"clientConfig": map[string]any{"caBundle": bundle, "service": map[string]any{"namespace": "ingress", "name": "admission", "port": 443}}}}}
	resources := &resourceFixture{objects: []map[string]any{webhook}}
	provider := mustProvider(t, Options{Client: client, Resources: resources})
	receipt, err := provider.ListResources(context.Background(), ResourceRequest{Kind: "ValidatingWebhookConfiguration"})
	if err != nil || receipt.Count != 1 {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	item := string(receipt.Items[0])
	if strings.Contains(item, bundle) || strings.Contains(item, "caBundle") {
		t.Fatalf("the CA bundle came back: %s", item)
	}
	for _, kept := range []string{"validate.nginx.ingress.kubernetes.io", "ingresses", `"failurePolicy":"Fail"`, `"name":"admission"`} {
		if !strings.Contains(item, kept) {
			t.Fatalf("item %s lacks %s", item, kept)
		}
	}
	// The resource the fixture holds keeps its bundle: the copy is the answer's.
	if config := webhook["webhooks"].([]any)[0].(map[string]any)["clientConfig"].(map[string]any); config["caBundle"] != bundle {
		t.Fatal("the read changed the object it read")
	}
}
