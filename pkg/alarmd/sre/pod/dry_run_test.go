// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/client-go/rest"
)

// Every dry run's write is a server-side apply with dryRun=All, under its
// own field manager, forcing ownership -- set by the adapter, whatever the
// caller sent. The apiserver persists nothing for such a request.
func TestEveryDryRunApplyIsDryRunAll(t *testing.T) {
	var mu sync.Mutex
	var seen []*http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Clone(r.Context()))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm","namespace":"ns1"}}`))
	}))
	defer server.Close()
	client, err := NewKubernetesClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	resource := schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	if _, err := client.ApplyDryRun(context.Background(), resource, "ns1", "cm", []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm"}}`)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 {
		t.Fatalf("%d requests, want one", len(seen))
	}
	request := seen[0]
	query := request.URL.Query()
	if request.Method != http.MethodPatch || request.Header.Get("Content-Type") != "application/apply-patch+yaml" ||
		request.URL.Path != "/api/v1/namespaces/ns1/configmaps/cm" {
		t.Fatalf("request %s %s %s, want a server-side apply of the object", request.Method, request.URL.Path, request.Header.Get("Content-Type"))
	}
	if query["dryRun"] == nil || len(query["dryRun"]) != 1 || query.Get("dryRun") != "All" {
		t.Fatalf("query %v, want dryRun=All", query)
	}
	if query.Get("fieldManager") != "alarmd-dry-run" || query.Get("force") != "true" {
		t.Fatalf("query %v, want its own field manager and force", query)
	}
}

type fakeDryRunner struct {
	mappings map[string]schema.GroupVersionResource
	cluster  map[string]bool
	live     map[string]*unstructured.Unstructured
	applied  func(name string, object map[string]any) (*unstructured.Unstructured, error)
	getErr   error
	asked    []string
}

// Mapping parses apiVersion as the cluster client does, so a spelling the
// parser accepts ("/v1") resolves here as it would on a cluster.
func (f *fakeDryRunner) Mapping(apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
	version, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, false, err
	}
	resource, ok := f.mappings[version.String()+"/"+kind]
	if !ok {
		return schema.GroupVersionResource{}, false, errors.New("no matches for kind")
	}
	return resource, !f.cluster[kind], nil
}

func (f *fakeDryRunner) GetResource(_ context.Context, resource schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	f.asked = append(f.asked, fmt.Sprintf("get %s %s/%s", resource.Resource, namespace, name))
	if f.getErr != nil {
		return nil, f.getErr
	}
	if live, ok := f.live[namespace+"/"+name]; ok {
		return live.DeepCopy(), nil
	}
	return nil, apierrors.NewNotFound(resource.GroupResource(), name)
}

func (f *fakeDryRunner) ApplyDryRun(_ context.Context, resource schema.GroupVersionResource, namespace, name string, data []byte) (*unstructured.Unstructured, error) {
	f.asked = append(f.asked, fmt.Sprintf("apply %s %s/%s", resource.Resource, namespace, name))
	object := &unstructured.Unstructured{}
	if err := object.UnmarshalJSON(data); err != nil {
		return nil, err
	}
	if f.applied != nil {
		return f.applied(name, object.Object)
	}
	return object, nil
}

