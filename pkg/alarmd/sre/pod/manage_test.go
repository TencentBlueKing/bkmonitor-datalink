// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func managed(t *testing.T) (*Provider, *fixtureClient, Target) {
	t.Helper()
	client, target := fixture()
	return mustProvider(t, Options{Client: client, Manager: client}), client, target
}

// A Pod is deleted with its UID as the precondition, so its controller
// recreates it; the receipt says what was deleted.
func TestDeletePodCarriesItsUID(t *testing.T) {
	provider, client, target := managed(t)
	receipt, err := provider.DeletePod(context.Background(), DeleteRequest{Target: target, OwnerUID: "rs-uid"})
	if err != nil || !receipt.Applied || receipt.Op != OpDeletePod || receipt.UID != "pod-uid" {
		t.Fatalf("delete: %+v err=%v", receipt, err)
	}
	if len(client.deleted) != 1 || client.deleted[0] != "tenant/web-abc@pod-uid" {
		t.Fatalf("deleted %v, want the pod with its UID", client.deleted)
	}
}

// The Pod by that name is not deleted when it is no longer the one the caller
// saw: restarted under the same name (another UID), or now owned by another
// controller - a Deployment that rolled to a new ReplicaSet between the read
// and the delete. A replacement the apiserver catches at the write (409) is
// the same answer.
func TestDeletePodRefusesAReplacedTarget(t *testing.T) {
	for name, change := range map[string]func(*fixtureClient, *DeleteRequest){
		"restarted under the same name": func(c *fixtureClient, r *DeleteRequest) { c.pod.UID = "new-pod-uid" },
		"rolled to another ReplicaSet": func(c *fixtureClient, r *DeleteRequest) {
			c.pod.OwnerReferences = []metav1.OwnerReference{controllerRef("ReplicaSet", "web-rs-2", "rs-2-uid")}
		},
		"replaced before the write": func(c *fixtureClient, r *DeleteRequest) {
			c.writeError = apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "web-abc", nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			provider, client, target := managed(t)
			request := DeleteRequest{Target: target, OwnerUID: "rs-uid"}
			change(client, &request)
			receipt, err := provider.DeletePod(context.Background(), request)
			requireCode(t, err, CodeTargetChanged)
			if receipt.Applied || len(client.deleted) != 0 {
				t.Fatalf("deleted a replaced target: %+v %v", receipt, client.deleted)
			}
		})
	}
}

// A restart stamps the Pod template with the time, as kubectl rollout
// restart does, and the patch carries the workload's UID for the apiserver
// to check; a scale sets replicas and says what they were.
func TestRestartAndScaleWriteThroughTheWorkloadsUID(t *testing.T) {
	provider, client, _ := managed(t)
	restart, err := provider.RestartWorkload(context.Background(), WorkloadRequest{Namespace: "tenant", Kind: "Deployment", Name: "web", UID: "deployment-uid"})
	if err != nil || !restart.Applied || restart.RestartedAt == "" {
		t.Fatalf("restart: %+v err=%v", restart, err)
	}
	var patch map[string]map[string]any
	if len(client.patches) != 1 || json.Unmarshal([]byte(client.patches[0][strings.Index(client.patches[0], " ")+1:]), &patch) != nil ||
		patch["metadata"]["uid"] != "deployment-uid" || !strings.Contains(client.patches[0], RestartedAtAnnotation) {
		t.Fatalf("restart patch %v", client.patches)
	}
	owner := client.owners["web"]
	three := int32(3)
	owner.Replicas = &three
	client.owners["web"] = owner
	five := int32(5)
	scale, err := provider.ScaleWorkload(context.Background(), WorkloadRequest{Namespace: "tenant", Kind: "Deployment", Name: "web", UID: "deployment-uid", Replicas: &five})
	if err != nil || !scale.Applied || scale.PreviousReplicas == nil || *scale.PreviousReplicas != 3 || scale.Replicas == nil || *scale.Replicas != 5 {
		t.Fatalf("scale: %+v err=%v", scale, err)
	}
	if !strings.Contains(client.patches[1], `"replicas":5`) || !strings.Contains(client.patches[1], `"uid":"deployment-uid"`) {
		t.Fatalf("scale patch %q", client.patches[1])
	}
}

// A workload read under another UID than the caller saw is not written; nor
// is a kind the action does not apply to, or a count out of bounds.
func TestWorkloadActionsRefuseWhatTheyDoNotApplyTo(t *testing.T) {
	provider, client, _ := managed(t)
	_, err := provider.RestartWorkload(context.Background(), WorkloadRequest{Namespace: "tenant", Kind: "Deployment", Name: "web", UID: "old-uid"})
	requireCode(t, err, CodeTargetChanged)
	_, err = provider.RestartWorkload(context.Background(), WorkloadRequest{Namespace: "tenant", Kind: "Job", Name: "web", UID: "deployment-uid"})
	requireCode(t, err, CodeInvalidRequest)
	_, err = provider.ScaleWorkload(context.Background(), WorkloadRequest{Namespace: "tenant", Kind: "DaemonSet", Name: "web", UID: "deployment-uid", Replicas: new(int32)})
	requireCode(t, err, CodeInvalidRequest)
	tooMany := int32(MaxReplicas + 1)
	_, err = provider.ScaleWorkload(context.Background(), WorkloadRequest{Namespace: "tenant", Kind: "Deployment", Name: "web", UID: "deployment-uid", Replicas: &tooMany})
	requireCode(t, err, CodeInvalidRequest)
	if len(client.patches) != 0 {
		t.Fatalf("patched %v", client.patches)
	}
}
