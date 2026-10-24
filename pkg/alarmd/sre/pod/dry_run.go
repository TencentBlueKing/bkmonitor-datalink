// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pod

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
)

// OpDryRun asks the apiserver what it would do with a set of objects: each
// is applied server-side with dryRun=All, so admission webhooks, schema,
// quota and conflicts all run and nothing is persisted. It is how a
// rendered chart is checked against the cluster before anyone deploys it.
const OpDryRun = "k8s.dry_run"

// The bounds of one dry run: how many objects and how many manifest bytes --
// under the channel's 1 MiB request envelope, with room for the JSON
// escaping around them. A larger render goes in several runs.
const (
	MaxDryRunObjects = 1000
	MaxDryRunBytes   = 15 << 20
	// dryRunMessageBytes bounds one object's message as returned.
	dryRunMessageBytes = 4096
)

// The verdicts, closed. denied is the apiserver refusing the object --
// an admission webhook, validation, a quota, forbidden; manager_conflict a
// field ownership conflict, which is not a refusal of the object; not_checked
// an object this process could not ask about (a kind the cluster does not
// serve, no name, no namespace) or a call that got no answer.
const (
	DryRunWouldCreate     = "would_create"
	DryRunWouldUpdate     = "would_update"
	DryRunUnchanged       = "unchanged"
	DryRunDenied          = "denied"
	DryRunManagerConflict = "manager_conflict"
	DryRunNotChecked      = "not_checked"
	// DryRunNotAttempted is an object the run did not reach before its time
	// ran out.
	DryRunNotAttempted = "not_attempted"
	// DryRunRBACNotDryRun is a Role, ClusterRole or binding, not sent: the
	// apiserver runs RBAC's escalation check even on a dry run, and passing
	// it would need the bind and escalate verbs -- a path to any permission
	// for whoever holds this process. It does not make a run unclean.
	DryRunRBACNotDryRun = "rbac_not_dry_run"
	// DryRunNotGrantedKind is a kind outside what the channel's ClusterRole
	// grants a dry run on, refused before any call: a chart that starts
	// rendering a new kind shows as a grant to extend, not as a 403.
	DryRunNotGrantedKind = "not_granted_kind"
)

// dryRunGranted is the kinds the channel's ClusterRole grants management
// on (get, list, create, patch, delete), RBAC aside: what the chart renders
// and the common workload, storage, network and monitoring kinds. The chart
// and this list change together. Namespace and Node are cluster-scoped;
// a Node is granted patch only, so a dry run of a Node that does not exist
// comes back denied, as it should.
var dryRunGranted = map[schema.GroupKind]bool{
	{Kind: "Namespace"}: true, {Kind: "Node"}: true,
	{Kind: "ConfigMap"}: true, {Kind: "Service"}: true, {Kind: "Secret"}: true, {Kind: "ServiceAccount"}: true,
	{Kind: "PersistentVolumeClaim"}: true, {Kind: "Endpoints"}: true,
	{Group: "apps", Kind: "Deployment"}: true, {Group: "apps", Kind: "StatefulSet"}: true, {Group: "apps", Kind: "DaemonSet"}: true,
	{Group: "batch", Kind: "Job"}: true, {Group: "batch", Kind: "CronJob"}: true,
	{Group: "networking.k8s.io", Kind: "Ingress"}: true, {Group: "networking.k8s.io", Kind: "NetworkPolicy"}: true,
	{Group: "autoscaling", Kind: "HorizontalPodAutoscaler"}: true, {Group: "policy", Kind: "PodDisruptionBudget"}: true,
	{Group: "monitoring.coreos.com", Kind: "ServiceMonitor"}: true, {Group: "monitoring.coreos.com", Kind: "PodMonitor"}: true,
	{Group: "bk.tencent.com", Kind: "BkLogConfig"}: true, {Group: "monitoring.bk.tencent.com", Kind: "DataID"}: true,
}

// rbacGroup is the group whose objects a dry run does not send.
const rbacGroup = "rbac.authorization.k8s.io"

