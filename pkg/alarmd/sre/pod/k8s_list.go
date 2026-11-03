// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	MaxResourceLimit     = 500
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
	// Custom is a custom resource on the fixed list: a list the apiserver
	// answers not-found means its CRD is not installed.
	Custom bool
	// KeysOnly returns the object as its metadata and its data keys with
	// their sizes, never a value (keysOnlyRow); Fingerprinted adds each
	// value's fingerprint, for Secrets.
	KeysOnly      bool
	Fingerprinted bool
	// Webhooks drops each webhook's clientConfig.caBundle: the CA a webhook
	// is called under is the cluster's trust material, and which webhooks
	// gate what needs only their rules, selectors and targets.
	Webhooks bool
}

var resourceKinds = []resourceKind{
	{Kind: "Node", Resource: schema.GroupVersionResource{Version: "v1", Resource: "nodes"}},
	{Kind: "Namespace", Resource: schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}},
	{Kind: "Pod", Resource: schema.GroupVersionResource{Version: "v1", Resource: "pods"}, Namespaced: true},
	{Kind: "Service", Resource: schema.GroupVersionResource{Version: "v1", Resource: "services"}, Namespaced: true},
	{Kind: "Endpoints", Resource: schema.GroupVersionResource{Version: "v1", Resource: "endpoints"}, Namespaced: true},
	// Endpoints stops at 1,000 addresses per Service; the slices hold them all.
	{Kind: "EndpointSlice", Resource: schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}, Namespaced: true},
	{Kind: "Event", Resource: schema.GroupVersionResource{Version: "v1", Resource: "events"}, Namespaced: true},
	{Kind: "Deployment", Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}, Namespaced: true},
	{Kind: "StatefulSet", Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "statefulsets"}, Namespaced: true},
	{Kind: "DaemonSet", Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "daemonsets"}, Namespaced: true},
	{Kind: "ReplicaSet", Resource: schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "replicasets"}, Namespaced: true},
	{Kind: "Job", Resource: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, Namespaced: true},
	{Kind: "CronJob", Resource: schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "cronjobs"}, Namespaced: true},
	{Kind: "Ingress", Resource: schema.GroupVersionResource{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}, Namespaced: true},
	{Kind: "HorizontalPodAutoscaler", Resource: schema.GroupVersionResource{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}, Namespaced: true},
	// What a Helm release owns besides its workloads, read by its ownership
	// annotations (helmOwnership).
	{Kind: "ConfigMap", Resource: schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}, Namespaced: true, KeysOnly: true},
	{Kind: "Secret", Resource: schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, Namespaced: true, KeysOnly: true, Fingerprinted: true},
	{Kind: "ServiceAccount", Resource: schema.GroupVersionResource{Version: "v1", Resource: "serviceaccounts"}, Namespaced: true},
	{Kind: "PersistentVolumeClaim", Resource: schema.GroupVersionResource{Version: "v1", Resource: "persistentvolumeclaims"}, Namespaced: true},
	{Kind: "Role", Resource: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "roles"}, Namespaced: true},
	{Kind: "RoleBinding", Resource: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "rolebindings"}, Namespaced: true},
	{Kind: "ClusterRole", Resource: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}},
	{Kind: "ClusterRoleBinding", Resource: schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterrolebindings"}},
	// Custom resources, a fixed list rather than any group/version/kind: a
	// kind is read only where its group is granted, and some custom
	// resources carry credentials.
	{Kind: "ServiceMonitor", Resource: schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "servicemonitors"}, Namespaced: true, Custom: true},
	{Kind: "PodMonitor", Resource: schema.GroupVersionResource{Group: "monitoring.coreos.com", Version: "v1", Resource: "podmonitors"}, Namespaced: true, Custom: true},
	{Kind: "BkLogConfig", Resource: schema.GroupVersionResource{Group: "bk.tencent.com", Version: "v1alpha1", Resource: "bklogconfigs"}, Namespaced: true, Custom: true},
	// bk-monitor-operator's DataID: which business each kind of collected
	// data lands in, the fact the trends derive their space from.
	{Kind: "DataID", Resource: schema.GroupVersionResource{Group: "monitoring.bk.tencent.com", Version: "v1beta1", Resource: "dataids"}, Custom: true},
	// The admission webhooks: which webhook gates which resources, so a
	// dry run's denial can be traced to the webhook that made it.
	{Kind: "ValidatingWebhookConfiguration", Resource: schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "validatingwebhookconfigurations"}, Webhooks: true},
	{Kind: "MutatingWebhookConfiguration", Resource: schema.GroupVersionResource{Group: "admissionregistration.k8s.io", Version: "v1", Resource: "mutatingwebhookconfigurations"}, Webhooks: true},
}

