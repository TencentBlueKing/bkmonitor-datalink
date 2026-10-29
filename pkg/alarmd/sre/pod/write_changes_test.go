// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"reflect"
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

func changePaths(changes []FieldChange) string {
	paths := make([]string, 0, len(changes))
	for _, change := range changes {
		paths = append(paths, change.Path)
	}
	return strings.Join(paths, " ")
}

// A key patched in or out with an empty value is a change: a missing key
// reads as "", so presence is compared apart from the value. Both values
// print as "", so the change carries the fingerprint of the side that holds
// the key and none for the side without it. The receipt names what the dry
// run's comparison names for the same two objects.
func TestAKeyPatchedInOrOutWithAnEmptyValueIsAChange(t *testing.T) {
	cases := []struct {
		name         string
		data         map[string]any
		labels       map[string]any
		op           PatchOp
		path         string
		before       string
		after        string
		fingerprints string
	}{
		{"data add empty", map[string]any{"a": "1"}, nil, PatchOp{Op: "add", Path: "/data/e", Value: raw(`""`)}, "data.e", "", "", "after"},
		{"data remove empty", map[string]any{"a": "1", "e": ""}, nil, PatchOp{Op: "remove", Path: "/data/e"}, "data.e", "", "", "before"},
		{"label add empty", map[string]any{"a": "1"}, map[string]any{"app": "web"}, PatchOp{Op: "add", Path: "/metadata/labels/flag", Value: raw(`""`)}, "metadata.labels.flag", "", "", "after"},
		{"label remove empty", map[string]any{"a": "1"}, map[string]any{"app": "web", "flag": ""}, PatchOp{Op: "remove", Path: "/metadata/labels/flag"}, "metadata.labels.flag", "", "", "before"},
		{"data add value", map[string]any{"a": "1"}, nil, PatchOp{Op: "add", Path: "/data/e", Value: raw(`"x"`)}, "data.e", "", "x", ""},
		{"data remove value", map[string]any{"a": "1", "e": "x"}, nil, PatchOp{Op: "remove", Path: "/data/e"}, "data.e", "x", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
				"metadata": map[string]any{"name": "conf", "namespace": "ns1", "uid": "uid-conf"}, "data": c.data}}
			if c.labels != nil {
				object.Object["metadata"].(map[string]any)["labels"] = c.labels
			}
			before := object.DeepCopy()
			provider, runner := configMapPatchProvider(t, object)
			receipt, err := provider.PatchObject(context.Background(), ObjectPatchRequest{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns1", Name: "conf", UID: "uid-conf",
				Patch: []PatchOp{c.op}})
			if err != nil || len(receipt.Objects) != 1 {
				t.Fatalf("receipt %+v err %v", receipt, err)
			}
			after := runner.live["ns1/conf"]
			if reflect.DeepEqual(before.Object, after.Object) {
				t.Fatalf("the patch did not change the object")
			}
			o := receipt.Objects[0]
			if o.Outcome != WriteUpdated || changePaths(o.Changes) != c.path {
				t.Fatalf("outcome %s changes %+v, want updated on %s", o.Outcome, o.Changes, c.path)
			}
			if dryRun := strings.Join(changedPaths(cleanForCompare(before), cleanForCompare(after), ""), " "); dryRun != c.path {
				t.Fatalf("the dry run's comparison names %q, the receipt %q", dryRun, c.path)
			}
			change := o.Changes[0]
			if change.Before != c.before || change.After != c.after {
				t.Fatalf("change %+v, want %q to %q", change, c.before, c.after)
			}
			if got := map[bool]string{true: "before"}[change.BeforeFingerprint != ""] + map[bool]string{true: "after"}[change.AfterFingerprint != ""]; got != c.fingerprints {
				t.Fatalf("change %+v carries fingerprints %q, want %q", change, got, c.fingerprints)
			}
			if receipt.Counts[WriteUnchanged] != 0 || receipt.Counts[WriteUpdated] != 1 {
				t.Fatalf("counts %v", receipt.Counts)
			}
		})
	}

	// A patch that leaves an empty value as it was is still unchanged.
	object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "conf", "namespace": "ns1", "uid": "uid-conf"}, "data": map[string]any{"a": "1", "e": ""}}}
	provider, _ := configMapPatchProvider(t, object)
	receipt, err := provider.PatchObject(context.Background(), ObjectPatchRequest{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns1", Name: "conf", UID: "uid-conf",
		Patch: []PatchOp{{Op: "replace", Path: "/data/e", Value: raw(`""`)}}})
	if err != nil || receipt.Objects[0].Outcome != WriteUnchanged || len(receipt.Objects[0].Changes) != 0 {
		t.Fatalf("a patch to the empty value already there: %+v err %v", receipt.Objects, err)
	}
}