func dryRunProvider(t *testing.T, runner *fakeDryRunner) *Provider {
	t.Helper()
	client, _ := fixture()
	if runner.mappings == nil {
		runner.mappings = map[string]schema.GroupVersionResource{
			"v1/ConfigMap":                 {Version: "v1", Resource: "configmaps"},
			"v1/Secret":                    {Version: "v1", Resource: "secrets"},
			"networking.k8s.io/v1/Ingress": {Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"},
			"example.test/v1/ClusterThing": {Group: "example.test", Version: "v1", Resource: "clusterthings"},
		}
		runner.cluster = map[string]bool{"ClusterThing": true}
	}
	return mustProvider(t, Options{Client: client, DryRun: runner})
}

func configMap(namespace, name string, data map[string]any) *unstructured.Unstructured {
	object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": name}, "data": data}}
	if namespace != "" {
		object.SetNamespace(namespace)
	}
	return object
}

const ingressManifest = `
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: uq
  namespace: ns1
spec:
  rules:
  - host: uq.example.test
---
`

// The manifests a chart renders come back one verdict each, in order: an
// object that does not exist would be created, one that would end as it is
// is unchanged, one that would change would be updated; an empty document is
// skipped and a namespaced object without a namespace takes the request's.
func TestADryRunAnswersEachObject(t *testing.T) {
	helmOwned := configMap("ns1", "same", map[string]any{"k": "v"})
	helmOwned.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "helm", Operation: metav1.ManagedFieldsOperationUpdate}})
	helmOwned.SetResourceVersion("41")
	runner := &fakeDryRunner{live: map[string]*unstructured.Unstructured{
		"ns1/same":    helmOwned,
		"ns1/changed": configMap("ns1", "changed", map[string]any{"k": "old"}),
	}}
	provider := dryRunProvider(t, runner)
	manifests := ingressManifest + `
apiVersion: v1
kind: ConfigMap
metadata: {name: same, namespace: ns1}
data: {k: v}
---
---
{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"changed"},"data":{"k":"new"}}
`
	receipt, err := provider.DryRun(context.Background(), DryRunRequest{Manifests: manifests, Namespace: "ns1"})
	if err != nil {
		t.Fatal(err)
	}
	var verdicts []string
	for _, object := range receipt.Objects {
		verdicts = append(verdicts, object.Kind+"/"+object.Namespace+"/"+object.Name+"="+object.Verdict)
	}
	if strings.Join(verdicts, " ") != "Ingress/ns1/uq=would_create ConfigMap/ns1/same=unchanged ConfigMap/ns1/changed=would_update" {
		t.Fatalf("verdicts %v", verdicts)
	}
	if !receipt.Clean || receipt.Counts[DryRunWouldCreate] != 1 || receipt.Counts[DryRunUnchanged] != 1 || receipt.Counts[DryRunWouldUpdate] != 1 {
		t.Fatalf("receipt %+v, want clean with one of each", receipt)
	}
	// The object without a namespace was asked about in the request's.
	if runner.asked[len(runner.asked)-1] != "apply configmaps ns1/changed" {
		t.Fatalf("asked %v", runner.asked)
	}
}

// A refusal comes back with the apiserver's status verbatim -- the
// admission webhook's denial of a duplicate host is a denial -- and the run
// is not clean; a field ownership conflict is its own word, not a denial.
func TestARefusalIsTheApiserversWordsAndAConflictIsNotADenial(t *testing.T) {
	webhook := &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: 400, Reason: metav1.StatusReasonBadRequest,
		Message: `admission webhook "validate.nginx.ingress.kubernetes.io" denied the request: host "uq.example.test" and path "/" is already defined in ingress ns0/old`}}
	runner := &fakeDryRunner{applied: func(name string, object map[string]any) (*unstructured.Unstructured, error) {
		switch name {
		case "uq":
			return nil, webhook
		case "owned":
			return nil, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, "owned", errors.New("conflict with \"helm\" using v1: .data.k"))
		}
		return &unstructured.Unstructured{Object: object}, nil
	}}
	provider := dryRunProvider(t, runner)
	receipt, err := provider.DryRun(context.Background(), DryRunRequest{Manifests: ingressManifest + `
apiVersion: v1
kind: ConfigMap
metadata: {name: owned, namespace: ns1}
`})
	if err != nil {
		t.Fatal(err)
	}
	denied, conflict := receipt.Objects[0], receipt.Objects[1]
	if denied.Verdict != DryRunDenied || denied.Code != 400 || denied.Reason != "BadRequest" || denied.Message != webhook.ErrStatus.Message {
		t.Fatalf("denied = %+v, want the webhook's status verbatim", denied)
	}
	if conflict.Verdict != DryRunManagerConflict || conflict.Code != 409 {
		t.Fatalf("conflict = %+v, want manager_conflict", conflict)
	}
	if receipt.Clean || receipt.Counts[DryRunDenied] != 1 || receipt.Counts[DryRunManagerConflict] != 1 {
		t.Fatalf("receipt %+v, want not clean", receipt)
	}
}

