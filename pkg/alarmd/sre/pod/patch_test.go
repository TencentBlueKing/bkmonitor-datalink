// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

// patchingWriter applies a JSON patch to the live object as the apiserver
// does: all of it or, when any op fails, none of it, answering 422.
type patchingWriter struct {
	fakeWriter
	patches []string
	// refuse answers every patch with a bare 422, as validation would.
	refuse bool
}

func (w *patchingWriter) JSONPatch(_ context.Context, resource schema.GroupVersionResource, namespace, name string, patch []byte) (*unstructured.Unstructured, error) {
	w.patches = append(w.patches, string(patch))
	if w.refuse {
		return nil, &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: http.StatusUnprocessableEntity,
			Reason: metav1.StatusReasonInvalid, Message: "the server rejected our request due to an error in our request"}}
	}
	live := w.runner.live[namespace+"/"+name]
	original, _ := json.Marshal(live.Object)
	decoded, err := jsonpatch.DecodePatch(patch)
	if err != nil {
		return nil, apierrors.NewBadRequest(err.Error())
	}
	modified, err := decoded.Apply(original)
	if err != nil {
		// The apiserver answers a failed test with a bare 422: no causes and
		// none of the patch library's words (seen on a live apiserver).
		return nil, &apierrors.StatusError{ErrStatus: metav1.Status{Status: metav1.StatusFailure, Code: http.StatusUnprocessableEntity,
			Reason: metav1.StatusReasonInvalid, Message: "the server rejected our request due to an error in our request"}}
	}
	out := &unstructured.Unstructured{}
	if err := out.UnmarshalJSON(modified); err != nil {
		return nil, err
	}
	w.runner.live[namespace+"/"+name] = out
	return out.DeepCopy(), nil
}

func patchedDeployment() *unstructured.Unstructured {
	object := deploymentWithEnv()
	object.SetUID(types.UID("uid-web"))
	return object
}

func deploymentWithEnv() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web", "namespace": "ns1", "annotations": map[string]any{"example.test/hotfix-id": "h1"}},
		"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "web", "image": "web:1",
			"env": []any{map[string]any{"name": "KEEP", "value": "k"}, map[string]any{"name": "DROP", "value": "d"}}}}}}}}}
}

