// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	typedcore "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kubeclient"
)

type KubernetesClient struct {
	core   typedcore.CoreV1Interface
	owners dynamic.Interface
	// mapper resolves a kind to its resource from the cluster's discovery,
	// cached; a kind it does not know resets the cache once, for a CRD
	// installed since.
	mapper *restmapper.DeferredDiscoveryRESTMapper
}

// serviceAccountNamespace is where the kubelet writes the Pod's own namespace.
const serviceAccountNamespace = "/var/run/secrets/kubernetes.io/serviceaccount/namespace"

// NewInCluster constructs the production adapters from the ServiceAccount.
// Availability errors can be reported by the channel without blocking its auth
// lifecycle. No Pod, logs, execution or list requests are made during
// construction: a client and its connection pool, no informer. The default
// namespace is the Pod's own unless options name one.
func NewInCluster(options Options) (*Provider, error) {
	if options.DefaultNamespace == "" {
		if own, err := os.ReadFile(serviceAccountNamespace); err == nil {
			options.DefaultNamespace = strings.TrimSpace(string(own))
		}
	}
	config, err := kubeclient.InCluster()
	if err != nil {
		return nil, &Error{Code: "service_account_not_mounted", Message: "in-cluster configuration is unavailable"}
	}
	client, err := NewKubernetesClient(config)
	if err != nil {
		return nil, err
	}
	executor, err := NewKubernetesExecutor(config)
	if err != nil {
		return nil, err
	}
	options.Client = client
	options.Manager = client
	options.Secrets = client
	options.ConfigMaps = client
	options.Resources = client
	options.DryRun = client
	options.Writer = client
	options.Executor = executor
	return New(options)
}

// NewKubernetesClient uses the server's ServiceAccount when config is nil.
// Callers may share a rest.InClusterConfig result with the exec constructor.
func NewKubernetesClient(config *rest.Config) (*KubernetesClient, error) {
	if config == nil {
		var err error
		config, err = kubeclient.InCluster()
		if err != nil {
			return nil, &Error{Code: "service_account_not_mounted", Message: "in-cluster configuration is unavailable"}
		}
	}
	config = kubeclient.WithRate(config)
	core, err := typedcore.NewForConfig(rest.CopyConfig(config))
	if err != nil {
		return nil, apiError(err)
	}
	owners, err := dynamic.NewForConfig(rest.CopyConfig(config))
	if err != nil {
		return nil, apiError(err)
	}
	discovered, err := discovery.NewDiscoveryClientForConfig(rest.CopyConfig(config))
	if err != nil {
		return nil, apiError(err)
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(discovered))
	return &KubernetesClient{core: core, owners: owners, mapper: mapper}, nil
}
func (c *KubernetesClient) ListPods(ctx context.Context, namespace string, options metav1.ListOptions) (*corev1.PodList, error) {
	return c.core.Pods(namespace).List(ctx, options)
}
func (c *KubernetesClient) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	return c.core.Pods(namespace).Get(ctx, name, metav1.GetOptions{})
}
func (c *KubernetesClient) OpenLogs(ctx context.Context, namespace, name string, options corev1.PodLogOptions) (io.ReadCloser, error) {
	return c.core.Pods(namespace).GetLogs(name, &options).Stream(ctx)
}

var ownerResources = map[string]schema.GroupVersionResource{
	"ReplicaSet":  {Group: "apps", Version: "v1", Resource: "replicasets"},
	"Deployment":  {Group: "apps", Version: "v1", Resource: "deployments"},
	"StatefulSet": {Group: "apps", Version: "v1", Resource: "statefulsets"},
	"DaemonSet":   {Group: "apps", Version: "v1", Resource: "daemonsets"},
	"Job":         {Group: "batch", Version: "v1", Resource: "jobs"},
	"CronJob":     {Group: "batch", Version: "v1", Resource: "cronjobs"},
}

