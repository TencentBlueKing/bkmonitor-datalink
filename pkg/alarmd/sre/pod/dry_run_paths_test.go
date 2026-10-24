// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func deployment(image string, annotations map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "web", "namespace": "ns1"},
		"spec": map[string]any{"replicas": int64(1), "template": map[string]any{
			"metadata": map[string]any{"annotations": annotations},
			"spec":     map[string]any{"containers": []any{map[string]any{"name": "web", "image": image}}}}}}}
}

func pathsRunner(live map[string]*unstructured.Unstructured) *fakeDryRunner {
	return &fakeDryRunner{live: live, mappings: map[string]schema.GroupVersionResource{
		"apps/v1/Deployment": {Group: "apps", Version: "apps", Resource: "deployments"},
		"v1/Secret":          {Version: "v1", Resource: "secrets"},
		"v1/ConfigMap":       {Version: "v1", Resource: "configmaps"},
	}}
}

func dryRunOneManifest(t *testing.T, runner *fakeDryRunner, manifest any) DryRunObject {
	t.Helper()
	data, _ := json.Marshal(manifest)
	receipt, err := dryRunProvider(t, runner).DryRun(context.Background(), DryRunRequest{Manifests: string(data), Namespace: "ns1"})
	if err != nil || len(receipt.Objects) != 1 {
		t.Fatalf("receipt %+v err %v", receipt, err)
	}
	return receipt.Objects[0]
}

// An update says which fields it changes: a new image is that one path, and
// an annotation the template gains is its key's path; the bookkeeping an
// apply always touches and the status are not changes.
func TestAnUpdateNamesTheFieldsItChanges(t *testing.T) {
	live := deployment("web:1", map[string]any{"checksum/config": "a"})
	live.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "helm"}})
	live.SetResourceVersion("7")
	live.SetGeneration(3)
	live.Object["status"] = map[string]any{"replicas": int64(1)}
	runner := pathsRunner(map[string]*unstructured.Unstructured{"ns1/web": live})
	object := dryRunOneManifest(t, runner, deployment("web:2", map[string]any{"checksum/config": "a", "checksum/envs": "e"}).Object)
	if object.Verdict != DryRunWouldUpdate || strings.Join(object.ChangedPaths, " ") !=
		"spec.template.metadata.annotations.checksum/envs spec.template.spec.containers[0].image" {
		t.Fatalf("object %+v", object)
	}

	// Only bookkeeping and status differ: unchanged, no paths.
	same := deployment("web:1", map[string]any{"checksum/config": "a"})
	runner.applied = func(name string, applied map[string]any) (*unstructured.Unstructured, error) {
		out := &unstructured.Unstructured{Object: applied}
		out.SetResourceVersion("8")
		out.SetGeneration(4)
		out.SetManagedFields([]metav1.ManagedFieldsEntry{{Manager: "alarmd-dry-run"}})
		out.Object["status"] = map[string]any{"replicas": int64(1)}
		return out, nil
	}
	object = dryRunOneManifest(t, runner, same.Object)
	if object.Verdict != DryRunUnchanged || len(object.ChangedPaths) != 0 {
		t.Fatalf("object %+v", object)
	}
}

// A key on one side only is a path, and so is a list that changes length.
func TestAddedRemovedAndResizedAreChanges(t *testing.T) {
	live := configMap("ns1", "cm", map[string]any{"gone": "x", "kept": "y"})
	runner := pathsRunner(map[string]*unstructured.Unstructured{"ns1/cm": live})
	object := dryRunOneManifest(t, runner, configMap("ns1", "cm", map[string]any{"kept": "y", "new": "z"}).Object)
	if strings.Join(object.ChangedPaths, " ") != "data.gone data.new" {
		t.Fatalf("paths %v", object.ChangedPaths)
	}
	two := deployment("web:1", map[string]any{"a": "b"})
	containers := two.Object["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	containers["containers"] = append(containers["containers"].([]any), map[string]any{"name": "side", "image": "side:1"})
	runner = pathsRunner(map[string]*unstructured.Unstructured{"ns1/web": deployment("web:1", map[string]any{"a": "b"})})
	object = dryRunOneManifest(t, runner, two.Object)
	if strings.Join(object.ChangedPaths, " ") != "spec.template.spec.containers" {
		t.Fatalf("paths %v", object.ChangedPaths)
	}
}

// A Secret's changed value is named by its key; the value is compared
// inside the process and never answered.
func TestASecretChangeIsItsKeyNeverItsValue(t *testing.T) {
	secret := func(value string) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Secret",
			"metadata": map[string]any{"name": "s", "namespace": "ns1"}, "data": map[string]any{"TOKEN": value, "SAME": "c2FtZQ=="}}}
	}
	runner := pathsRunner(map[string]*unstructured.Unstructured{"ns1/s": secret("bGl2ZS1zZW50aW5lbA==")})
	object := dryRunOneManifest(t, runner, secret("bmV3LXNlbnRpbmVs").Object)
	encoded, _ := json.Marshal(object)
	if object.Verdict != DryRunWouldUpdate || strings.Join(object.ChangedPaths, " ") != "data.TOKEN" ||
		strings.Contains(string(encoded), "bGl2ZS1zZW50aW5lbA") || strings.Contains(string(encoded), "bmV3LXNlbnRpbmVs") {
		t.Fatalf("object %s", encoded)
	}
}