func patchProvider(t *testing.T) (*Provider, *fakeDryRunner, *patchingWriter) {
	t.Helper()
	runner := &fakeDryRunner{live: map[string]*unstructured.Unstructured{"ns1/web": patchedDeployment()}, mappings: map[string]schema.GroupVersionResource{
		"apps/v1/Deployment":                {Group: "apps", Version: "v1", Resource: "deployments"},
		"rbac.authorization.k8s.io/v1/Role": {Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"},
	}}
	writer := &patchingWriter{}
	provider := writeProvider(t, runner, &writer.fakeWriter)
	writer.runner = runner
	provider.options.Writer = writer
	return provider, runner, writer
}

func patchRequest(uid string, ops ...PatchOp) ObjectPatchRequest {
	return ObjectPatchRequest{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "ns1", Name: "web", UID: uid, Patch: ops}
}

func raw(value string) json.RawMessage { return json.RawMessage(value) }

// A patch goes to the object the uid names, with a test of that uid first:
// the apiserver applies all of it or none. A removal guarded by a test of the
// entry's whole value changes exactly that entry, and the receipt lists it.
func TestAPatchIsGuardedByTheUIDAndChangesWhatItSays(t *testing.T) {
	provider, runner, writer := patchProvider(t)
	receipt, err := provider.PatchObject(context.Background(), patchRequest("uid-web",
		PatchOp{Op: "test", Path: "/spec/template/spec/containers/0/env/1", Value: raw(`{"name":"DROP","value":"d"}`)},
		PatchOp{Op: "remove", Path: "/spec/template/spec/containers/0/env/1"},
		PatchOp{Op: "test", Path: "/metadata/annotations/example.test~1hotfix-id", Value: raw(`"h1"`)},
		PatchOp{Op: "remove", Path: "/metadata/annotations/example.test~1hotfix-id"}))
	if err != nil || len(receipt.Objects) != 1 || receipt.Objects[0].Outcome != WriteUpdated {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	if len(writer.patches) != 1 || !strings.HasPrefix(writer.patches[0], `[{"op":"test","path":"/metadata/uid","value":"uid-web"}`) {
		t.Fatalf("patch sent %v, want the uid test first", writer.patches)
	}
	env := runner.live["ns1/web"].Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)["env"].([]any)
	if len(env) != 1 || env[0].(map[string]any)["name"] != "KEEP" || len(runner.live["ns1/web"].GetAnnotations()) != 0 {
		t.Fatalf("object after %+v", runner.live["ns1/web"].Object)
	}
	if len(receipt.Objects[0].Changes) == 0 || !receipt.Objects[0].WillRoll {
		t.Fatalf("changes or will_roll missing: %+v", receipt.Objects[0])
	}
	// A change outside the Pod template does not roll.
	receipt, err = provider.PatchObject(context.Background(), patchRequest("uid-web",
		PatchOp{Op: "add", Path: "/metadata/labels", Value: raw(`{"team":"sre"}`)}))
	if err != nil || receipt.Objects[0].Outcome != WriteUpdated || receipt.Objects[0].WillRoll {
		t.Fatalf("metadata-only patch %+v err %v", receipt.Objects[0], err)
	}
}

// A uid other than the live object's sends nothing; a failed test changes
// nothing and says so.
func TestAPatchOnAnotherObjectOrAFailedTestChangesNothing(t *testing.T) {
	provider, runner, writer := patchProvider(t)
	receipt, err := provider.PatchObject(context.Background(), patchRequest("uid-old", PatchOp{Op: "remove", Path: "/metadata/annotations"}))
	if err != nil || receipt.Objects[0].Outcome != WritePrecondition || len(writer.patches) != 0 {
		t.Fatalf("receipt %+v err %v patches %v", receipt, err, writer.patches)
	}
	before, _ := json.Marshal(runner.live["ns1/web"].Object)
	receipt, err = provider.PatchObject(context.Background(), patchRequest("uid-web",
		PatchOp{Op: "test", Path: "/spec/template/spec/containers/0/env/1", Value: raw(`{"name":"DROP","value":"changed"}`)},
		PatchOp{Op: "remove", Path: "/spec/template/spec/containers/0/env/1"}))
	after, _ := json.Marshal(runner.live["ns1/web"].Object)
	if err != nil || receipt.Objects[0].Outcome != WriteTestFailed || string(before) != string(after) {
		t.Fatalf("receipt %+v err %v changed %v", receipt, err, string(before) != string(after))
	}
}

// Only test, remove, replace and add; a value where the op needs one; a
// path; and kinds the channel does not write are refused, before any call.
func TestAPatchOutsideTheContractIsRefusedBeforeAnyCall(t *testing.T) {
	provider, _, writer := patchProvider(t)
	for _, ops := range [][]PatchOp{
		nil,
		{{Op: "move", Path: "/a", Value: raw(`"/b"`)}},
		{{Op: "copy", Path: "/a", Value: raw(`"/b"`)}},
		{{Op: "test", Path: "/metadata/name"}},
		{{Op: "remove", Path: "metadata/name"}},
	} {
		if _, err := provider.PatchObject(context.Background(), patchRequest("uid-web", ops...)); err == nil {
			t.Fatalf("ops %+v accepted", ops)
		}
	}
	if _, err := provider.PatchObject(context.Background(), ObjectPatchRequest{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "ns1", Name: "web",
		Patch: []PatchOp{{Op: "remove", Path: "/metadata/annotations"}}}); err == nil {
		t.Fatal("a patch without a uid accepted")
	}
	rbac := patchRequest("uid-1", PatchOp{Op: "remove", Path: "/rules"})
	rbac.APIVersion, rbac.Kind = "rbac.authorization.k8s.io/v1", "Role"
	receipt, err := provider.PatchObject(context.Background(), rbac)
	if err != nil || len(receipt.Objects) != 1 || receipt.Objects[0].Outcome == WriteUpdated {
		t.Fatalf("RBAC patch receipt %+v err %v", receipt, err)
	}
	if len(writer.patches) != 0 {
		t.Fatalf("patches sent %v", writer.patches)
	}
}

// Through the production client a patch is a JSON patch under the
// channel's own field manager, never a dry run.
func TestThePatchIsAJSONPatchAsTheChannelsManager(t *testing.T) {
	var seen *http.Request
	var body string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		seen, body = r.Clone(r.Context()), string(data)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"apiVersion":"apps/v1","kind":"Deployment","metadata":{"name":"web","namespace":"ns1"}}`))
	}))
	defer server.Close()
	client, err := NewKubernetesClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	patch := `[{"op":"remove","path":"/metadata/annotations"}]`
	if _, err := client.JSONPatch(context.Background(), schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, "ns1", "web", []byte(patch)); err != nil {
		t.Fatal(err)
	}
	query := seen.URL.Query()
	if seen.Method != http.MethodPatch || seen.Header.Get("Content-Type") != "application/json-patch+json" || seen.URL.Path != "/apis/apps/v1/namespaces/ns1/deployments/web" ||
		query.Get("fieldManager") != "alarmd-sre" || query.Has("dryRun") || body != patch {
		t.Fatalf("%s %s %s %v body %s", seen.Method, seen.URL.Path, seen.Header.Get("Content-Type"), query, body)
	}
}

// A bare 422 is a failed test only when one of the patch's own tests does
// not hold on the object as read; a 422 for a patch whose tests all hold is
// the apiserver refusing the result, a denial.
func TestABare422IsATestFailureOnlyWhenATestDoesNotHold(t *testing.T) {
	provider, _, writer := patchProvider(t)
	writer.refuse = true
	receipt, err := provider.PatchObject(context.Background(), patchRequest("uid-web",
		PatchOp{Op: "test", Path: "/spec/template/spec/containers/0/env/1", Value: raw(`{"name":"DROP","value":"d"}`)},
		PatchOp{Op: "remove", Path: "/spec/template/spec/containers/0/env/1"}))
	if err != nil || receipt.Objects[0].Outcome != WriteDenied {
		t.Fatalf("tests hold, refused anyway: %+v err %v", receipt.Objects[0], err)
	}
}

// A test of a Secret's value with the right value holds on the raw object:
// a bare 422 for that patch is not relabeled a failed test because receipts
// show the value masked.
func TestASecretTestIsReplayedOnTheRawValue(t *testing.T) {
	secret := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "s", "namespace": "ns1", "uid": "uid-s", "resourceVersion": "7"}, "data": map[string]any{"KEY": "c2VjcmV0LXNlbnRpbmVs"}}}
	runner := &fakeDryRunner{live: map[string]*unstructured.Unstructured{"ns1/s": secret}, mappings: map[string]schema.GroupVersionResource{
		"v1/Secret": {Version: "v1", Resource: "secrets"}}}
	writer := &patchingWriter{refuse: true}
	provider := writeProvider(t, runner, &writer.fakeWriter)
	writer.runner = runner
	provider.options.Writer = writer
	request := ObjectPatchRequest{APIVersion: "v1", Kind: "Secret", Namespace: "ns1", Name: "s", UID: "uid-s", Patch: []PatchOp{
		{Op: "test", Path: "/data/KEY", Value: raw(`"c2VjcmV0LXNlbnRpbmVs"`)}, {Op: "remove", Path: "/data/KEY"}}}
	receipt, err := provider.PatchObject(context.Background(), request)
	if err != nil || receipt.Objects[0].Outcome != WriteDenied {
		t.Fatalf("a holding test on a Secret value: %+v err %v", receipt.Objects[0], err)
	}
	request.Patch[0].Value = raw(`"d3Jvbmc="`)
	receipt, err = provider.PatchObject(context.Background(), request)
	o := receipt.Objects[0]
	encoded, _ := json.Marshal(o)
	if err != nil || o.Outcome != WriteTestFailed || !strings.Contains(o.Message, "/data/KEY") || !strings.Contains(o.Message, "resourceVersion 7") ||
		strings.Contains(string(encoded), "c2VjcmV0LXNlbnRpbmVs") || strings.Contains(string(encoded), "d3Jvbmc=") {
		t.Fatalf("a failing test on a Secret value: %s err %v", encoded, err)
	}
}

// Ops apply in order: a test after a remove tests the state the remove left.
// Here env/1 holds DROP only after env/0 is removed; with the apiserver
// refusing for another reason, the in-order replay finds every op applies and
// the outcome stays a denial, where replaying the test alone against the
// object as read would have called it a failed test.
func TestTheReplayAppliesOpsInOrder(t *testing.T) {
	provider, _, writer := patchProvider(t)
	writer.refuse = true
	receipt, err := provider.PatchObject(context.Background(), patchRequest("uid-web",
		PatchOp{Op: "remove", Path: "/spec/template/spec/containers/0/env/0"},
		PatchOp{Op: "test", Path: "/spec/template/spec/containers/0/env/0", Value: raw(`{"name":"DROP","value":"d"}`)}))
	if err != nil || receipt.Objects[0].Outcome != WriteDenied || strings.Contains(receipt.Objects[0].Message, "does not hold") {
		t.Fatalf("in-order test after a remove: %+v err %v", receipt.Objects[0], err)
	}
	// A non-test op that does not apply is named, and stays a denial.
	receipt, err = provider.PatchObject(context.Background(), patchRequest("uid-web",
		PatchOp{Op: "remove", Path: "/spec/template/spec/containers/0/env/5"}))
	if err != nil || receipt.Objects[0].Outcome != WriteDenied || !strings.Contains(receipt.Objects[0].Message, "op 0 remove /spec/template/spec/containers/0/env/5 does not apply") {
		t.Fatalf("missing path: %+v err %v", receipt.Objects[0], err)
	}
}