// helmLabel and helmAnnotations are Helm's ownership marks on every object a
// release manages: what "what does this release own" is read by.
const helmLabel = "app.kubernetes.io/managed-by"

var helmAnnotations = []string{"meta.helm.sh/release-name", "meta.helm.sh/release-namespace"}

// ResourceKinds is every kind the read serves, for the operation's schema.
func ResourceKinds() []string {
	kinds := make([]string, 0, len(resourceKinds))
	for _, kind := range resourceKinds {
		kinds = append(kinds, kind.Kind)
	}
	return kinds
}

func lookupKind(value string) (resourceKind, error) {
	name := strings.ToLower(strings.TrimSpace(value))
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
// OversizedItems. A page whose objects do not all fit the output bound is
// listed again at the count that fit (resourceListCuts), so
// it ends where its items end and Continue goes on from there: a walk that
// follows Continue reads every object. Truncated says even that did not
// fit -- the objects grew between the two lists: the rest are omitted,
// Continue is empty, and ReadWith is the token this page was read with, to
// read it again with a smaller limit.
type ResourceReceipt struct {
	Kind        string            `json:"kind"`
	APIVersion  string            `json:"api_version"`
	Namespace   string            `json:"namespace"`
	ClusterWide bool              `json:"cluster_wide"`
	Items       []json.RawMessage `json:"items"`
	Count       int               `json:"count"`
	Continue    string            `json:"continue,omitempty"`
	// RemainingItems is how many objects the apiserver says are past this
	// page, when it says; with Continue set and no count, more pages follow
	// of a size it did not state.
	RemainingItems    *int64   `json:"remaining_items,omitempty"`
	ResourceVersion   string   `json:"resource_version,omitempty"`
	Truncated         bool     `json:"truncated"`
	ItemsOmitted      int      `json:"items_omitted,omitempty"`
	ReadWith          string   `json:"read_with,omitempty"`
	OversizedItems    []string `json:"oversized_items,omitempty"`
	RedactionContract string   `json:"redaction_contract"`
	// FingerprintScope is where a Secret's fingerprints compare; set on a
	// Secret read only.
	FingerprintScope string    `json:"fingerprint_scope,omitempty"`
	At               time.Time `json:"at"`
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
	fingerprints := p.newFingerprints()
	if kind.Fingerprinted {
		result.FingerprintScope = fingerprints.Scope()
	}
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
		if kind.Custom && apierrors.IsNotFound(err) {
			return result, &Error{Code: CodeKindNotInstalled, Message: kind.Kind + " is not installed on this cluster: its CRD " +
				kind.Resource.Resource + "." + kind.Resource.Group + " is absent"}
		}
		if err != nil {
			return result, apiError(err)
		}
		objects, result.Continue, result.ResourceVersion, result.RemainingItems = list.Items, list.GetContinue(), list.GetResourceVersion(), list.GetRemainingItemCount()
	}
	for cuts := 0; ; cuts++ {
		fit, err := p.encodeResources(&result, kind, paths, objects, fingerprints)
		if err != nil || fit == len(objects) {
			return result, err
		}
		if request.Name != "" || cuts == resourceListCuts {
			result.Truncated, result.ItemsOmitted = true, len(objects)-fit
			result.ReadWith, result.Continue = request.Continue, ""
			return result, nil
		}
		// The page again, at the count that fit: the apiserver's own token
		// then resumes after the last item returned.
		list, err := p.options.Resources.ListResources(ctx, kind.Resource, request.Namespace, metav1.ListOptions{
			LabelSelector: request.LabelSelector, FieldSelector: request.FieldSelector, Continue: request.Continue, Limit: int64(fit)})
		if err != nil {
			return result, apiError(err)
		}
		objects, result.Continue, result.ResourceVersion, result.RemainingItems = list.Items, list.GetContinue(), list.GetResourceVersion(), list.GetRemainingItemCount()
		result.Items, result.Count, result.OversizedItems = []json.RawMessage{}, 0, nil
	}
}