// emptyValueRunner serves the kinds the empty-value applies write, with the
// objects as live. Neither the live objects nor the manifests carry a uid,
// so the dry run, which compares uids, sees only what the manifest changes.
func emptyValueRunner(live ...*unstructured.Unstructured) *fakeDryRunner {
	runner := &fakeDryRunner{live: map[string]*unstructured.Unstructured{}, mappings: map[string]schema.GroupVersionResource{
		"v1/ConfigMap":                            {Version: "v1", Resource: "configmaps"},
		"policy/v1/PodDisruptionBudget":           {Group: "policy", Version: "v1", Resource: "poddisruptionbudgets"},
		"networking.k8s.io/v1/NetworkPolicy":      {Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"},
		"monitoring.coreos.com/v1/ServiceMonitor": {Group: "monitoring.coreos.com", Version: "v1", Resource: "servicemonitors"},
	}}
	for _, object := range live {
		runner.live[object.GetNamespace()+"/"+object.GetName()] = object
	}
	return runner
}

// applyAndDryRun applies one manifest over live and dry-runs the same
// manifest over the same live object: the receipt's object and the dry
// run's.
func applyAndDryRun(t *testing.T, live, manifest *unstructured.Unstructured) (WriteObject, DryRunObject) {
	t.Helper()
	manifests := mustJSON(t, manifest.Object)
	dryRun, err := dryRunProvider(t, emptyValueRunner(live.DeepCopy())).DryRun(context.Background(), DryRunRequest{Manifests: manifests})
	if err != nil || len(dryRun.Objects) != 1 {
		t.Fatalf("dry run %+v err %v", dryRun, err)
	}
	receipt, err := writeProvider(t, emptyValueRunner(live.DeepCopy()), &fakeWriter{}).Apply(context.Background(), ApplyRequest{Manifests: manifests})
	if err != nil || len(receipt.Objects) != 1 {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	return receipt.Objects[0], dryRun.Objects[0]
}

// An apply that adds or removes a key with an empty value is an update, and
// the receipt names the fields the dry run names for the same manifest; an
// apply of the object as it is, empty value and all, is unchanged.
func TestAnApplyAddingOrRemovingAnEmptyValueIsAnUpdate(t *testing.T) {
	withLabels := func(object *unstructured.Unstructured, labels map[string]string) *unstructured.Unstructured {
		object.SetLabels(labels)
		return object
	}
	cases := []struct {
		name           string
		live, manifest *unstructured.Unstructured
		path           string
	}{
		{"data add empty", configMap("ns1", "conf", map[string]any{"a": "1"}), configMap("ns1", "conf", map[string]any{"a": "1", "e": ""}), "data.e"},
		{"data remove empty", configMap("ns1", "conf", map[string]any{"a": "1", "e": ""}), configMap("ns1", "conf", map[string]any{"a": "1"}), "data.e"},
		{"label add empty", withLabels(configMap("ns1", "conf", map[string]any{"a": "1"}), map[string]string{"app": "web"}),
			withLabels(configMap("ns1", "conf", map[string]any{"a": "1"}), map[string]string{"app": "web", "flag": ""}), "metadata.labels.flag"},
		{"label remove empty", withLabels(configMap("ns1", "conf", map[string]any{"a": "1"}), map[string]string{"app": "web", "flag": ""}),
			withLabels(configMap("ns1", "conf", map[string]any{"a": "1"}), map[string]string{"app": "web"}), "metadata.labels.flag"},
		{"data add value", configMap("ns1", "conf", map[string]any{"a": "1"}), configMap("ns1", "conf", map[string]any{"a": "1", "e": "x"}), "data.e"},
		{"data remove value", configMap("ns1", "conf", map[string]any{"a": "1", "e": "x"}), configMap("ns1", "conf", map[string]any{"a": "1"}), "data.e"},
		{"the same, empty value and all", configMap("ns1", "conf", map[string]any{"a": "1", "e": ""}), configMap("ns1", "conf", map[string]any{"a": "1", "e": ""}), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			written, dryRun := applyAndDryRun(t, c.live, c.manifest)
			outcome, verdict := WriteUpdated, DryRunWouldUpdate
			if c.path == "" {
				outcome, verdict = WriteUnchanged, DryRunUnchanged
			}
			if written.Outcome != outcome || changePaths(written.Changes) != c.path {
				t.Fatalf("outcome %s changes %+v, want %s on %q", written.Outcome, written.Changes, outcome, c.path)
			}
			if dryRun.Verdict != verdict || strings.Join(dryRun.ChangedPaths, " ") != c.path {
				t.Fatalf("the dry run says %s on %v, the receipt %s on %q", dryRun.Verdict, dryRun.ChangedPaths, written.Outcome, c.path)
			}
		})
	}
}

// A field set to an empty map or list where it was absent, or taken away
// when it was one, is a change: a PodDisruptionBudget's selector: {}
// selects every Pod and none without it, a NetworkPolicy's ingress: [{}]
// admits all traffic and none without it, and a custom resource keeps an
// empty list as written. An empty map on both sides is not, and a map or
// list filled or emptied is listed by the leaves it gains or loses, not as
// the container.
func TestAFieldSetToAnEmptyMapOrListIsAChange(t *testing.T) {
	budget := func(selector any) *unstructured.Unstructured {
		spec := map[string]any{"minAvailable": int64(1)}
		if selector != nil {
			spec["selector"] = selector
		}
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "policy/v1", "kind": "PodDisruptionBudget",
			"metadata": map[string]any{"name": "web", "namespace": "ns1"}, "spec": spec}}
	}
	matchWeb := map[string]any{"matchLabels": map[string]any{"app": "web"}}
	monitor := func(targetLabels any) *unstructured.Unstructured {
		spec := map[string]any{"endpoints": []any{map[string]any{"port": "web"}}}
		if targetLabels != nil {
			spec["targetLabels"] = targetLabels
		}
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "monitoring.coreos.com/v1", "kind": "ServiceMonitor",
			"metadata": map[string]any{"name": "web", "namespace": "ns1"}, "spec": spec}}
	}
	cases := []struct {
		name           string
		live, manifest *unstructured.Unstructured
		paths, dryRun  string
		before, after  string
	}{
		{"selector set to {}", budget(nil), budget(map[string]any{}), "spec.selector", "spec.selector", "", "{}"},
		{"selector {} taken away", budget(map[string]any{}), budget(nil), "spec.selector", "spec.selector", "{}", ""},
		{"selector {} on both sides", budget(map[string]any{}), budget(map[string]any{}), "", "", "", ""},
		{"selector {} filled", budget(map[string]any{}), budget(matchWeb), "spec.selector.matchLabels.app", "spec.selector.matchLabels", "", "web"},
		{"selector emptied to {}", budget(matchWeb), budget(map[string]any{}), "spec.selector.matchLabels.app", "spec.selector.matchLabels", "web", ""},
		{"list set to []", monitor(nil), monitor([]any{}), "spec.targetLabels", "spec.targetLabels", "", "[]"},
		{"list [] taken away", monitor([]any{}), monitor(nil), "spec.targetLabels", "spec.targetLabels", "[]", ""},
		{"list [] filled", monitor([]any{}), monitor([]any{"team"}), "spec.targetLabels[0]", "spec.targetLabels", "", "team"},
		{"list emptied to []", monitor([]any{"team"}), monitor([]any{}), "spec.targetLabels[0]", "spec.targetLabels", "team", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			written, dryRun := applyAndDryRun(t, c.live, c.manifest)
			outcome := WriteUpdated
			if c.paths == "" {
				outcome = WriteUnchanged
			}
			if written.Outcome != outcome || changePaths(written.Changes) != c.paths {
				t.Fatalf("outcome %s changes %+v, want %s on %q", written.Outcome, written.Changes, outcome, c.paths)
			}
			if c.paths != "" && (written.Changes[0].Before != c.before || written.Changes[0].After != c.after) {
				t.Fatalf("change %+v, want %q to %q", written.Changes[0], c.before, c.after)
			}
			// The dry run names a map that appears or goes, or a list that
			// changes length, as the container; the receipt names the
			// leaves, each with its values.
			if strings.Join(dryRun.ChangedPaths, " ") != c.dryRun {
				t.Fatalf("the dry run names %v, want %q", dryRun.ChangedPaths, c.dryRun)
			}
		})
	}

	// The patch path: ingress: [{}] added to a NetworkPolicy that had none.
	policy := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": map[string]any{"name": "web", "namespace": "ns1", "uid": "uid-web"},
		"spec":     map[string]any{"podSelector": map[string]any{}, "policyTypes": []any{"Ingress"}}}}
	runner := emptyValueRunner(policy)
	writer := &patchingWriter{}
	provider := writeProvider(t, runner, &writer.fakeWriter)
	writer.runner = runner
	provider.options.Writer = writer
	receipt, err := provider.PatchObject(context.Background(), ObjectPatchRequest{APIVersion: "networking.k8s.io/v1", Kind: "NetworkPolicy", Namespace: "ns1", Name: "web", UID: "uid-web",
		Patch: []PatchOp{{Op: "add", Path: "/spec/ingress", Value: raw(`[{}]`)}}})
	if err != nil || len(receipt.Objects) != 1 {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	if o := receipt.Objects[0]; o.Outcome != WriteUpdated || changePaths(o.Changes) != "spec.ingress[0]" || o.Changes[0].After != "{}" {
		t.Fatalf("ingress: [{}] added: %+v", o)
	}
}