func (c *KubernetesClient) GetOwner(ctx context.Context, namespace string, ref metav1.OwnerReference) (OwnerResource, error) {
	resource, ok := ownerResources[ref.Kind]
	if !ok {
		return OwnerResource{}, denied("unsupported owner resource")
	}
	if ref.APIVersion != resource.Group+"/"+resource.Version {
		return OwnerResource{}, denied("unsupported owner API version")
	}
	obj, err := c.owners.Resource(resource).Namespace(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return OwnerResource{}, err
	}
	result := OwnerResource{Kind: ref.Kind, Namespace: obj.GetNamespace(), Name: obj.GetName(), UID: string(obj.GetUID()), Labels: obj.GetLabels(), Owners: obj.GetOwnerReferences()}
	selector, found, err := unstructured.NestedMap(obj.Object, "spec", "selector")
	if err != nil {
		return OwnerResource{}, err
	}
	if found {
		result.Selector = &metav1.LabelSelector{}
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(selector, result.Selector); err != nil {
			return OwnerResource{}, err
		}
	}
	if replicas, found, err := unstructured.NestedInt64(obj.Object, "spec", "replicas"); err == nil && found {
		count := int32(replicas)
		result.Replicas = &count
	}
	return result, nil
}

// ListResources lists one page of one kind, across the cluster when
// namespace is empty.
func (c *KubernetesClient) ListResources(ctx context.Context, resource schema.GroupVersionResource, namespace string, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	return c.owners.Resource(resource).Namespace(namespace).List(ctx, options)
}

// GetResource reads one object of one kind.
func (c *KubernetesClient) GetResource(ctx context.Context, resource schema.GroupVersionResource, namespace, name string) (*unstructured.Unstructured, error) {
	return c.owners.Resource(resource).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
}

// GetConfigMap reads one ConfigMap by name.
func (c *KubernetesClient) GetConfigMap(ctx context.Context, namespace, name string) (*corev1.ConfigMap, error) {
	return c.core.ConfigMaps(namespace).Get(ctx, name, metav1.GetOptions{})
}

// GetSecret reads one Secret by name.
func (c *KubernetesClient) GetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	return c.core.Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
}

// DeletePod deletes one Pod with its UID as a precondition: a Pod recreated
// under the same name since the caller saw it is not deleted.
func (c *KubernetesClient) DeletePod(ctx context.Context, namespace, name, uid string) error {
	precondition := types.UID(uid)
	return c.core.Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &precondition}})
}

// GetWorkload reads one workload as an owner of its kind is read.
func (c *KubernetesClient) GetWorkload(ctx context.Context, namespace, kind, name string) (OwnerResource, error) {
	resource, ok := ownerResources[kind]
	if !ok {
		return OwnerResource{}, denied("unsupported workload kind")
	}
	return c.GetOwner(ctx, namespace, metav1.OwnerReference{APIVersion: resource.Group + "/" + resource.Version, Kind: kind, Name: name})
}

// PatchWorkload applies one JSON merge patch to a workload. The patch carries
// metadata.uid, which the apiserver checks against the live object.
func (c *KubernetesClient) PatchWorkload(ctx context.Context, namespace, kind, name string, patch []byte) error {
	resource, ok := ownerResources[kind]
	if !ok {
		return denied("unsupported workload kind")
	}
	_, err := c.owners.Resource(resource).Namespace(namespace).Patch(ctx, name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

type KubernetesExecutor struct {
	config     *rest.Config
	core       typedcore.CoreV1Interface
	parameters runtime.ParameterCodec
}

func NewKubernetesExecutor(config *rest.Config) (*KubernetesExecutor, error) {
	if config == nil {
		var err error
		config, err = kubeclient.InCluster()
		if err != nil {
			return nil, &Error{Code: "service_account_not_mounted", Message: "in-cluster configuration is unavailable"}
		}
	}
	copyConfig := kubeclient.WithRate(config)
	core, err := typedcore.NewForConfig(copyConfig)
	if err != nil {
		return nil, apiError(err)
	}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, apiError(err)
	}
	return &KubernetesExecutor{config: copyConfig, core: core, parameters: runtime.NewParameterCodec(scheme)}, nil
}

// Stream opens exactly one standard Kubernetes SPDY exec connection. There is
// no fallback or reconnect that could execute the submitted command twice.
func (e *KubernetesExecutor) Stream(ctx context.Context, request StreamRequest) (StreamResult, error) {
	url := e.core.RESTClient().Post().Resource("pods").Namespace(request.Target.Namespace).Name(request.Target.Pod).SubResource("exec").VersionedParams(&corev1.PodExecOptions{Container: request.Target.Container, Command: request.Argv, Stdin: request.Stdin != nil, Stdout: true, Stderr: true, TTY: false}, e.parameters).URL()
	executor, err := remotecommand.NewSPDYExecutor(e.config, http.MethodPost, url)
	if err != nil {
		return StreamResult{}, err
	}
	err = executor.StreamWithContext(ctx, remotecommand.StreamOptions{Stdin: request.Stdin, Stdout: request.Stdout, Stderr: request.Stderr, Tty: false})
	if err == nil {
		code := 0
		return StreamResult{ExitCode: &code}, nil
	}
	var exit utilexec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitStatus()
		return StreamResult{ExitCode: &code}, nil
	}
	if startRefused(err, request.Argv[0]) {
		return StreamResult{StartRefused: true}, nil
	}
	return StreamResult{}, err
}