// DryRunVerdicts is every verdict an object can carry.
var DryRunVerdicts = []string{DryRunWouldCreate, DryRunWouldUpdate, DryRunUnchanged, DryRunDenied, DryRunManagerConflict, DryRunNotChecked, DryRunRBACNotDryRun, DryRunNotGrantedKind}

// DryRunner is the Kubernetes access a dry run has. ApplyDryRun is its only
// write, and the adapter behind it sets dryRun=All on every call; nothing a
// caller sends can reach that option.
type DryRunner interface {
	// Mapping resolves an object's apiVersion and kind to its resource and
	// whether it is namespaced, as this cluster serves it.
	Mapping(apiVersion, kind string) (schema.GroupVersionResource, bool, error)
	GetResource(ctx context.Context, resource schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error)
	ApplyDryRun(ctx context.Context, resource schema.GroupVersionResource, namespace, name string, data []byte) (*unstructured.Unstructured, error)
}

// DryRunRequest is a manifest stream -- YAML documents or JSON objects, as a
// chart renders them -- and the namespace a namespaced object without one
// goes to. Empty, such an object is not checked.
type DryRunRequest struct {
	Manifests string `json:"manifests"`
	Namespace string `json:"namespace,omitempty"`
}

// DryRunCause is one cause the apiserver gave, as it gave it.
type DryRunCause struct {
	Type    string `json:"type,omitempty"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message,omitempty"`
}

// DryRunObject is one object's answer: what the apiserver would do with it,
// and when it refused, its status verbatim -- reason, code, message and
// causes -- with the object's own Secret values masked.
type DryRunObject struct {
	APIVersion string `json:"api_version"`
	Kind       string `json:"kind"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name"`
	Verdict    string `json:"verdict"`
	// ChangedPaths is, for would_update, every field the apply changes:
	// leaves by path (maps by key, lists by index, a key on one side only,
	// a list that changes length as the list). A Secret's values are
	// compared here and named by key only (data.KEY), never answered.
	ChangedPaths []string      `json:"changed_paths,omitempty"`
	Reason       string        `json:"reason,omitempty"`
	Code         int32         `json:"code,omitempty"`
	Message      string        `json:"message,omitempty"`
	Causes       []DryRunCause `json:"causes,omitempty"`
}

// DryRunReceipt is the whole answer: every object in manifest order and how
// many carry each verdict. Clean says every object would be created,
// updated or is unchanged -- nothing denied, conflicted or unchecked.
type DryRunReceipt struct {
	Objects []DryRunObject `json:"objects"`
	Counts  map[string]int `json:"counts"`
	Clean   bool           `json:"clean"`
	// ResumeFrom is the index of the first object not checked when the
	// operation's time ran out between objects; nil when every object was
	// checked. The objects checked keep their verdicts.
	ResumeFrom *int `json:"resume_from,omitempty"`
}