// A value whose JSON type changes is a change though it prints the same:
// the string "{}" and an empty map, "[]" and an empty list, "1" and the
// number 1, "true" and true. The string side is printed quoted, so the two
// read apart, and the receipt names what the dry run names.
func TestAValueWhoseTypeChangesIsAChange(t *testing.T) {
	monitor := func(probe any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "monitoring.coreos.com/v1", "kind": "ServiceMonitor",
			"metadata": map[string]any{"name": "web", "namespace": "ns1"},
			"spec":     map[string]any{"endpoints": []any{map[string]any{"port": "web"}}, "probe": probe}}}
	}
	cases := []struct {
		name          string
		live, applied any
		before, after string
	}{
		{"string {} to an empty map", "{}", map[string]any{}, `"{}"`, "{}"},
		{"an empty map to string {}", map[string]any{}, "{}", "{}", `"{}"`},
		{"string [] to an empty list", "[]", []any{}, `"[]"`, "[]"},
		{"string 1 to the number 1", "1", int64(1), `"1"`, "1"},
		{"the number 1 to string 1", int64(1), "1", "1", `"1"`},
		{"string true to true", "true", true, `"true"`, "true"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			written, dryRun := applyAndDryRun(t, monitor(c.live), monitor(c.applied))
			if written.Outcome != WriteUpdated || changePaths(written.Changes) != "spec.probe" {
				t.Fatalf("outcome %s changes %+v, want updated on spec.probe", written.Outcome, written.Changes)
			}
			if change := written.Changes[0]; change.Before != c.before || change.After != c.after {
				t.Fatalf("change %+v, want %s to %s", change, c.before, c.after)
			}
			if dryRun.Verdict != DryRunWouldUpdate || strings.Join(dryRun.ChangedPaths, " ") != "spec.probe" {
				t.Fatalf("the dry run says %s on %v", dryRun.Verdict, dryRun.ChangedPaths)
			}
		})
	}

	// The same string on both sides is unchanged, quoted nowhere.
	written, _ := applyAndDryRun(t, monitor("1"), monitor("1"))
	if written.Outcome != WriteUnchanged || len(written.Changes) != 0 {
		t.Fatalf("the same string: %+v", written)
	}
	// A string the other side holds as a filled map: the string is quoted,
	// the map listed by its leaves.
	written, _ = applyAndDryRun(t, monitor("{}"), monitor(map[string]any{"path": "/healthz"}))
	if written.Outcome != WriteUpdated || changePaths(written.Changes) != "spec.probe spec.probe.path" ||
		written.Changes[0].Before != `"{}"` || written.Changes[0].After != "" || written.Changes[1].After != "/healthz" {
		t.Fatalf("string {} to a filled map: %+v", written.Changes)
	}
}

