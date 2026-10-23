// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package rollout

import (
	"context"
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8stesting "k8s.io/client-go/testing"
)

// apiserverLike is the fake client answering a patch whose test fails as
// the apiserver does - an Invalid (422) status - rather than with the plain
// error the fake's tracker returns.
func apiserverLike(objects ...runtime.Object) *dynamicfake.FakeDynamicClient {
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), objects...)
	react := k8stesting.ObjectReaction(client.Tracker())
	client.PrependReactor("patch", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		handled, object, err := react(action)
		if err != nil && !apierrors.IsNotFound(err) {
			name := action.(k8stesting.PatchAction).GetName()
			err = apierrors.NewInvalid(schema.GroupKind{Group: "apps", Kind: "Deployment"}, name, field.ErrorList{field.Invalid(field.NewPath("patch"), "", err.Error())})
		}
		return handled, object, err
	})
	return client
}

func fakeDeployment() *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment",
		"metadata": map[string]any{"name": "app", "namespace": "ns", "uid": "uid-1", "generation": int64(4)},
		"spec": map[string]any{"replicas": int64(4), "progressDeadlineSeconds": int64(120),
			"strategy": map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": int64(1), "maxUnavailable": "25%"}},
			"template": map[string]any{"spec": map[string]any{"containers": []any{
				map[string]any{"name": "sidecar", "image": "sidecar:1"},
				map[string]any{"name": "app", "image": "app:1"},
			}}}},
		"status": map[string]any{"observedGeneration": int64(4), "replicas": int64(5), "updatedReplicas": int64(2), "availableReplicas": int64(3),
			"conditions": []any{map[string]any{"type": "Progressing", "status": "True", "reason": "ReplicaSetUpdated", "lastUpdateTime": "2026-10-12T08:00:00Z"}}},
	}}
}

// A Deployment reads as the rollout judges it: generations, counts, the
// progress deadline, the Progressing condition, the containers in order and
// the strategy resolved as the controller rounds it (surge up,
// unavailability down: 25% of 4 is 1).
func TestADeploymentReadsAsTheRolloutJudgesIt(t *testing.T) {
	cluster := NewKubernetesCluster(dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), fakeDeployment()))
	dep, err := cluster.GetDeployment(context.Background(), "ns", "app")
	if err != nil {
		t.Fatal(err)
	}
	if dep.UID != "uid-1" || dep.Generation != 4 || dep.ObservedGeneration != 4 || dep.Replicas != 4 || dep.StatusReplicas != 5 ||
		dep.Updated != 2 || dep.Available != 3 || dep.ProgressDeadlineSeconds != 120 || dep.Strategy != "RollingUpdate" ||
		dep.MaxSurge != 1 || dep.MaxUnavailable != 1 || dep.Progressing.Reason != "ReplicaSetUpdated" ||
		dep.Progressing.LastUpdate != "2026-10-12T08:00:00Z" || len(dep.Containers) != 2 || dep.Containers[1] != (Container{"app", "app:1"}) {
		t.Fatalf("deployment %+v", dep)
	}
	if _, err := cluster.GetDeployment(context.Background(), "ns", "missing"); !errors.Is(err, ErrDeploymentGone) {
		t.Fatalf("a missing deployment = %v", err)
	}
}

// The image write names the container by index and tests what it expects:
// a matching write lands; one whose expected image, container or UID is not
// the Deployment's is refused without writing.
func TestTheImageWriteTestsWhatItExpects(t *testing.T) {
	cluster := NewKubernetesCluster(apiserverLike(fakeDeployment()))
	ctx := context.Background()
	for _, refused := range []struct {
		name                string
		uid                 string
		index               int
		container, expected string
	}{
		{"another image", "uid-1", 1, "app", "app:0"},
		{"another container at that index", "uid-1", 0, "app", "app:1"},
		{"another deployment", "uid-2", 1, "app", "app:1"},
	} {
		if _, err := cluster.SetImage(ctx, "ns", "app", refused.uid, refused.index, refused.container, refused.expected, "app:2"); !errors.Is(err, ErrImageChanged) {
			t.Fatalf("%s: %v, want refused", refused.name, err)
		}
	}
	if dep, _ := cluster.GetDeployment(ctx, "ns", "app"); dep.Containers[1].Image != "app:1" {
		t.Fatalf("a refused write changed the image: %+v", dep.Containers)
	}
	if _, err := cluster.SetImage(ctx, "ns", "app", "uid-1", 1, "app", "app:1", "app:2"); err != nil {
		t.Fatal(err)
	}
	if dep, _ := cluster.GetDeployment(ctx, "ns", "app"); dep.Containers[1].Image != "app:2" || dep.Containers[0].Image != "sidecar:1" {
		t.Fatalf("after the write: %+v", dep.Containers)
	}
	if _, err := cluster.SetImage(ctx, "ns", "missing", "uid-1", 1, "app", "app:1", "app:2"); !errors.Is(err, ErrDeploymentGone) {
		t.Fatalf("a write to a missing deployment = %v", err)
	}
}

// The apiserver refusing the service account reads as ErrForbidden.
func TestAForbiddenCallReadsAsForbidden(t *testing.T) {
	client := dynamicfake.NewSimpleDynamicClient(runtime.NewScheme(), fakeDeployment())
	client.PrependReactor("*", "deployments", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Group: "apps", Resource: "deployments"}, "app", errors.New("not allowed"))
	})
	cluster := NewKubernetesCluster(client)
	if _, err := cluster.GetDeployment(context.Background(), "ns", "app"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a forbidden read = %v", err)
	}
	if _, err := cluster.SetImage(context.Background(), "ns", "app", "uid-1", 1, "app", "app:1", "app:2"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("a forbidden write = %v", err)
	}
}