// The causes of an invalid object come back as given; a Secret's own
// values, which the apiserver can quote back, are masked in every message,
// encoded or decoded.
func TestARefusalCarriesItsCausesAndNeverASecretValue(t *testing.T) {
	plain := "s3cret-value-for-the-test"
	decoded := "an0ther-value-held-in-data"
	encoded := base64.StdEncoding.EncodeToString([]byte(decoded))
	runner := &fakeDryRunner{applied: func(name string, object map[string]any) (*unstructured.Unstructured, error) {
		return nil, apierrors.NewInvalid(schema.GroupKind{Kind: "Secret"}, name, []*field.Error{
			field.Invalid(field.NewPath("data").Key("token"), plain, "is not valid"),
			field.Invalid(field.NewPath("data").Key("raw"), encoded, "is not valid"),
			field.Invalid(field.NewPath("data").Key("raw"), decoded, "decodes to an invalid value"),
		})
	}}
	provider := dryRunProvider(t, runner)
	receipt, err := provider.DryRun(context.Background(), DryRunRequest{Manifests: fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata: {name: creds, namespace: ns1}
data: {raw: %s}
stringData: {token: %s}
`, encoded, plain)})
	if err != nil {
		t.Fatal(err)
	}
	object := receipt.Objects[0]
	if object.Verdict != DryRunDenied || object.Code != 422 || len(object.Causes) != 3 || object.Causes[0].Field != "data[token]" {
		t.Fatalf("object = %+v, want invalid with its three causes", object)
	}
	text := fmt.Sprintf("%+v", object)
	if strings.Contains(text, plain) || strings.Contains(text, encoded) || strings.Contains(text, decoded) || !strings.Contains(text, "<redacted>") {
		t.Fatalf("a Secret value came back: %s", text)
	}
}

// What cannot be asked about is said so, object by object, and the run is
// not clean: a granted kind the cluster does not serve (a CRD not
// installed), an object without a name, a namespaced object with no
// namespace anywhere, an apiVersion not in its canonical form (the parser
// takes "/v1" for core v1; it is refused, not resolved), and a call that
// got no status back.
func TestWhatCannotBeAskedAboutIsNotChecked(t *testing.T) {
	runner := &fakeDryRunner{}
	provider := dryRunProvider(t, runner)
	receipt, err := provider.DryRun(context.Background(), DryRunRequest{Manifests: `
apiVersion: bk.tencent.com/v1alpha1
kind: BkLogConfig
metadata: {name: logs, namespace: ns1}
---
apiVersion: v1
kind: ConfigMap
metadata: {generateName: cm-, namespace: ns1}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: nowhere}
---
apiVersion: /v1
kind: ConfigMap
metadata: {name: spelled, namespace: ns1}
`})
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range receipt.Objects {
		if object.Verdict != DryRunNotChecked || object.Message == "" {
			t.Fatalf("object %+v, want not_checked with why", object)
		}
	}
	if receipt.Clean || receipt.Counts[DryRunNotChecked] != 4 || len(runner.asked) != 0 {
		t.Fatalf("receipt %+v asked %v, want four not checked and nothing asked", receipt, runner.asked)
	}
	if spelled := receipt.Objects[3]; !strings.Contains(spelled.Message, `write "v1"`) {
		t.Fatalf("object %+v, want the canonical spelling named", spelled)
	}
	transport := &fakeDryRunner{getErr: errors.New("dial tcp: connection refused")}
	receipt, err = dryRunProvider(t, transport).DryRun(context.Background(), DryRunRequest{Manifests: ingressManifest})
	if err != nil || receipt.Objects[0].Verdict != DryRunNotChecked || !strings.Contains(receipt.Objects[0].Message, "connection refused") {
		t.Fatalf("receipt %+v err %v, want not_checked with the transport's word", receipt, err)
	}
}

// A run is bounded before anything is asked: by bytes, by objects, and it
// must hold an object and decode.
func TestADryRunIsBounded(t *testing.T) {
	provider := dryRunProvider(t, &fakeDryRunner{})
	many := strings.Repeat("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, namespace: ns1}\n---\n", MaxDryRunObjects+1)
	for _, c := range []struct {
		name, manifests, code string
	}{
		{"too large", strings.Repeat("#", MaxDryRunBytes+1), "dry_run_too_large"},
		{"too many", many, "dry_run_too_many_objects"},
		{"empty", "---\n---\n", CodeInvalidRequest},
		{"not YAML", "kind: [unclosed", CodeInvalidRequest},
	} {
		if _, err := provider.DryRun(context.Background(), DryRunRequest{Manifests: c.manifests}); errorCode(err) != c.code {
			t.Errorf("%s: err %v, want %s", c.name, err, c.code)
		}
	}
	exactly := strings.Repeat("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, namespace: ns1}\n---\n", MaxDryRunObjects)
	if receipt, err := provider.DryRun(context.Background(), DryRunRequest{Manifests: exactly}); err != nil || len(receipt.Objects) != MaxDryRunObjects {
		t.Fatalf("%d objects: %v, want all answered", MaxDryRunObjects, err)
	}
}

// A List is its items, each answered.
func TestAListIsItsItems(t *testing.T) {
	receipt, err := dryRunProvider(t, &fakeDryRunner{}).DryRun(context.Background(), DryRunRequest{Manifests: `{"apiVersion":"v1","kind":"List","items":[
		{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"a","namespace":"ns1"}},
		{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"b","namespace":"ns1"}}]}`})
	if err != nil || len(receipt.Objects) != 2 || receipt.Objects[1].Name != "b" {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
}

// A Role, ClusterRole or binding is not sent: it comes back as its own
// verdict, the run stays clean, and nothing is asked about it.
func TestRBACObjectsAreNotSentAndDoNotMakeARunUnclean(t *testing.T) {
	runner := &fakeDryRunner{}
	receipt, err := dryRunProvider(t, runner).DryRun(context.Background(), DryRunRequest{Manifests: `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata: {name: reader}
