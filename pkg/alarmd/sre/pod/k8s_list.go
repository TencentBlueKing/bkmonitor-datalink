// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"
)

// OpListResources is the cluster read: one kind of Kubernetes object, listed
// or got, across the cluster or in one namespace.
const OpListResources = "k8s.list"

const (
	// DefaultResourceLimit and MaxResourceLimit bound one page: what the
	// apiserver returns and this process decodes for one call.
	DefaultResourceLimit = 50
	MaxResourceLimit     = 100
	// MaxResourcePaths bounds a projection.
	MaxResourcePaths = 32
	maxPathBytes     = 256
	// lastAppliedAnnotation is kubectl's copy of the applied manifest, env
	// values in clear included; it is dropped whole.
	lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"
)

// ResourceReader is the one Kubernetes read a resource read makes.
type ResourceReader interface {
	ListResources(ctx context.Context, resource schema.GroupVersionResource, namespace string, options metav1.ListOptions) (*unstructured.UnstructuredList, error)
	GetResource(ctx context.Context, resource schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error)
}

// resourceKind is one kind the read serves. The list is what the channel's
// ClusterRole grants get and list on, and nothing else: a kind added here
// without the grant reads as forbidden on every deployment.
type resourceKind struct {
	Kind       string
	Resource   schema.GroupVersionResource
	Namespaced bool
}

var resourceKinds = []resourceKind{
	{Kind: "Node", Resource: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}},
	{Kind: "Namespace", Resource: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}},
	{Kind: "Pod", Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Namespaced: true},
	{Kind: "Service", Resource: schema.GroupVersionResource{Version: "v1", Resource: "services"}, Namespaced: true},
	{Kind: "Endpoints", Resource: schema.GroupVersionResource{Version: "v1", Resource: "endpoints"}, Namespaced: true},
	{Kind: "Event", Resource: schema.GroupVersionResource{Version: "v1", Resource: "events"}, Namespaced: true},
	{Kind: "Deployment", Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, Namespaced: true},
	{Kind: "StatefulSet", Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, Namespaced: true},
	{Kind: "DaemonSet", Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, Namespaced: true},
	{Kind: "ReplicaSet", Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, Namespaced: true},
	{Kind: "Job", Resource: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, Namespaced: true},
	{Kind: "CronJob", Resource: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, Namespaced: true},
	{Kind: "Ingress", Resource: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, Namespaced: true},
	{Kind: "HorizontalPodAutoscaler", Resource: schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}, Namespaced: true},
}

// ResourceKinds is every kind the read serves, for the operation's schema.
func ResourceKinds() []string {
	kinds := make([]string, 0, len(resourceKinds))
	for _, kind := range resourceKinds {
		kinds = append(kinds, kind.Kind)
	}
	return kinds
}

// refusedKinds have operations of their own, which mask what this read
// would return in full.
var refusedKinds = map[string]string{
	"secret": OpReadSecret, "secrets": OpReadSecret,
	"configmap": OpReadConfigMap, "configmaps": OpReadConfigMap,
}

func lookupKind(value string) (resourceKind, error) {
	name := strings.ToLower(strings.TrimSpace(value))
	if op, refused := refusedKinds[name]; refused {
		return resourceKind{}, denied(value + " is read with " + op + ", which masks its values")
	}
	for _, kind := range resourceKinds {
		if name == strings.ToLower(kind.Kind) || name == kind.Resource.Resource {
			return kind, nil
		}
	}
	return resourceKind{}, denied("kind must be one of " + strings.Join(ResourceKinds(), ", "))
}

// ResourceRequest is one page of one kind: across the cluster when Namespace
// is empty, one object when Name is set.
type ResourceRequest struct {
	Kind          string   `json:"kind"`
	Namespace     string   `json:"namespace,omitempty"`
	Name          string   `json:"name,omitempty"`
	LabelSelector string   `json:"label_selector,omitempty"`
	FieldSelector string   `json:"field_selector,omitempty"`
	Paths         []string `json:"paths,omitempty"`
	Continue      string   `json:"continue,omitempty"`
	Limit         int      `json:"limit,omitempty"`
}

