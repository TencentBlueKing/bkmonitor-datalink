// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// longText is a config file longer than a receipt shows of one value, so a
// line changed near its end is past what the receipt prints.
func longText(tail string) string {
	return strings.Repeat("line: unchanged\n", 40) + "tail: " + tail + "\n"
}

func configMapPatchProvider(t *testing.T, object *unstructured.Unstructured) (*Provider, *fakeDryRunner) {
	t.Helper()
	runner := &fakeDryRunner{live: map[string]*unstructured.Unstructured{"ns1/" + object.GetName(): object}, mappings: map[string]schema.GroupVersionResource{
		"v1/ConfigMap":       {Version: "v1", Resource: "configmaps"},
		"v1/Secret":          {Version: "v1", Resource: "secrets"},
		"apps/v1/Deployment": {Group: "apps", Version: "v1", Resource: "deployments"},
	}}
	writer := &patchingWriter{}
	provider := writeProvider(t, runner, &writer.fakeWriter)
	writer.runner = runner
	provider.options.Writer = writer
	return provider, runner
}

func jsonString(t *testing.T, value string) json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// A change is judged on the object as stored, not as the receipt prints it:
// a ConfigMap value changed past the printed length, in data or binaryData,
// is an update, listed on its path with the two values' fingerprints since
// the printed text cannot tell them apart.
func TestAChangePastWhatTheReceiptPrintsIsStillAnUpdate(t *testing.T) {
	for _, field := range []string{"data", "binaryData"} {
		t.Run(field, func(t *testing.T) {
			object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
				"metadata": map[string]any{"name": "conf", "namespace": "ns1", "uid": "uid-conf"},
				field:      map[string]any{"app.yaml": longText("old")}}}
			provider, runner := configMapPatchProvider(t, object)
			path := "/" + field + "/app.yaml"
			receipt, err := provider.PatchObject(context.Background(), ObjectPatchRequest{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns1", Name: "conf", UID: "uid-conf",
				Patch: []PatchOp{{Op: "test", Path: path, Value: jsonString(t, longText("old"))}, {Op: "replace", Path: path, Value: jsonString(t, longText("new"))}}})
			if err != nil || len(receipt.Objects) != 1 {
				t.Fatalf("receipt %+v err %v", receipt, err)
			}
			if got, _ := runner.live["ns1/conf"].Object[field].(map[string]any)["app.yaml"].(string); got != longText("new") {
				t.Fatalf("the object was not changed: %q", got)
			}
			o := receipt.Objects[0]
			if o.Outcome != WriteUpdated || len(o.Changes) != 1 || o.Changes[0].Path != field+".app.yaml" {
				t.Fatalf("a changed %s value: %+v", field, o)
			}
			c := o.Changes[0]
			if c.BeforeFingerprint == "" || c.AfterFingerprint == "" || c.BeforeFingerprint == c.AfterFingerprint {
				t.Fatalf("the change cannot be told apart in the receipt: %+v", c)
			}
		})
	}
}

// A change inside a masked span is an update too, and rolls when it is in the
// Pod template; the receipt carries fingerprints, never either value.
func TestAChangeTheReceiptMasksIsStillAnUpdateAndRolls(t *testing.T) {
	deployment := func(token string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment",
			"metadata": map[string]any{"name": "web", "namespace": "ns1", "uid": "uid-web"},
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{map[string]any{"name": "web", "image": "web:1",
				"args": []any{"--header=Authorization: Bearer " + token}}}}}}}}
	}
	provider, _ := configMapPatchProvider(t, deployment("tok-sentinel-aaa"))
	path := "/spec/template/spec/containers/0/args/0"
	receipt, err := provider.PatchObject(context.Background(), ObjectPatchRequest{APIVersion: "apps/v1", Kind: "Deployment", Namespace: "ns1", Name: "web", UID: "uid-web",
		Patch: []PatchOp{{Op: "replace", Path: path, Value: jsonString(t, "--header=Authorization: Bearer tok-sentinel-bbb")}}})
	if err != nil || len(receipt.Objects) != 1 {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	o := receipt.Objects[0]
	encoded, _ := json.Marshal(o)
	if o.Outcome != WriteUpdated || !o.WillRoll || len(o.Changes) != 1 {
		t.Fatalf("a masked change in the Pod template: %s", encoded)
	}
	if strings.Contains(string(encoded), "tok-sentinel") {
		t.Fatalf("the receipt carries a masked value: %s", encoded)
	}
	if c := o.Changes[0]; c.BeforeFingerprint == "" || c.BeforeFingerprint == c.AfterFingerprint {
		t.Fatalf("the masked change cannot be told apart: %+v", c)
	}
}