rules: [{apiGroups: [""], resources: [secrets], verbs: [get]}]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata: {name: bind, namespace: ns1}
`})
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range receipt.Objects {
		if object.Verdict != DryRunRBACNotDryRun || !strings.Contains(object.Message, "bind and escalate") {
			t.Fatalf("object %+v, want rbac_not_dry_run with why", object)
		}
	}
	if !receipt.Clean || len(runner.asked) != 0 || receipt.Counts[DryRunRBACNotDryRun] != 2 {
		t.Fatalf("receipt %+v asked %v, want clean and nothing asked", receipt, runner.asked)
	}
}

// A kind the channel is not granted a dry run on is refused before any
// call, by name, and makes the run not clean: a chart that starts rendering
// a new kind shows as a grant to extend.
func TestAKindOutsideTheGrantIsRefusedBeforeAnyCall(t *testing.T) {
	runner := &fakeDryRunner{}
	receipt, err := dryRunProvider(t, runner).DryRun(context.Background(), DryRunRequest{Manifests: `
apiVersion: example.test/v1
kind: ClusterThing
metadata: {name: thing}
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata: {name: fast}
`})
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range receipt.Objects {
		if object.Verdict != DryRunNotGrantedKind || !strings.Contains(object.Message, "extend the chart's grant") {
			t.Fatalf("object %+v, want not_granted_kind", object)
		}
	}
	if receipt.Clean || len(runner.asked) != 0 {
		t.Fatalf("receipt %+v asked %v, want not clean and nothing asked", receipt, runner.asked)
	}
}

// A field ownership conflict alone makes a run not clean: it is not a
// denial, and it is not nothing either.
func TestAConflictAloneMakesARunNotClean(t *testing.T) {
	runner := &fakeDryRunner{applied: func(name string, object map[string]any) (*unstructured.Unstructured, error) {
		return nil, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, name, errors.New("conflict with \"helm\""))
	}}
	receipt, err := dryRunProvider(t, runner).DryRun(context.Background(), DryRunRequest{Manifests: "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, namespace: ns1}\n"})
	if err != nil || receipt.Clean || receipt.Counts[DryRunManagerConflict] != 1 {
		t.Fatalf("receipt %+v err %v, want a lone conflict to make the run not clean", receipt, err)
	}
}