// The last-applied annotation is neither shown nor compared, and an
// annotations map it alone held is the same metadata as none: a patch that
// removes that map changes nothing the receipt judges.
func TestRemovingAnnotationsOnlyTheLastAppliedOneHeldIsUnchanged(t *testing.T) {
	object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "conf", "namespace": "ns1", "uid": "uid-conf",
			"annotations": map[string]any{lastAppliedAnnotation: `{"data":{"a":"1"}}`}},
		"data": map[string]any{"a": "1"}}}
	provider, runner := configMapPatchProvider(t, object)
	receipt, err := provider.PatchObject(context.Background(), ObjectPatchRequest{APIVersion: "v1", Kind: "ConfigMap", Namespace: "ns1", Name: "conf", UID: "uid-conf",
		Patch: []PatchOp{{Op: "remove", Path: "/metadata/annotations"}}})
	if err != nil || len(receipt.Objects) != 1 {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	if _, present := runner.live["ns1/conf"].Object["metadata"].(map[string]any)["annotations"]; present {
		t.Fatalf("the patch did not remove the annotations")
	}
	if o := receipt.Objects[0]; o.Outcome != WriteUnchanged || len(o.Changes) != 0 {
		t.Fatalf("the last-applied annotation alone removed: %+v", o)
	}
}