// A change the receipt prints in full needs no fingerprints, and a patch that
// leaves the object as it was is still unchanged.
func TestAVisibleChangeHasNoFingerprintsAndANoOpIsUnchanged(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "conf", "namespace": "ns1", "uid": "uid-conf"}, "data": map[string]any{"mode": "old"}}}
	provider, _ := configMapPatchProvider(t, object)
	request := ObjectPatchRequest{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns1", Name: "conf", UID: "uid-conf",
		Patch: []PatchOp{{Op: "replace", Path: "/data/mode", Value: raw(`"new"`)}}}
	receipt, err := provider.PatchObject(context.Background(), request)
	if err != nil || receipt.Objects[0].Outcome != WriteUpdated || len(receipt.Objects[0].Changes) != 1 {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	if c := receipt.Objects[0].Changes[0]; c.Before != "old" || c.After != "new" || c.BeforeFingerprint != "" || c.AfterFingerprint != "" {
		t.Fatalf("a visible change: %+v", c)
	}
	receipt, err = provider.PatchObject(context.Background(), request)
	if err != nil || receipt.Objects[0].Outcome != WriteUnchanged || len(receipt.Objects[0].Changes) != 0 {
		t.Fatalf("a patch to the value already there: %+v err %v", receipt.Objects[0], err)
	}
}

// An apply is judged the same way: a value changed past the printed length is
// an update, not unchanged.
func TestAnApplyChangingALongValueIsAnUpdate(t *testing.T) {
	live := configMap("ns1", "conf", map[string]any{"app.yaml": longText("old")})
	runner := &fakeDryRunner{live: map[string]*unstructured.Unstructured{"ns1/conf": live}}
	provider := writeProvider(t, runner, &fakeWriter{})
	receipt, err := provider.Apply(context.Background(), ApplyRequest{Manifests: mustJSON(t, configMap("ns1", "conf", map[string]any{"app.yaml": longText("new")}).Object)})
	if err != nil || len(receipt.Objects) != 1 {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	if o := receipt.Objects[0]; o.Outcome != WriteUpdated || len(o.Changes) != 1 || o.Changes[0].AfterFingerprint == "" {
		t.Fatalf("an apply changing a long value: %+v", o)
	}
}

// A Secret value changed is an update, told apart by the per-key fingerprint,
// with neither value in the receipt.
func TestASecretValueChangeIsAnUpdateWithoutTheValue(t *testing.T) {
	secret := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
		"metadata": map[string]any{"name": "s", "namespace": "ns1", "uid": "uid-s"}, "data": map[string]any{"KEY": "b2xk"}}}
	provider, _ := configMapPatchProvider(t, secret)
	receipt, err := provider.PatchObject(context.Background(), ObjectPatchRequest{APIVersion: "v1", Kind: "Secret", Namespace: "ns1", Name: "s", UID: "uid-s",
		Patch: []PatchOp{{Op: "test", Path: "/data/KEY", Value: raw(`"b2xk"`)}, {Op: "replace", Path: "/data/KEY", Value: raw(`"bmV3"`)}}})
	if err != nil || len(receipt.Objects) != 1 {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	o := receipt.Objects[0]
	encoded, _ := json.Marshal(o)
	if o.Outcome != WriteUpdated || len(o.Changes) != 1 {
		t.Fatalf("a Secret value change: %s", encoded)
	}
	// A Secret is shown by keys and per-key fingerprints, so the changed value
	// surfaces as that key's fingerprint differing -- a visible change, no raw
	// value and no extra FieldChange fingerprint.
	if c := o.Changes[0]; c.Before == "" || c.Before == c.After {
		t.Fatalf("the change is not told apart: %+v", c)
	}
	if strings.Contains(string(encoded), "b2xk") || strings.Contains(string(encoded), "bmV3") {
		t.Fatalf("the receipt carries a Secret value: %s", encoded)
	}
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
