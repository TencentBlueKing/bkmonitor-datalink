// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"errors"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"
)

// RestartedAtAnnotation is the Pod template annotation a rolling restart
// sets, the same one `kubectl rollout restart` sets.
const RestartedAtAnnotation = "kubectl.kubernetes.io/restartedAt"

// MaxReplicas bounds a scale request.
const MaxReplicas = 1000

// The management operations, as a receipt names them.
const (
	OpDeletePod       = "pod.delete"
	OpRestartWorkload = "workload.restart"
	OpScaleWorkload   = "workload.scale"
)

// DeletePod deletes one Pod for its controller to recreate. It is refused,
// as target_changed, when the Pod by that name is no longer the one the
// caller saw - another UID, or another controller than OwnerUID - and the
// deletion itself carries the UID as a precondition, so a replacement made
// between the read and the delete is refused by the apiserver as well.
func (p *Provider) DeletePod(ctx context.Context, request DeleteRequest) (ActionReceipt, error) {
	target := request.Target
	result := ActionReceipt{Op: OpDeletePod, Namespace: target.Namespace, Kind: "Pod", Name: target.Pod, UID: target.UID, At: p.options.Now().UTC()}
	if p.options.Manager == nil {
		return result, &Error{Code: CodeAPIError, Message: "Kubernetes management adapter is unavailable"}
	}
	if len(validation.IsDNS1123Label(target.Namespace)) != 0 || len(validation.IsDNS1123Subdomain(target.Pod)) != 0 || target.UID == "" {
		return result, invalid("namespace, pod and the pod UID from discovery are required")
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return result, err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	live, err := p.options.Client.GetPod(ctx, target.Namespace, target.Pod)
	if err != nil {
		return result, apiError(err)
	}
	if string(live.UID) != target.UID {
		return result, &Error{Code: CodeTargetChanged, Message: "the pod by this name is not the one discovered"}
	}
	if request.OwnerUID != "" {
		owner, err := controller(live.OwnerReferences)
		if err != nil || string(owner.UID) != request.OwnerUID {
			return result, &Error{Code: CodeTargetChanged, Message: "the pod is no longer owned by the controller discovered"}
		}
	}
	if err := p.options.Manager.DeletePod(ctx, target.Namespace, target.Pod, target.UID); err != nil {
		return result, writeError(err)
	}
	result.Applied = true
	return result, nil
}

// RestartWorkload rolls a Deployment, StatefulSet or DaemonSet by stamping
// its Pod template, as `kubectl rollout restart` does.
func (p *Provider) RestartWorkload(ctx context.Context, request WorkloadRequest) (ActionReceipt, error) {
	result := ActionReceipt{Op: OpRestartWorkload, Namespace: request.Namespace, Kind: request.Kind, Name: request.Name, UID: request.UID, At: p.options.Now().UTC()}
	if request.Kind != "Deployment" && request.Kind != "StatefulSet" && request.Kind != "DaemonSet" {
		return result, invalid("a restart is for a Deployment, StatefulSet or DaemonSet")
	}
	restartedAt := result.At.Format("2006-01-02T15:04:05Z07:00")
	patch := map[string]any{"metadata": map[string]any{"uid": request.UID},
		"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{RestartedAtAnnotation: restartedAt}}}}}
	if err := p.patchWorkload(ctx, request, &result, patch); err != nil {
		return result, err
	}
	result.RestartedAt = restartedAt
	return result, nil
}

// ScaleWorkload sets a Deployment's or StatefulSet's replicas, and says what
// they were.
func (p *Provider) ScaleWorkload(ctx context.Context, request WorkloadRequest) (ActionReceipt, error) {
	result := ActionReceipt{Op: OpScaleWorkload, Namespace: request.Namespace, Kind: request.Kind, Name: request.Name, UID: request.UID, At: p.options.Now().UTC()}
	if request.Kind != "Deployment" && request.Kind != "StatefulSet" {
		return result, invalid("a scale is for a Deployment or StatefulSet")
	}
	if request.Replicas == nil || *request.Replicas < 0 || *request.Replicas > MaxReplicas {
		return result, invalid("replicas must be between 0 and 1000")
	}
	patch := map[string]any{"metadata": map[string]any{"uid": request.UID}, "spec": map[string]any{"replicas": *request.Replicas}}
	if err := p.patchWorkload(ctx, request, &result, patch); err != nil {
		return result, err
	}
	replicas := *request.Replicas
	result.Replicas = &replicas
	return result, nil
}

// patchWorkload reads the workload, refuses a replaced one, and applies the
// patch, whose metadata.uid the apiserver checks again at the write.
func (p *Provider) patchWorkload(ctx context.Context, request WorkloadRequest, result *ActionReceipt, patch map[string]any) error {
	if p.options.Manager == nil {
		return &Error{Code: CodeAPIError, Message: "Kubernetes management adapter is unavailable"}
	}
	if len(validation.IsDNS1123Label(request.Namespace)) != 0 || len(validation.IsDNS1123Subdomain(request.Name)) != 0 || request.UID == "" {
		return invalid("namespace, name and the workload UID from discovery are required")
	}
	release, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	ctx, cancel := p.readContext(ctx)
	defer cancel()
	live, err := p.options.Manager.GetWorkload(ctx, request.Namespace, request.Kind, request.Name)
	if err != nil {
		return apiError(err)
	}
	if live.UID != request.UID {
		return &Error{Code: CodeTargetChanged, Message: "the workload by this name is not the one discovered"}
	}
	result.PreviousReplicas = live.Replicas
	body, _ := json.Marshal(patch)
	if err := p.options.Manager.PatchWorkload(ctx, request.Namespace, request.Kind, request.Name, body); err != nil {
		return writeError(err)
	}
	result.Applied = true
	return nil
}

// writeError names a refused write. A precondition the apiserver refused
// (409) is the object having been replaced since it was read.
func writeError(err error) error {
	var named *Error
	if errors.As(err, &named) {
		return named
	}
	if apierrors.IsConflict(err) {
		return &Error{Code: CodeTargetChanged, Message: "the object was replaced before the write"}
	}
	return apiError(err)
}