// DryRun runs the manifests through the apiserver with dryRun=All.
func (p *Provider) DryRun(ctx context.Context, request DryRunRequest) (DryRunReceipt, error) {
	receipt := DryRunReceipt{Objects: []DryRunObject{}, Counts: map[string]int{}}
	if p.options.DryRun == nil {
		return receipt, &Error{Code: CodeAPIError, Message: "Kubernetes dry run adapter is unavailable"}
	}
	if len(request.Manifests) > MaxDryRunBytes {
		return receipt, &Error{Code: "dry_run_too_large", Message: fmt.Sprintf("manifests exceed %d bytes", MaxDryRunBytes)}
	}
	if request.Namespace != "" && len(validation.IsDNS1123Label(request.Namespace)) != 0 {
		return receipt, invalid("namespace must be one Kubernetes namespace name")
	}
	objects, err := decodeManifests(request.Manifests)
	if err != nil {
		return receipt, invalid("manifests do not decode: " + err.Error())
	}
	if len(objects) == 0 {
		return receipt, invalid("manifests hold no object")
	}
	if len(objects) > MaxDryRunObjects {
		return receipt, &Error{Code: "dry_run_too_many_objects", Message: fmt.Sprintf("%d objects, at most %d in one dry run", len(objects), MaxDryRunObjects)}
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return receipt, err
	}
	defer release()
	budget := newObjectBudget(ctx, p.options.Now)
	for index, object := range objects {
		if receipt.ResumeFrom == nil && !budget.canStart() {
			resume := index
			receipt.ResumeFrom = &resume
		}
		if receipt.ResumeFrom != nil {
			receipt.Objects = append(receipt.Objects, DryRunObject{APIVersion: object.GetAPIVersion(), Kind: object.GetKind(),
				Namespace: object.GetNamespace(), Name: object.GetName(), Verdict: DryRunNotAttempted,
				Message: "the operation's time ran out before this object"})
			receipt.Counts[DryRunNotAttempted]++
			continue
		}
		started := p.options.Now()
		answer := p.dryRunOne(ctx, object, request.Namespace)
		budget.took(p.options.Now().Sub(started))
		receipt.Objects = append(receipt.Objects, answer)
		receipt.Counts[answer.Verdict]++
	}
	receipt.Clean = receipt.Counts[DryRunDenied] == 0 && receipt.Counts[DryRunManagerConflict] == 0 && receipt.Counts[DryRunNotChecked] == 0 &&
		receipt.Counts[DryRunNotGrantedKind] == 0 && receipt.Counts[DryRunNotAttempted] == 0
	return receipt, nil
}

func (p *Provider) dryRunOne(ctx context.Context, object *unstructured.Unstructured, defaultNamespace string) DryRunObject {
	answer := DryRunObject{APIVersion: object.GetAPIVersion(), Kind: object.GetKind(), Namespace: object.GetNamespace(), Name: object.GetName()}
	notChecked := func(message string) DryRunObject {
		answer.Verdict, answer.Message = DryRunNotChecked, message
		return answer
	}
	resource, outcome, message := p.gate(object, defaultNamespace, DryRunRBACNotDryRun, DryRunNotGrantedKind)
	answer.Namespace = object.GetNamespace()
	if outcome != "" {
		answer.Verdict, answer.Message = outcome, message
		return answer
	}
	data, err := json.Marshal(object.Object)
	if err != nil {
		return notChecked("the object does not encode")
	}
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	live, err := p.options.DryRun.GetResource(ctx, resource, answer.Namespace, answer.Name)
	existed := err == nil
	if err != nil && !apierrors.IsNotFound(err) {
		return p.refused(answer, err, object)
	}
	applied, err := p.options.DryRun.ApplyDryRun(ctx, resource, answer.Namespace, answer.Name, data)
	if err != nil {
		return p.refused(answer, err, object)
	}
	switch {
	case !existed:
		answer.Verdict = DryRunWouldCreate
	default:
		answer.ChangedPaths = changedPaths(cleanForCompare(live), cleanForCompare(applied), "")
		answer.Verdict = DryRunWouldUpdate
		if len(answer.ChangedPaths) == 0 {
			answer.Verdict = DryRunUnchanged
		}
	}
	return answer
}

// refused files an apiserver error on the object: a field ownership
// conflict apart from a refusal, the status verbatim but for the object's
// own Secret values, and an error without a status as not checked.
func (p *Provider) refused(answer DryRunObject, err error, object *unstructured.Unstructured) DryRunObject {
	mask := secretMasker(object)
	var status apierrors.APIStatus
	if !errors.As(err, &status) {
		answer.Verdict, answer.Message = DryRunNotChecked, bound(mask(err.Error()))
		return answer
	}
	details := status.Status()
	answer.Verdict = DryRunDenied
	if apierrors.IsConflict(err) {
		answer.Verdict = DryRunManagerConflict
	}
	answer.Reason, answer.Code, answer.Message = string(details.Reason), details.Code, bound(mask(details.Message))
	if details.Details != nil {
		for _, cause := range details.Details.Causes {
			answer.Causes = append(answer.Causes, DryRunCause{Type: string(cause.Type), Field: cause.Field, Message: bound(mask(cause.Message))})
		}
	}
	return answer
}

