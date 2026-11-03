// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

// scalePods is a namespace of n single-replica Deployments, as a test
// environment runs them: n Pods, n ReplicaSets, n Deployments.
func scalePods(n int) []corev1.Pod {
	pods := make([]corev1.Pod, 0, n)
	for i := 0; i < n; i++ {
		app := fmt.Sprintf("app-%03d", i)
		pods = append(pods, corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: app + "-5d4f8-x", UID: types.UID("pod-" + app), Labels: map[string]string{"app": app},
				OwnerReferences: []metav1.OwnerReference{controllerRef("ReplicaSet", app+"-5d4f8", "rs-"+app)}},
			Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "main", Image: "img:1"}}},
			Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "main", ContainerID: "containerd://" + app, Ready: true}}},
		})
	}
	return pods
}

// scaleOwner answers an owner read for scalePods: a ReplicaSet named
// <app>-5d4f8 owned by the Deployment <app>.
func scaleOwner(kind, name string) (OwnerResource, bool) {
	selector := func(app string) *metav1.LabelSelector {
		return &metav1.LabelSelector{MatchLabels: map[string]string{"app": app}}
	}
	switch kind {
	case "ReplicaSet":
		app, ok := strings.CutSuffix(name, "-5d4f8")
		if !ok {
			return OwnerResource{}, false
		}
		return OwnerResource{Kind: kind, Namespace: "ns1", Name: name, UID: "rs-" + app, Labels: map[string]string{"app": app},
			Owners: []metav1.OwnerReference{controllerRef("Deployment", app, "deploy-"+app)}, Selector: selector(app)}, true
	case "Deployment":
		return OwnerResource{Kind: kind, Namespace: "ns1", Name: name, UID: "deploy-" + name, Labels: map[string]string{"app": name}, Selector: selector(name)}, true
	}
	return OwnerResource{}, false
}

// A discovery over a hundred Pods with a hundred owners each reads two
// owners per Pod through the production client. At client-go's default of
// 5 requests a second that is some 40 seconds, past the operation's own
// deadline; the client's rate is generous, so it is well inside it.
func TestDiscoverOverAHundredOwnersFinishesInsideTheDeadline(t *testing.T) {
	pods := scalePods(100)
	var mu sync.Mutex
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/namespaces/ns1/pods" {
			_ = json.NewEncoder(w).Encode(corev1.PodList{TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "PodList"}, Items: pods})
			return
		}
		parts := strings.Split(r.URL.Path, "/")
		// /apis/apps/v1/namespaces/ns1/<resource>/<name>
		if len(parts) != 8 || parts[4] != "namespaces" || parts[5] != "ns1" {
			http.NotFound(w, r)
			return
		}
		kind := map[string]string{"replicasets": "ReplicaSet", "deployments": "Deployment"}[parts[6]]
		owner, ok := scaleOwner(kind, parts[7])
		if !ok {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		gets++
		mu.Unlock()
		object := map[string]any{"apiVersion": "apps/v1", "kind": kind, "metadata": map[string]any{"name": owner.Name, "namespace": owner.Namespace, "uid": owner.UID, "labels": owner.Labels, "ownerReferences": owner.Owners},
			"spec": map[string]any{"selector": owner.Selector}}
		_ = json.NewEncoder(w).Encode(object)
	}))
	defer server.Close()
	client, err := NewKubernetesClient(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	provider := mustProvider(t, Options{Client: client})
	// The operation's default deadline is 30 s; the read must take a small
	// part of it.
	ctx, cancel := context.WithCancel(testwait.Context(t))
	defer cancel()
	started := time.Now()
	result, err := provider.Discover(ctx, DiscoverRequest{Namespace: "ns1"})
	if err != nil {
		t.Fatalf("discovery over 100 Pods failed after %v: %v", time.Since(started), err)
	}
	if len(result.Targets) != 100 || result.Rejected != 0 {
		t.Fatalf("targets=%d rejected=%d, want 100 and 0", len(result.Targets), result.Rejected)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("discovery took %v", elapsed)
	}
	if gets != 200 {
		t.Fatalf("%d owner reads, want 200: two per single-replica Pod", gets)
	}
}

// countingClient serves a namespace of many Pods and counts owner reads.
type countingClient struct {
	mu     sync.Mutex
	pods   []corev1.Pod
	owners map[string]OwnerResource
	reads  map[string]int
}