// startRefused reads the runtime's refusal to start the exec's program, as
// containerd and runc word it ("exec: \"/bin/sh\": stat /bin/sh: no such file
// or directory", "executable file not found in $PATH", "permission denied"),
// naming the program: the process was never created, so nothing ran. Read
// only here; the text is never returned.
func startRefused(err error, program string) bool {
	// Matched as containerd quotes it (exec: "/bin/sh": ...): a short argv[0]
	// such as "ls" appears in unrelated error text unquoted.
	text := err.Error()
	if program == "" || !strings.Contains(text, `"`+program+`"`) {
		return false
	}
	for _, refusal := range []string{"no such file or directory", "executable file not found", "permission denied"} {
		if strings.Contains(text, refusal) {
			return true
		}
	}
	return false
}

// Mapping resolves apiVersion and kind to the resource the cluster serves
// them under and whether it is namespaced.
func (c *KubernetesClient) Mapping(apiVersion, kind string) (schema.GroupVersionResource, bool, error) {
	version, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return schema.GroupVersionResource{}, false, err
	}
	gk := schema.GroupKind{Group: version.Group, Kind: kind}
	mapping, err := c.mapper.RESTMapping(gk, version.Version)
	if meta.IsNoMatchError(err) {
		c.mapper.Reset()
		mapping, err = c.mapper.RESTMapping(gk, version.Version)
	}
	if err != nil {
		return schema.GroupVersionResource{}, false, err
	}
	return mapping.Resource, mapping.Scope.Name() == meta.RESTScopeNameNamespace, nil
}

// ApplyDryRun is the dry run's one write: a server-side apply that the
// apiserver runs through admission and does not persist. dryRunPatchOptions
// is fixed here; no caller reaches it.
func (c *KubernetesClient) ApplyDryRun(ctx context.Context, resource schema.GroupVersionResource, namespace, name string, data []byte) (*unstructured.Unstructured, error) {
	return c.owners.Resource(resource).Namespace(namespace).Patch(ctx, name, types.ApplyPatchType, data, dryRunPatchOptions())
}

// Apply is k8s.apply's write: a server-side apply under the channel's field
// manager, persisted; writePatchOptions is fixed here.
func (c *KubernetesClient) Apply(ctx context.Context, resource schema.GroupVersionResource, namespace, name string, data []byte, force bool) (*unstructured.Unstructured, error) {
	return c.owners.Resource(resource).Namespace(namespace).Patch(ctx, name, types.ApplyPatchType, data, writePatchOptions(force))
}

// JSONPatch sends an RFC 6902 patch as alarmd-sre; the apiserver applies it
// whole or not at all.
func (c *KubernetesClient) JSONPatch(ctx context.Context, resource schema.GroupVersionResource, namespace, name string, patch []byte) (*unstructured.Unstructured, error) {
	return c.owners.Resource(resource).Namespace(namespace).Patch(ctx, name, types.JSONPatchType, patch, metav1.PatchOptions{FieldManager: applyFieldManager})
}

// Delete is k8s.delete's write: only the object the uid names, dependents in
// the background; deleteOptions is fixed here.
func (c *KubernetesClient) Delete(ctx context.Context, resource schema.GroupVersionResource, namespace, name string, uid types.UID, resourceVersion string) error {
	return c.owners.Resource(resource).Namespace(namespace).Delete(ctx, name, deleteOptions(uid, resourceVersion))
}
