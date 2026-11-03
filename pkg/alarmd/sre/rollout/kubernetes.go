// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package rollout

import (
	"context"
	"encoding/json"
	"strconv"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/dynamic"
)

var deployments = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}

// KubernetesCluster reads and writes Deployments through the apiserver,
// with the get and patch the SRE channel's role already grants.
type KubernetesCluster struct {
	client dynamic.Interface
}

// NewKubernetesCluster uses client for Deployments.
func NewKubernetesCluster(client dynamic.Interface) *KubernetesCluster {
	return &KubernetesCluster{client: client}
}

// GetDeployment reads one Deployment.
func (c *KubernetesCluster) GetDeployment(ctx context.Context, namespace, name string) (Deployment, error) {
	object, err := c.client.Resource(deployments).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return Deployment{}, ErrDeploymentGone
	}
	if apierrors.IsForbidden(err) {
		return Deployment{}, ErrForbidden
	}
	if err != nil {
		return Deployment{}, err
	}
	return deploymentOf(object), nil
}

// SetImage writes image in one JSON Patch that first tests the UID, the
// container's name and its image. A failed test is refused by the apiserver
// as invalid (422) or, by some versions, as a conflict (409); both are
// ErrImageChanged here, the patch being well formed by construction.
func (c *KubernetesCluster) SetImage(ctx context.Context, namespace, name, uid string, index int, container, expected, image string) (int64, error) {
	at := "/spec/template/spec/containers/" + strconv.Itoa(index)
	patch, err := json.Marshal([]map[string]any{
		{"op": "test", "path": "/metadata/uid", "value": uid},
		{"op": "test", "path": at + "/name", "value": container},
		{"op": "test", "path": at + "/image", "value": expected},
		{"op": "replace", "path": at + "/image", "value": image},
	})
	if err != nil {
		return 0, err
	}
	object, err := c.client.Resource(deployments).Namespace(namespace).Patch(ctx, name, types.JSONPatchType, patch, metav1.PatchOptions{})
	if apierrors.IsNotFound(err) {
		return 0, ErrDeploymentGone
	}
	if apierrors.IsForbidden(err) {
		return 0, ErrForbidden
	}
	if apierrors.IsInvalid(err) || apierrors.IsConflict(err) {
		return 0, ErrImageChanged
	}
	if err != nil {
		return 0, err
	}
	return object.GetGeneration(), nil
}

func deploymentOf(object *unstructured.Unstructured) Deployment {
	content := object.UnstructuredContent()
	integer := func(fields ...string) int64 {
		value, _, _ := unstructured.NestedInt64(content, fields...)
		return value
	}
	dep := Deployment{UID: string(object.GetUID()), Generation: object.GetGeneration(),
		ObservedGeneration: integer("status", "observedGeneration"), Replicas: 1,
		StatusReplicas: int32(integer("status", "replicas")), Updated: int32(integer("status", "updatedReplicas")),
		Ready:     int32(integer("status", "readyReplicas")),
		Available: int32(integer("status", "availableReplicas")), ProgressDeadlineSeconds: 600, Strategy: "RollingUpdate"}
	if replicas, found, _ := unstructured.NestedInt64(content, "spec", "replicas"); found {
		dep.Replicas = int32(replicas)
	}
	if deadline, found, _ := unstructured.NestedInt64(content, "spec", "progressDeadlineSeconds"); found {
		dep.ProgressDeadlineSeconds = int32(deadline)
	}
	dep.Paused, _, _ = unstructured.NestedBool(content, "spec", "paused")
	if strategy, found, _ := unstructured.NestedString(content, "spec", "strategy", "type"); found && strategy != "" {
		dep.Strategy = strategy
	}
	// The Kubernetes defaults are 25% each; the controller rounds surge up
	// and unavailability down.
	surge, unavailable := intstr.FromString("25%"), intstr.FromString("25%")
	if value, found, _ := unstructured.NestedFieldNoCopy(content, "spec", "strategy", "rollingUpdate", "maxSurge"); found {
		surge = intOrStringOf(value)
	}
	if value, found, _ := unstructured.NestedFieldNoCopy(content, "spec", "strategy", "rollingUpdate", "maxUnavailable"); found {
		unavailable = intOrStringOf(value)
	}
	if dep.Strategy == "RollingUpdate" {
		if value, err := intstr.GetScaledValueFromIntOrPercent(&surge, int(dep.Replicas), true); err == nil {
			dep.MaxSurge = int32(value)
		}
		if value, err := intstr.GetScaledValueFromIntOrPercent(&unavailable, int(dep.Replicas), false); err == nil {
			dep.MaxUnavailable = int32(value)
		}
	}
	conditions, _, _ := unstructured.NestedSlice(content, "status", "conditions")
	for _, item := range conditions {
		condition, _ := item.(map[string]any)
		if condition["type"] != "Progressing" {
			continue
		}
		dep.Progressing.Status, _ = condition["status"].(string)
		dep.Progressing.Reason, _ = condition["reason"].(string)
		dep.Progressing.LastUpdate, _ = condition["lastUpdateTime"].(string)
	}
	containers, _, _ := unstructured.NestedSlice(content, "spec", "template", "spec", "containers")
	for _, item := range containers {
		container, _ := item.(map[string]any)
		name, _ := container["name"].(string)
		image, _ := container["image"].(string)
		dep.Containers = append(dep.Containers, Container{Name: name, Image: image})
	}
	return dep
}

func intOrStringOf(value any) intstr.IntOrString {
	switch v := value.(type) {
	case string:
		return intstr.FromString(v)
	case int64:
		return intstr.FromInt32(int32(v))
	case float64:
		return intstr.FromInt32(int32(v))
	}
	return intstr.FromInt32(0)
}