func (c *countingClient) ListPods(ctx context.Context, namespace string, options metav1.ListOptions) (*corev1.PodList, error) {
	return &corev1.PodList{Items: c.pods}, nil
}
func (c *countingClient) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	return nil, fmt.Errorf("not served")
}
func (c *countingClient) GetOwner(ctx context.Context, namespace string, ref metav1.OwnerReference) (OwnerResource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reads[ref.Kind+"/"+ref.Name]++
	if owner, ok := c.owners[ref.Kind+"/"+ref.Name]; ok {
		return owner, nil
	}
	if owner, ok := scaleOwner(ref.Kind, ref.Name); ok {
		return owner, nil
	}
	return OwnerResource{}, fmt.Errorf("no owner %s/%s", ref.Kind, ref.Name)
}
func (c *countingClient) OpenLogs(ctx context.Context, namespace, name string, options corev1.PodLogOptions) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not served")
}
func (c *countingClient) total() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, each := range c.reads {
		n += each
	}
	return n
}

// A discovery that names a workload reads the owners only of the Pods whose
// controller could lead to it -- the name itself, or the name and a dash, as
// a Deployment's ReplicaSet or a CronJob's Job is named -- and still checks
// those through the whole chain. app-0420 is not app-042.
func TestDiscoverNamingAWorkloadReadsOnlyItsCandidates(t *testing.T) {
	pods := scalePods(100)
	extra := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "app-0420-5d4f8-y", UID: "pod-app-0420", Labels: map[string]string{"app": "app-0420"},
		OwnerReferences: []metav1.OwnerReference{controllerRef("ReplicaSet", "app-0420-5d4f8", "rs-app-0420")}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}}}}
	client := &countingClient{pods: append(pods, extra), reads: map[string]int{}}
	provider := mustProvider(t, Options{Client: client})
	result, err := provider.Discover(context.Background(), DiscoverRequest{Namespace: "ns1", Workload: "app-042"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Targets) != 1 || result.Targets[0].Workload.Name != "app-042" || result.Targets[0].Target.Pod != "app-042-5d4f8-x" {
		t.Fatalf("targets: %+v", result.Targets)
	}
	if got := client.total(); got != 2 {
		t.Fatalf("%d owner reads (%v), want 2: the one candidate's ReplicaSet and Deployment", got, client.reads)
	}

	// A StatefulSet's Pods name it directly.
	db := OwnerResource{Kind: "StatefulSet", Namespace: "ns1", Name: "db", UID: "sts-db", Labels: map[string]string{"app": "db"}, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}}}
	stateful := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: "db-0", UID: "pod-db-0", Labels: map[string]string{"app": "db"},
		OwnerReferences: []metav1.OwnerReference{controllerRef("StatefulSet", "db", "sts-db")}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "db"}}}}
	client = &countingClient{pods: append(scalePods(100), stateful), owners: map[string]OwnerResource{"StatefulSet/db": db}, reads: map[string]int{}}
	provider = mustProvider(t, Options{Client: client})
	result, err = provider.Discover(context.Background(), DiscoverRequest{Namespace: "ns1", Workload: "db"})
	if err != nil || len(result.Targets) != 1 || result.Targets[0].Workload.Name != "db" {
		t.Fatalf("targets=%+v err=%v", result.Targets, err)
	}
	if got := client.total(); got != 1 {
		t.Fatalf("%d owner reads (%v), want 1", got, client.reads)
	}
}

// Pods of one workload share their owners: one discovery reads each owner
// once, however many replicas there are.
func TestDiscoverReadsEachOwnerOnce(t *testing.T) {
	var pods []corev1.Pod
	for i := 0; i < 10; i++ {
		pods = append(pods, corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "ns1", Name: fmt.Sprintf("app-007-5d4f8-%d", i), UID: types.UID(fmt.Sprintf("pod-%d", i)), Labels: map[string]string{"app": "app-007"},
			OwnerReferences: []metav1.OwnerReference{controllerRef("ReplicaSet", "app-007-5d4f8", "rs-app-007")}},
			Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}}}})
	}
	client := &countingClient{pods: pods, reads: map[string]int{}}
	provider := mustProvider(t, Options{Client: client})
	result, err := provider.Discover(context.Background(), DiscoverRequest{Namespace: "ns1"})
	if err != nil || len(result.Targets) != 10 {
		t.Fatalf("targets=%d err=%v", len(result.Targets), err)
	}
	if client.reads["ReplicaSet/app-007-5d4f8"] != 1 || client.reads["Deployment/app-007"] != 1 || client.total() != 2 {
		t.Fatalf("owner reads %v, want each owner once", client.reads)
	}
	// The next discovery reads them again: nothing is kept between requests.
	if _, err := provider.Discover(context.Background(), DiscoverRequest{Namespace: "ns1"}); err != nil {
		t.Fatal(err)
	}
	if client.total() != 4 {
		t.Fatalf("owner reads %v, want each owner read again by a new discovery", client.reads)
	}
}
