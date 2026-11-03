// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

// An apply is a persisted server-side apply under the channel's own field
// manager, forcing ownership only when asked and never a dry run; a delete
// carries the uid (and resource version) precondition and deletes the
// dependents in the background.
func TestTheWritesCarryTheirFixedOptions(t *testing.T) {
	var mu sync.Mutex
	var seen []*http.Request
	var bodies []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen, bodies = append(seen, r.Clone(r.Context())), append(bodies, string(body))
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
	for _, force := range []bool{false, true} {
		if _, err := client.Apply(context.Background(), resource, "ns1", "cm", []byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"cm"}}`), force); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.Delete(context.Background(), resource, "ns1", "cm", types.UID("uid-7"), "41"); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("%d requests", len(seen))
	}
	for index, force := range []string{"false", "true"} {
		request, query := seen[index], seen[index].URL.Query()
		if request.Method != http.MethodPatch || request.Header.Get("Content-Type") != "application/apply-patch+yaml" ||
			query.Get("fieldManager") != "alarmd-sre" || query.Get("force") != force || query.Has("dryRun") {
			t.Fatalf("apply %d: %s %s %v, want a persisted apply as alarmd-sre with force=%s", index, request.Method, request.Header.Get("Content-Type"), query, force)
		}
	}
	if seen[2].Method != http.MethodDelete || seen[2].URL.Path != "/api/v1/namespaces/ns1/configmaps/cm" {
		t.Fatalf("delete %s %s", seen[2].Method, seen[2].URL.Path)
	}
	var options metav1.DeleteOptions
	if err := json.Unmarshal([]byte(bodies[2]), &options); err != nil {
		t.Fatal(err)
	}
	if options.Preconditions == nil || options.Preconditions.UID == nil || *options.Preconditions.UID != "uid-7" ||
		options.Preconditions.ResourceVersion == nil || *options.Preconditions.ResourceVersion != "41" ||
		options.PropagationPolicy == nil || *options.PropagationPolicy != metav1.DeletePropagationBackground || len(options.DryRun) != 0 {
		t.Fatalf("delete options %s, want the uid and version preconditions and background propagation", bodies[2])
	}
}

type fakeWriter struct {
	runner  *fakeDryRunner
	applied func(name string, object map[string]any, force bool) (*unstructured.Unstructured, error)
	deleted func(name string, uid types.UID) error
	forces  []bool
	asked   []string
}

func (f *fakeWriter) Apply(_ context.Context, resource schema.GroupVersionResource, namespace, name string, data []byte, force bool) (*unstructured.Unstructured, error) {
	f.forces = append(f.forces, force)
	f.asked = append(f.asked, fmt.Sprintf("apply %s %s/%s", resource.Resource, namespace, name))
	object := &unstructured.Unstructured{}
	if err := object.UnmarshalJSON(data); err != nil {
		return nil, err
	}
	if f.applied != nil {
		return f.applied(name, object.Object, force)
	}
	if object.GetUID() == "" {
		object.SetUID(types.UID("uid-" + name))
	}
	return object, nil
}

func (f *fakeWriter) Delete(_ context.Context, resource schema.GroupVersionResource, namespace, name string, uid types.UID, _ string) error {
	f.asked = append(f.asked, fmt.Sprintf("delete %s %s/%s %s", resource.Resource, namespace, name, uid))
	if f.deleted != nil {
		return f.deleted(name, uid)
	}
	return nil
}

func (f *fakeWriter) JSONPatch(_ context.Context, resource schema.GroupVersionResource, namespace, name string, _ []byte) (*unstructured.Unstructured, error) {
	f.asked = append(f.asked, fmt.Sprintf("patch %s %s/%s", resource.Resource, namespace, name))
	return nil, errors.New("not served")
}

func writeProvider(t *testing.T, runner *fakeDryRunner, writer *fakeWriter) *Provider {
	t.Helper()
	provider := dryRunProvider(t, runner)
	writer.runner = runner
	provider.options.Writer = writer
	provider.options.DefaultNamespace = "alarmd-ns"
	return provider
}

// An apply answers created, updated or unchanged per object, with the
// fields that changed and the helm release that owns it.
func TestAnApplyAnswersEachObjectWithWhatChanged(t *testing.T) {
	owned := configMap("ns1", "env", map[string]any{"mode": "old", "same": "x"})
	owned.SetAnnotations(map[string]string{"meta.helm.sh/release-name": "bk-monitor", "meta.helm.sh/release-namespace": "blueking"})
	owned.SetResourceVersion("41")
	unchanged := configMap("ns1", "kept", map[string]any{"k": "v"})
	runner := &fakeDryRunner{live: map[string]*unstructured.Unstructured{"ns1/env": owned, "ns1/kept": unchanged}}
	writer := &fakeWriter{applied: func(name string, object map[string]any, _ bool) (*unstructured.Unstructured, error) {
		applied := &unstructured.Unstructured{Object: object}
		if live, ok := runner.live["ns1/"+name]; ok {
			applied.SetAnnotations(live.GetAnnotations())
			applied.SetResourceVersion("42")
		}
		applied.SetUID(types.UID("uid-" + name))
		return applied, nil
	}}
	receipt, err := writeProvider(t, runner, writer).Apply(context.Background(), ApplyRequest{Namespace: "ns1", Manifests: `
apiVersion: v1
kind: ConfigMap
metadata: {name: fresh}
data: {k: v}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: env}
data: {mode: new, same: x}
---
apiVersion: v1
kind: ConfigMap
metadata: {name: kept, namespace: ns1}
data: {k: v}
`})
	if err != nil {
		t.Fatal(err)
	}
	fresh, env, kept := receipt.Objects[0], receipt.Objects[1], receipt.Objects[2]
	if fresh.Outcome != WriteCreated || fresh.UID != "uid-fresh" || len(fresh.Changes) == 0 {
		t.Fatalf("fresh = %+v, want created with its fields", fresh)
	}
	if env.Outcome != WriteUpdated || env.HelmRelease != "blueking/bk-monitor" || len(env.Changes) != 1 ||
		env.Changes[0] != (FieldChange{Path: "data.mode", Before: "old", After: "new"}) {
		t.Fatalf("env = %+v, want updated by data.mode alone, helm-owned", env)
	}
	if kept.Outcome != WriteUnchanged || len(kept.Changes) != 0 {
		t.Fatalf("kept = %+v, want unchanged with no changes", kept)
	}
	if receipt.Counts[WriteCreated] != 1 || receipt.Counts[WriteUpdated] != 1 || receipt.Counts[WriteUnchanged] != 1 {
		t.Fatalf("counts %v", receipt.Counts)
	}
}

// A Secret's change is shown as its keys and fingerprints; no value, encoded
// or decoded, comes back.
func TestAnAppliedSecretShowsFingerprintsNeverValues(t *testing.T) {
	old, updated := "old-secret-value-for-test", "new-secret-value-for-test"
	encode := func(text string) string { return base64.StdEncoding.EncodeToString([]byte(text)) }
	live := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "creds", "namespace": "ns1"}, "data": map[string]any{"password": encode(old)}}}
	runner := &fakeDryRunner{live: map[string]*unstructured.Unstructured{"ns1/creds": live}}
	receipt, err := writeProvider(t, runner, &fakeWriter{}).Apply(context.Background(), ApplyRequest{Manifests: fmt.Sprintf(
		"apiVersion: v1\nkind: Secret\nmetadata: {name: creds, namespace: ns1}\ndata: {password: %s}\n", encode(updated))})
	if err != nil {
		t.Fatal(err)
	}
	object := receipt.Objects[0]
	text := fmt.Sprintf("%+v", receipt)
	for _, value := range []string{old, updated, encode(old), encode(updated)} {
		if strings.Contains(text, value) {
			t.Fatalf("a Secret value came back: %s", text)
		}
	}
	found := false
	for _, change := range object.Changes {
		found = found || (strings.Contains(change.Path, "fingerprint") && change.Before != "" && change.After != "" && change.Before != change.After)
	}
	if object.Outcome != WriteUpdated || !found {
		t.Fatalf("object %+v, want updated with the fingerprint's change", object)
	}
}

// A field ownership conflict is its own outcome, with how to take the
// fields and, the object being helm's, the release that owns it -- known
// only from the live object, since the apply wrote nothing; force goes to
// the apiserver as asked.
func TestAConflictSaysHowToTakeTheFields(t *testing.T) {
	writer := &fakeWriter{applied: func(name string, _ map[string]any, force bool) (*unstructured.Unstructured, error) {
		return nil, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, name, errors.New(`conflict with "helm" using v1: .data.k`))
	}}
	owned := configMap("ns1", "a", map[string]any{"k": "v"})
	owned.SetAnnotations(map[string]string{"meta.helm.sh/release-name": "bk-monitor", "meta.helm.sh/release-namespace": "blueking"})
	provider := writeProvider(t, &fakeDryRunner{live: map[string]*unstructured.Unstructured{"ns1/a": owned}}, writer)
	receipt, err := provider.Apply(context.Background(), ApplyRequest{Manifests: "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, namespace: ns1}\n"})
	if err != nil {
		t.Fatal(err)
	}
	if object := receipt.Objects[0]; object.Outcome != WriteConflict || object.Code != 409 || !strings.Contains(object.Message, "force=true takes ownership") || object.HelmRelease != "blueking/bk-monitor" {
		t.Fatalf("object %+v, want conflict with the hint and the owning release", object)
	}
	if _, err := provider.Apply(context.Background(), ApplyRequest{Force: true, Manifests: "apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, namespace: ns1}\n"}); err != nil {
		t.Fatal(err)
	}
	if len(writer.forces) != 2 || writer.forces[0] || !writer.forces[1] {
		t.Fatalf("forces %v, want false then true", writer.forces)
	}
}

// RBAC objects and kinds outside the grant are refused before any call; a
// denial is the apiserver's words; an apply is bounded.
func TestAnApplyRefusesWhatItMustNotWrite(t *testing.T) {
	writer := &fakeWriter{applied: func(name string, _ map[string]any, _ bool) (*unstructured.Unstructured, error) {
		return nil, &apierrors.StatusError{ErrStatus: metav1.Status{Code: 400, Reason: metav1.StatusReasonBadRequest, Message: "admission webhook denied the request"}}
	}}
	receipt, err := writeProvider(t, &fakeDryRunner{}, writer).Apply(context.Background(), ApplyRequest{Manifests: `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata: {name: admin-for-me}
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata: {name: fast}
---
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata: {name: uq, namespace: ns1}
`})
	if err != nil {
		t.Fatal(err)
	}
	outcomes := []string{receipt.Objects[0].Outcome, receipt.Objects[1].Outcome, receipt.Objects[2].Outcome}
	if strings.Join(outcomes, ",") != "rbac_not_writable,not_granted_kind,denied" || receipt.Objects[2].Message != "admission webhook denied the request" {
		t.Fatalf("objects %+v", receipt.Objects)
	}
	if len(writer.asked) != 1 {
		t.Fatalf("asked %v, want only the Ingress sent", writer.asked)
	}
	spelled, err := writeProvider(t, &fakeDryRunner{}, writer).Apply(context.Background(), ApplyRequest{Manifests: "apiVersion: /v1\nkind: ConfigMap\nmetadata: {name: spelled, namespace: ns1}\n"})
	if err != nil || spelled.Objects[0].Outcome != WriteNotChecked || len(writer.asked) != 1 {
		t.Fatalf("receipt %+v asked %v err %v, want a non-canonical apiVersion not checked and not sent", spelled.Objects, writer.asked, err)
	}
	many := strings.Repeat("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: a, namespace: ns1}\n---\n", MaxApplyObjects+1)
	if _, err := writeProvider(t, &fakeDryRunner{}, &fakeWriter{}).Apply(context.Background(), ApplyRequest{Manifests: many}); errorCode(err) != "apply_too_many_objects" {
		t.Fatalf("err %v, want apply_too_many_objects", err)
	}
}

// A delete goes by the uid a read returned and answers what happened; the
// namespaces the cluster or this process depends on are never deleted.
func TestADeleteGoesByUIDAndSparesTheProtectedNamespaces(t *testing.T) {
	owned := configMap("ns1", "env", nil)
	owned.SetAnnotations(map[string]string{"meta.helm.sh/release-name": "bk-monitor"})
	runner := &fakeDryRunner{live: map[string]*unstructured.Unstructured{"ns1/env": owned}}
	runner.mappings = map[string]schema.GroupVersionResource{
		"v1/ConfigMap": {Version: "v1", Resource: "configmaps"}, "v1/Namespace": {Version: "v1", Resource: "namespaces"},
		"rbac.authorization.k8s.io/v1/Role": {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
	}
	runner.cluster = map[string]bool{"Namespace": true}
	writer := &fakeWriter{deleted: func(name string, _ types.UID) error {
		switch name {
		case "gone":
			return apierrors.NewNotFound(schema.GroupResource{Resource: "configmaps"}, name)
		case "replaced":
			return apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, name, errors.New("Precondition failed: UID in precondition: uid-1, UID in object meta: uid-2"))
		}
		return nil
	}}
	provider := writeProvider(t, runner, writer)
	cases := []struct {
		request ObjectDeleteRequest
		want    string
	}{
		{ObjectDeleteRequest{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns1", Name: "env", UID: "uid-1"}, WriteDeleted},
		{ObjectDeleteRequest{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns1", Name: "gone", UID: "uid-1"}, WriteGone},
		{ObjectDeleteRequest{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns1", Name: "replaced", UID: "uid-1"}, WritePrecondition},
		{ObjectDeleteRequest{APIVersion: "v1", Kind: "Namespace", Name: "kube-system", UID: "uid-1"}, WriteProtected},
		{ObjectDeleteRequest{APIVersion: "v1", Kind: "Namespace", Name: "default", UID: "uid-1"}, WriteProtected},
		{ObjectDeleteRequest{APIVersion: "v1", Kind: "Namespace", Name: "alarmd-ns", UID: "uid-1"}, WriteProtected},
		{ObjectDeleteRequest{APIVersion: "v1", Kind: "Namespace", Name: "bk-monitor-old", UID: "uid-1"}, WriteDeleted},
		// "/v1" parses as core v1: the protection is decided on the resource
		// that would be deleted, not on the request's spelling.
		{ObjectDeleteRequest{APIVersion: "/v1", Kind: "Namespace", Name: "kube-system", UID: "uid-1"}, WriteProtected},
		{ObjectDeleteRequest{APIVersion: "/v1", Kind: "Namespace", Name: "alarmd-ns", UID: "uid-1"}, WriteProtected},
		{ObjectDeleteRequest{APIVersion: "/v1", Kind: "ConfigMap", Namespace: "ns1", Name: "spelled", UID: "uid-1"}, WriteNotChecked},
		{ObjectDeleteRequest{APIVersion: "rbac.authorization.k8s.io/v1", Kind: "Role", Namespace: "ns1", Name: "r", UID: "uid-1"}, WriteRBACNotWritable},
		{ObjectDeleteRequest{APIVersion: "storage.k8s.io/v1", Kind: "StorageClass", Name: "fast", UID: "uid-1"}, WriteNotGrantedKind},
	}
	for _, c := range cases {
		receipt, err := provider.DeleteObject(context.Background(), c.request)
		if err != nil || len(receipt.Objects) != 1 || receipt.Objects[0].Outcome != c.want {
			t.Errorf("%s %s: %+v err %v, want %s", c.request.Kind, c.request.Name, receipt.Objects, err, c.want)
		}
	}
	if receipt, _ := provider.DeleteObject(context.Background(), cases[0].request); receipt.Objects[0].HelmRelease != "bk-monitor" {
		t.Errorf("delete of a helm-owned object = %+v, want its release named", receipt.Objects[0])
	}
	sent := strings.Join(writer.asked, ";")
	for _, protected := range []string{"kube-system", "/default", "alarmd-ns", "roles", "storageclasses", "spelled"} {
		if strings.Contains(sent, protected) {
			t.Fatalf("asked %v: %s was sent", writer.asked, protected)
		}
	}
	if !strings.Contains(sent, "delete configmaps ns1/env uid-1") || !strings.Contains(sent, "delete namespaces /bk-monitor-old uid-1") {
		t.Fatalf("asked %v, want the deletes by uid", writer.asked)
	}
	if _, err := provider.DeleteObject(context.Background(), ObjectDeleteRequest{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns1", Name: "env"}); errorCode(err) != CodeInvalidRequest {
		t.Fatalf("a delete without a uid: %v, want refused", err)
	}
}