// ResourceReceipt is what one read returned. Items are the objects as
// cleaned (cleanObject) and, with paths, projected; each is whole or, when
// one object alone is over the output bound, its identity only and named in
// OversizedItems. Truncated says the page held more than the bound let
// through: the rest are omitted, Continue is empty, and ReadWith is the
// token this page was read with, to read it again with a smaller limit.
type ResourceReceipt struct {
	Kind              string            `json:"kind"`
	APIVersion        string            `json:"api_version"`
	Namespace         string            `json:"namespace"`
	ClusterWide       bool              `json:"cluster_wide"`
	Items             []json.RawMessage `json:"items"`
	Count             int               `json:"count"`
	Continue          string            `json:"continue,omitempty"`
	ResourceVersion   string            `json:"resource_version,omitempty"`
	Truncated         bool              `json:"truncated"`
	ItemsOmitted      int               `json:"items_omitted,omitempty"`
	ReadWith          string            `json:"read_with,omitempty"`
	OversizedItems    []string          `json:"oversized_items,omitempty"`
	RedactionContract string            `json:"redaction_contract"`
	At                time.Time         `json:"at"`
}

// identityPaths are on every item, projected or not.
var identityPaths = [][]string{{"apiVersion"}, {"kind"}, {"metadata", "namespace"}, {"metadata", "name"}, {"metadata", "uid"}}

// ListResources reads one page of one kind, or one object. A page costs the
// apiserver one LIST of at most MaxResourceLimit objects, and this process
// their decoding; reading a whole cluster is the caller's walk, page by page,
// each under the same bound.
func (p *Provider) ListResources(ctx context.Context, request ResourceRequest) (ResourceReceipt, error) {
	result := ResourceReceipt{Items: []json.RawMessage{}, RedactionContract: RedactionContract, At: p.options.Now().UTC()}
	kind, err := lookupKind(request.Kind)
	if err != nil {
		return result, err
	}
	result.Kind, result.APIVersion = kind.Kind, kind.Resource.GroupVersion().String()
	if p.options.Resources == nil {
		return result, &Error{Code: CodeAPIError, Message: "Kubernetes resource adapter is unavailable"}
	}
	paths, err := resourcePaths(request.Paths)
	if err != nil {
		return result, err
	}
	switch {
	case !kind.Namespaced && request.Namespace != "":
		return result, invalid(kind.Kind + " is not namespaced; leave namespace out")
	case request.Namespace != "" && len(validation.IsDNS1123Label(request.Namespace)) != 0:
		return result, invalid("namespace must be one Kubernetes namespace name")
	case request.Name != "" && len(validation.IsDNS1123Subdomain(request.Name)) != 0:
		return result, invalid("name must be a Kubernetes object name")
	case request.Name != "" && (request.LabelSelector != "" || request.FieldSelector != "" || request.Continue != ""):
		return result, invalid("name reads one object; selectors and continue are for a list")
	case request.Name != "" && kind.Namespaced && request.Namespace == "":
		return result, invalid("name of a namespaced kind needs its namespace")
	}
	if _, err := labels.Parse(request.LabelSelector); err != nil {
		return result, invalid("label_selector does not parse")
	}
	if _, err := fields.ParseSelector(request.FieldSelector); err != nil {
		return result, invalid("field_selector does not parse")
	}
	limit := request.Limit
	if limit == 0 {
		limit = DefaultResourceLimit
	}
	if limit < 1 || limit > MaxResourceLimit {
		return result, invalid("limit must be between 1 and 100")
	}
	result.Namespace, result.ClusterWide = request.Namespace, kind.Namespaced && request.Namespace == ""
	release, err := p.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	var objects []unstructured.Unstructured
	if request.Name != "" {
		object, err := p.options.Resources.GetResource(ctx, kind.Resource, request.Namespace, request.Name)
		if err != nil {
			return result, apiError(err)
		}
		objects = []unstructured.Unstructured{*object}
	} else {
		list, err := p.options.Resources.ListResources(ctx, kind.Resource, request.Namespace, metav1.ListOptions{
			LabelSelector: request.LabelSelector, FieldSelector: request.FieldSelector, Continue: request.Continue, Limit: int64(limit)})
		if err != nil {
			return result, apiError(err)
		}
		objects, result.Continue, result.ResourceVersion = list.Items, list.GetContinue(), list.GetResourceVersion()
	}
	remaining := p.options.MaxOutputBytes
	for index, object := range objects {
		item := p.cleanObject(object.Object)
		if len(paths) > 0 {
			item = project(item, append(append([][]string(nil), identityPaths...), paths...))
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return result, &Error{Code: CodeAPIError, Message: "a Kubernetes object did not encode"}
		}
		if int64(len(encoded)) > p.options.MaxOutputBytes {
			// Alone over the bound: its identity, so the caller can read it
			// again with paths.
			encoded, _ = json.Marshal(project(item, identityPaths))
			result.OversizedItems = append(result.OversizedItems, object.GetNamespace()+"/"+object.GetName())
		}
		if int64(len(encoded)) > remaining {
			result.Truncated, result.ItemsOmitted = true, len(objects)-index
			result.ReadWith, result.Continue = request.Continue, ""
			break
		}
		remaining -= int64(len(encoded))
		result.Items = append(result.Items, encoded)
	}
	result.Count = len(result.Items)
	return result, nil
}

