// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// OpPatch is a guarded JSON patch of one object.
const OpPatch = "k8s.patch"

// WriteTestFailed is a patch whose test did not hold: the apiserver applied
// none of it.
const WriteTestFailed = "test_failed"

// PatchOp is one RFC 6902 operation. Value is the op's value as JSON; test,
// add and replace need one, remove takes none.
type PatchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

// ObjectPatchRequest names one object by kind and the uid a read returned,
// and the operations to apply to it.
type ObjectPatchRequest struct {
	APIVersion string    `json:"api_version"`
	Kind       string    `json:"kind"`
	Namespace  string    `json:"namespace,omitempty"`
	Name       string    `json:"name"`
	UID        string    `json:"uid"`
	Patch      []PatchOp `json:"patch"`
}

var patchOps = map[string]bool{"test": true, "remove": true, "replace": true, "add": true}

func validatePatch(request ObjectPatchRequest) error {
	if request.UID == "" {
		return invalid("uid is required: patch the object a read returned, by its uid")
	}
	if len(request.Patch) == 0 {
		return invalid("patch holds no operation")
	}
	for _, op := range request.Patch {
		if !patchOps[op.Op] {
			return invalid("op must be test, remove, replace or add, not " + bound(op.Op))
		}
		if !strings.HasPrefix(op.Path, "/") {
			return invalid("path must be a JSON pointer starting with /")
		}
		if op.Op != "remove" && len(op.Value) == 0 {
			return invalid(op.Op + " needs a value")
		}
	}
	return nil
}

// PatchObject applies a JSON patch to one object, if it is still the one the
// uid names: a test of the uid goes first, and the apiserver applies the
// whole patch or, when any test fails, none of it. The receipt lists the
// fields changed, masked as k8s.list masks them, and whether the Pod
// template changed (will_roll).
func (p *Provider) PatchObject(ctx context.Context, request ObjectPatchRequest) (WriteReceipt, error) {
	receipt := WriteReceipt{Objects: []WriteObject{}, Counts: map[string]int{}}
	if p.options.DryRun == nil || p.options.Writer == nil {
		return receipt, &Error{Code: CodeAPIError, Message: "Kubernetes write adapter is unavailable"}
	}
	if err := validatePatch(request); err != nil {
		return receipt, err
	}
	object := &unstructured.Unstructured{Object: map[string]any{"apiVersion": request.APIVersion, "kind": request.Kind,
		"metadata": map[string]any{"name": request.Name}}}
	if request.Namespace != "" {
		object.SetNamespace(request.Namespace)
	}
	answer := WriteObject{APIVersion: request.APIVersion, Kind: request.Kind, Namespace: request.Namespace, Name: request.Name, UID: request.UID}
	record := func(answer WriteObject) (WriteReceipt, error) {
		receipt.Objects = append(receipt.Objects, answer)
		receipt.Counts[answer.Outcome]++
		return receipt, nil
	}
	resource, outcome, message := p.gate(object, "", WriteRBACNotWritable, WriteNotGrantedKind)
	if outcome != "" {
		answer.Outcome, answer.Message = outcome, message
		return record(answer)
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return receipt, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	live, err := p.options.DryRun.GetResource(ctx, resource, request.Namespace, request.Name)
	switch {
	case apierrors.IsNotFound(err):
		answer.Outcome = WriteGone
		return record(answer)
	case err != nil:
		return record(p.writeRefused(answer, err, object))
	case string(live.GetUID()) != request.UID:
		answer.Outcome, answer.Message = WritePrecondition, "the object is not the one the uid names: it was replaced"
		return record(answer)
	}
	answer.HelmRelease = helmRelease(live)
	uid, _ := json.Marshal(request.UID)
	patch, err := json.Marshal(append([]PatchOp{{Op: "test", Path: "/metadata/uid", Value: uid}}, request.Patch...))
	if err != nil {
		answer.Outcome, answer.Message = WriteNotChecked, "the patch does not encode"
		return record(answer)
	}
	patched, err := p.options.Writer.JSONPatch(ctx, resource, request.Namespace, request.Name, patch)
	if err != nil {
		refused := p.writeRefused(answer, err, object)
		if failure, isTest := replayRefused(err, live, request.Patch); failure != "" {
			if isTest {
				refused.Outcome = WriteTestFailed
			}
			refused.Message = failure + ", evaluated against the object as read at resourceVersion " +
				live.GetResourceVersion() + "; the apiserver applied nothing (" + refused.Message + ")"
		}
		return record(refused)
	}
	fingerprints := p.newFingerprints()
	answer.Changes, answer.ChangesOmitted = p.writeChanges(live, patched, fingerprints)
	// Whether the Pods roll is the stored templates' question: a change the
	// receipt masks rolls them all the same.
	answer.WillRoll = !reflect.DeepEqual(templateOf(p.stored(live, fingerprints)), templateOf(p.stored(patched, fingerprints)))
	answer.Outcome = WriteUpdated
	if len(answer.Changes) == 0 {
		answer.Outcome = WriteUnchanged
	}
	return record(answer)
}

// replayRefused says why the apiserver refused a patch with a 422, which it
// answers bare (no causes, no words of the patch library) for a failed test
// and for a validation or admission refusal alike. The request's ops are
// replayed in order, each on the result of the ones before, as RFC 6902
// applies them, starting from the raw object as read (never the masked copy
// shown in receipts). The first op that does not apply names the reason: a
// test makes it a failed test, any other op stays a denial with that op
// named. When every op applies, the apiserver refused the result: a denial
// with nothing to add. The replay sees the object as read, not what the
// apiserver saw a moment later.
func replayRefused(err error, live *unstructured.Unstructured, ops []PatchOp) (string, bool) {
	var status apierrors.APIStatus
	if !errors.As(err, &status) || status.Status().Code != http.StatusUnprocessableEntity {
		return "", false
	}
	doc, encodeErr := json.Marshal(live.Object)
	if encodeErr != nil {
		return "", false
	}
	for index, op := range ops {
		single, _ := json.Marshal([]PatchOp{op})
		decoded, decodeErr := jsonpatch.DecodePatch(single)
		if decodeErr != nil {
			return "", false
		}
		next, applyErr := decoded.Apply(doc)
		if applyErr != nil {
			if op.Op == "test" {
				return "test at " + op.Path + " does not hold", true
			}
			return fmt.Sprintf("op %d %s %s does not apply", index, op.Op, op.Path), false
		}
		doc = next
	}
	return "", false
}

// templateOf is an object's Pod template, nil for an object without one.
func templateOf(object map[string]any) any {
	spec, _ := object["spec"].(map[string]any)
	return spec["template"]
}