// resourceListCuts is how many times a page cut by the output bound is
// listed again at the count that fit: once, which costs the apiserver one
// more LIST of fewer objects than the page. Objects that grew between the
// two lists leave the page truncated, said so.
const resourceListCuts = 1

// encodeResources cleans, projects and encodes objects into the receipt
// while they fit the output bound, and says how many did. The first always
// fits: one alone over the bound is its identity only, named in
// OversizedItems.
func (p *Provider) encodeResources(result *ResourceReceipt, kind resourceKind, paths [][]string, objects []unstructured.Unstructured,
	fingerprints *fingerprints,
) (int, error) {
	remaining := p.options.MaxOutputBytes
	for index, object := range objects {
		raw := object.Object
		if kind.KeysOnly {
			raw = p.keysOnlyRow(raw, kind.Fingerprinted, fingerprints)
		}
		if kind.Webhooks {
			raw = withoutCABundles(raw)
		}
		item := p.cleanObject(raw, fingerprints)
		if len(paths) > 0 {
			item = keepHelmOwnership(project(item, append(append([][]string(nil), identityPaths...), paths...)), item)
		}
		encoded, err := json.Marshal(item)
		if err != nil {
			return index, &Error{Code: CodeAPIError, Message: "a Kubernetes object did not encode"}
		}
		if int64(len(encoded)) > p.options.MaxOutputBytes {
			// Alone over the bound: its identity, so the caller can read it
			// again with paths.
			encoded, _ = json.Marshal(project(item, identityPaths))
			result.OversizedItems = append(result.OversizedItems, object.GetNamespace()+"/"+object.GetName())
		}
		if int64(len(encoded)) > remaining {
			return index, nil
		}
		remaining -= int64(len(encoded))
		result.Items = append(result.Items, encoded)
		result.Count = len(result.Items)
	}
	return len(objects), nil
}

// keysOnlyRow is a ConfigMap or a Secret as listed: its identity, metadata,
// type, and each data and binaryData key with its value's size in bytes and,
// fingerprinted, its value's fingerprint, sorted by key. It is built from
// nothing that holds a value, so neither the whole item nor any projection
// of it can carry one. configmap.get reads ConfigMap values, masked; no read
// returns a Secret value.
func (p *Provider) keysOnlyRow(object map[string]any, fingerprinted bool, fingerprints *fingerprints) map[string]any {
	row := map[string]any{"apiVersion": object["apiVersion"], "kind": object["kind"]}
	if kind, ok := object["type"].(string); ok {
		row["type"] = kind
	}
	if metadata, ok := object["metadata"].(map[string]any); ok {
		kept := map[string]any{}
		for _, key := range []string{"name", "namespace", "uid", "labels", "annotations", "ownerReferences", "creationTimestamp"} {
			if value, present := metadata[key]; present {
				kept[key] = value
			}
		}
		row["metadata"] = kept
	}
	text := func(value string) ([]byte, bool) { return []byte(value), true }
	encoded := func(value string) ([]byte, bool) {
		decoded, err := base64.StdEncoding.DecodeString(value)
		return decoded, err == nil
	}
	data := text
	if fingerprinted {
		// A Secret's data is base64 on the wire, like a ConfigMap's binaryData.
		data = encoded
	}
	row["data"] = keySizes(object["data"], data, fingerprinted, fingerprints)
	if _, present := object["binaryData"]; present || !fingerprinted {
		row["binaryData"] = keySizes(object["binaryData"], encoded, fingerprinted, fingerprints)
	}
	return row
}