func resourcePaths(values []string) ([][]string, error) {
	if len(values) > MaxResourcePaths {
		return nil, invalid("at most 32 paths")
	}
	paths := make([][]string, 0, len(values))
	for _, value := range values {
		segments := strings.Split(value, ".")
		if len(value) == 0 || len(value) > maxPathBytes || sliceHasEmpty(segments) {
			return nil, invalid("a path is dot-separated field names, as status.addresses")
		}
		paths = append(paths, segments)
	}
	return paths, nil
}

func sliceHasEmpty(values []string) bool {
	for _, value := range values {
		if value == "" {
			return true
		}
	}
	return false
}

// cleanObject is an object as the read returns it:
//   - metadata.managedFields dropped, and the last-applied annotation, which
//     can carry env values in clear;
//   - an annotation under a credential-like key masked whole, as configmap.get
//     masks such a key;
//   - in any env list, the value of an entry whose name is credential-like
//     masked whole;
//   - every string through the output redaction.
//
// References by name - secretKeyRef, configMapKeyRef, envFrom - are left as
// they are on purpose: a Secret's or ConfigMap's name is not its content,
// and it is how a reader finds which one a workload reads.
func (p *Provider) cleanObject(object map[string]any) map[string]any {
	if metadata, ok := object["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
		if annotations, ok := metadata["annotations"].(map[string]any); ok {
			delete(annotations, lastAppliedAnnotation)
			for key, value := range annotations {
				if text, isText := value.(string); isText && text != "" && credentialKey.MatchString(key) {
					annotations[key] = "<redacted>"
				}
			}
		}
	}
	cleaned, _ := p.cleanValue(object).(map[string]any)
	return cleaned
}

func (p *Provider) cleanValue(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if key == "env" {
				maskEnv(item)
			}
			v[key] = p.cleanValue(item)
		}
		return v
	case []any:
		for index := range v {
			v[index] = p.cleanValue(v[index])
		}
		return v
	case string:
		redacted, _ := p.redactor.bounded(v, math.MaxInt64)
		return redacted
	default:
		return v
	}
}

func maskEnv(value any) {
	entries, ok := value.([]any)
	if !ok {
		return
	}
	for _, entry := range entries {
		variable, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		name, _ := variable["name"].(string)
		if text, isText := variable["value"].(string); isText && text != "" && credentialKey.MatchString(name) {
			variable["value"] = "<redacted>"
		}
	}
}

// project keeps the given paths of an object. A list on the way is walked
// element by element, each element keeping the rest of the path; the
// elements stay in place, so two paths into one list merge by position.
func project(object map[string]any, paths [][]string) map[string]any {
	out := map[string]any{}
	for _, path := range paths {
		if kept, found := projectPath(object, path); found {
			out = mergeProjected(out, kept).(map[string]any)
		}
	}
	return out
}

func projectPath(value any, path []string) (any, bool) {
	if len(path) == 0 {
		return value, true
	}
	switch v := value.(type) {
	case map[string]any:
		item, present := v[path[0]]
		if !present {
			return nil, false
		}
		kept, found := projectPath(item, path[1:])
		if !found {
			return nil, false
		}
		return map[string]any{path[0]: kept}, true
	case []any:
		out := make([]any, len(v))
		kept := false
		for index, element := range v {
			if value, found := projectPath(element, path); found {
				out[index], kept = value, true
			} else {
				out[index] = map[string]any{}
			}
		}
		return out, kept
	default:
		return nil, false
	}
}

func mergeProjected(into, from any) any {
	switch f := from.(type) {
	case map[string]any:
		target, ok := into.(map[string]any)
		if !ok {
			return f
		}
		keys := make([]string, 0, len(f))
		for key := range f {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if existing, present := target[key]; present {
				target[key] = mergeProjected(existing, f[key])
			} else {
				target[key] = f[key]
			}
		}
		return target
	case []any:
		target, ok := into.([]any)
		if !ok || len(target) != len(f) {
			return f
		}
		for index := range f {
			target[index] = mergeProjected(target[index], f[index])
		}
		return target
	default:
		return from
	}
}