// cleanForCompare is an object without what an apply always touches or
// cannot set: the bookkeeping (managedFields, resourceVersion, generation)
// and the status.
func cleanForCompare(object *unstructured.Unstructured) map[string]any {
	copied := object.DeepCopy().Object
	for _, field := range []string{"managedFields", "resourceVersion", "generation"} {
		unstructured.RemoveNestedField(copied, "metadata", field)
	}
	delete(copied, "status")
	return copied
}

// changedPaths is every leaf where live and applied differ, in path order.
// Values are compared, never returned: a Secret's data.KEY says its value
// changes and nothing about the value.
func changedPaths(live, applied any, path string) []string {
	join := func(key string) string {
		if path == "" {
			return key
		}
		return path + "." + key
	}
	switch a := live.(type) {
	case map[string]any:
		b, ok := applied.(map[string]any)
		if !ok {
			break
		}
		keys := make([]string, 0, len(a)+len(b))
		for key := range a {
			keys = append(keys, key)
		}
		for key := range b {
			if _, seen := a[key]; !seen {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		var out []string
		for _, key := range keys {
			out = append(out, changedPaths(a[key], b[key], join(key))...)
		}
		return out
	case []any:
		b, ok := applied.([]any)
		if !ok || len(a) != len(b) {
			break
		}
		var out []string
		for i := range a {
			out = append(out, changedPaths(a[i], b[i], path+"["+strconv.Itoa(i)+"]")...)
		}
		return out
	}
	if reflect.DeepEqual(live, applied) {
		return nil
	}
	return []string{path}
}

// secretMasker replaces a Secret object's own values in a message: the
// apiserver can quote an invalid value back, and a dry run's answer never
// carries one. Other kinds pass through.
func secretMasker(object *unstructured.Unstructured) func(string) string {
	if object.GetKind() != "Secret" {
		return func(text string) string { return text }
	}
	var values []string
	for _, field := range []string{"data", "stringData"} {
		entries, _, _ := unstructured.NestedMap(object.Object, field)
		for _, raw := range entries {
			text, ok := raw.(string)
			if !ok || text == "" {
				continue
			}
			values = append(values, text)
			if decoded, err := base64.StdEncoding.DecodeString(text); err == nil && len(decoded) > 0 {
				values = append(values, string(decoded))
			}
		}
	}
	masker := newRedactor(values)
	return func(text string) string {
		masked, _ := masker.bounded(text, int64(len(text))+1)
		return masked
	}
}

func bound(text string) string {
	if len(text) <= dryRunMessageBytes {
		return text
	}
	return strings.ToValidUTF8(text[:dryRunMessageBytes], "") + "..."
}

// decodeManifests reads a stream of YAML documents or JSON objects into
// objects, unwrapping lists, skipping empty documents.
func decodeManifests(manifests string) ([]*unstructured.Unstructured, error) {
	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader([]byte(manifests)), 4096)
	var objects []*unstructured.Unstructured
	for {
		var raw map[string]any
		if err := decoder.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return objects, nil
			}
			return nil, err
		}
		if len(raw) == 0 {
			continue
		}
		object := &unstructured.Unstructured{Object: raw}
		if object.IsList() {
			list, err := object.ToList()
			if err != nil {
				return nil, err
			}
			for index := range list.Items {
				objects = append(objects, &list.Items[index])
			}
			continue
		}
		objects = append(objects, object)
		if len(objects) > MaxDryRunObjects {
			return objects, nil
		}
	}
}

// dryRunPatchOptions is every dry run's apply: the apiserver runs all of
// admission and persists nothing (dryRun=All), under its own field manager,
// forcing ownership so a field another manager owns -- helm's -- is not
// read as a refusal. Forcing changes nothing: nothing is persisted.
func dryRunPatchOptions() metav1.PatchOptions {
	force := true
	return metav1.PatchOptions{DryRun: []string{metav1.DryRunAll}, FieldManager: "alarmd-dry-run", Force: &force}
}