func keySizes(value any, decode func(string) ([]byte, bool), fingerprinted bool, fingerprints *fingerprints) []any {
	entries, _ := value.(map[string]any)
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, key := range keys {
		raw, _ := entries[key].(string)
		decoded, ok := decode(raw)
		entry := map[string]any{"key": key, "bytes": len(decoded)}
		if !ok {
			entry["bytes"] = -1
		} else if fingerprinted {
			entry["fingerprint"] = fingerprints.sum(decoded)
		}
		out = append(out, entry)
	}
	return out
}

// keepHelmOwnership puts Helm's ownership label and annotations, where the
// object has them, back on a projected item: a path cannot name a key with
// dots in it, and a release's inventory is read by these on every kind.
func keepHelmOwnership(projected, item map[string]any) map[string]any {
	metadata, _ := item["metadata"].(map[string]any)
	if metadata == nil {
		return projected
	}
	out, _ := projected["metadata"].(map[string]any)
	if out == nil {
		out = map[string]any{}
	}
	keep := func(field string, keys []string) {
		from, _ := metadata[field].(map[string]any)
		for _, key := range keys {
			value, present := from[key]
			if !present {
				continue
			}
			to, _ := out[field].(map[string]any)
			if to == nil {
				to = map[string]any{}
				out[field] = to
			}
			to[key] = value
		}
	}
	keep("labels", []string{helmLabel})
	keep("annotations", helmAnnotations)
	projected["metadata"] = out
	return projected
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
//   - an annotation under a credential-like key, or holding something shaped
//     like a key under any name, masked whole, as configmap.get masks it;
//   - in any env list, the value of an entry whose name is credential-like
//     masked whole;
//   - every string through the output redaction.
//
// References by name - secretKeyRef, configMapKeyRef, envFrom - are left as
// they are on purpose: a Secret's or ConfigMap's name is not its content,
// and it is how a reader finds which one a workload reads.
func (p *Provider) cleanObject(object map[string]any, fingerprints *fingerprints) map[string]any {
	if metadata, ok := object["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
		if annotations, ok := metadata["annotations"].(map[string]any); ok {
			delete(annotations, lastAppliedAnnotation)
			for key, value := range annotations {
				if text, isText := value.(string); isText && text != "" && (credentialKey.MatchString(key) || credentialName(key) || keyMaterial(text)) {
					annotations[key] = "<redacted>"
				}
			}
		}
	}
	cleaned, _ := p.cleanValue(object, fingerprints).(map[string]any)
	return cleaned
}

func (p *Provider) cleanValue(value any, fingerprints *fingerprints) any {
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			if key == "env" {
				maskEnv(item, fingerprints)
			}
			v[key] = p.cleanValue(item, fingerprints)
		}
		return v
	case []any:
		for index := range v {
			v[index] = p.cleanValue(v[index], fingerprints)
		}
		return v
	case string:
		redacted, _ := p.redactor.bounded(v, math.MaxInt64)
		return redacted
	default:
		return v
	}
}

// maskEnv masks an env value under a credential name and puts its
// fingerprint beside it, so a value set in place can be compared with the
// Secret that should hold it.
func maskEnv(value any, fingerprints *fingerprints) {
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
			variable["value"], variable["valueFingerprint"] = "<redacted>", fingerprints.sum([]byte(text))
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

// withoutCABundles is a webhook configuration with every webhook's
// clientConfig.caBundle removed.
func withoutCABundles(object map[string]any) map[string]any {
	copied := (&unstructured.Unstructured{Object: object}).DeepCopy().Object
	webhooks, _ := copied["webhooks"].([]any)
	for _, raw := range webhooks {
		if webhook, ok := raw.(map[string]any); ok {
			if config, ok := webhook["clientConfig"].(map[string]any); ok {
				delete(config, "caBundle")
			}
		}
	}
	return copied
}
