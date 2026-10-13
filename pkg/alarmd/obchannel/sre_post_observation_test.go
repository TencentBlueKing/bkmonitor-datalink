// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cliauth"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type postObservationAuth struct{}

func (postObservationAuth) Authenticate(context.Context, string) (cliauth.Session, error) {
	return cliauth.Session{ID: "post-observation-session", EnvironmentID: "test", Scope: cliauth.ScopeReadonly, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func (postObservationAuth) Admit(_ context.Context, session cliauth.Session, _ bool) (cliauth.Session, error) {
	return session, nil
}

type postObservationClient struct {
	target       corev1.Pod
	postAPIError bool
	getCalls     atomic.Int32
	logCalls     atomic.Int32
}

func (*postObservationClient) ListPods(context.Context, string, metav1.ListOptions) (*corev1.PodList, error) {
	return nil, errors.New("unexpected list operation")
}

func (c *postObservationClient) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.getCalls.Add(1) == 2 && c.postAPIError {
		return nil, errors.New("private post-observation API failure")
	}
	return c.target.DeepCopy(), nil
}

func (*postObservationClient) GetOwner(context.Context, string, metav1.OwnerReference) (pod.OwnerResource, error) {
	return pod.OwnerResource{Kind: "StatefulSet", Namespace: "fixture", Name: "web", UID: "workload-uid", Labels: map[string]string{"app": "web"}, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}}, nil
}

func (c *postObservationClient) OpenLogs(context.Context, string, string, corev1.PodLogOptions) (io.ReadCloser, error) {
	c.logCalls.Add(1)
	return io.NopCloser(strings.NewReader("completed log read\n")), nil
}

type postObservationExecutor struct{ calls atomic.Int32 }

func (e *postObservationExecutor) Stream(_ context.Context, request pod.StreamRequest) (pod.StreamResult, error) {
	e.calls.Add(1)
	// Simulate the supervisor's records and the command's output; no local
	// or remote command runs.
	nonce := request.Argv[4]
	fmt.Fprintf(request.Stderr, "%s started\n\n%s group 1\n", nonce, nonce)
	io.WriteString(request.Stdout, "completed command output\n")
	io.WriteString(request.Stderr, "command diagnostic\n")
	fmt.Fprintf(request.Stderr, "\n%s complete 19\n", nonce)
	zero := 0
	return pod.StreamResult{ExitCode: &zero}, nil
}

func TestSREPostObservationUnavailablePreservesCompletedEvidence(t *testing.T) {
	for _, operation := range []string{"pod.exec", "pod.logs"} {
		for _, postAPIError := range []bool{false, true} {
			name := "identity_verified"
			if postAPIError {
				name = "post_api_unavailable"
			}
			t.Run(operation+"/"+name, func(t *testing.T) {
				controller := true
				client := &postObservationClient{postAPIError: postAPIError, target: corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Namespace: "fixture", Name: "web-0", UID: "pod-uid", Labels: map[string]string{"app": "web"}, OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "web", UID: "workload-uid", Controller: &controller}}},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "web:v1"}}},
					Status:     corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "web", ContainerID: "containerd://fixture", ImageID: "sha256:fixture", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}},
				}}
				executor := &postObservationExecutor{}
				provider, err := pod.New(pod.Options{Client: client, Executor: executor, DefaultNamespace: "fixture"})
				if err != nil {
					t.Fatal(err)
				}
				channel, err := New(Options{Auth: postObservationAuth{}, EnvironmentID: "test", Replica: "replica-1", Operations: SREOperations(SREOptions{Pods: provider})})
				if err != nil {
					t.Fatal(err)
				}
				params := Params{"target": map[string]any{"namespace": "fixture", "pod": "web-0", "uid": "pod-uid", "container": "web", "container_id": "containerd://fixture", "image_id": "sha256:fixture"}}
				if operation == "pod.exec" {
					params["argv"] = []any{"/bin/sh", "-c", "exit 19"}
				}
				status, response := call(t, channel, envelope(channel, "invoke", operation, params))
				wantStatus, wantScope := "ok", "identity_verified_before_and_after"
				if postAPIError {
					wantStatus, wantScope = "partial", "post_observation_unavailable"
				}
				if status != 200 || response.Status != wantStatus || response.Error != nil || response.Evidence.Complete != !postAPIError {
					t.Fatalf("completed operation lost its evidence state: HTTP %d %+v", status, response)
				}
				if client.getCalls.Load() != 2 {
					t.Fatalf("expected before/after GetPod, got %d", client.getCalls.Load())
				}
				if postAPIError && !strings.Contains(strings.Join(response.Evidence.Limitations, " "), "target identity verification did not complete") {
					t.Fatalf("missing post-observation limitation: %+v", response.Evidence)
				}
				data, err := json.Marshal(response.Result)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "private post-observation API failure") {
					t.Fatal("raw API failure leaked into the receipt")
				}
				if operation == "pod.exec" {
					var receipt pod.ExecReceipt
					if err := json.Unmarshal(data, &receipt); err != nil {
						t.Fatal(err)
					}
					if executor.calls.Load() != 1 || receipt.RemoteState != pod.RemoteCompleted || receipt.ExitCode == nil || *receipt.ExitCode != 19 || receipt.Stdout != "completed command output\n" || receipt.Stderr != "command diagnostic\n" || receipt.EvidenceScope != wantScope || receipt.Truncated || receipt.TargetChanged || (receipt.ObservedAfter != nil) != !postAPIError {
						t.Fatalf("completed exec receipt changed: %+v calls=%d", receipt, executor.calls.Load())
					}
				} else {
					var receipt pod.LogsReceipt
					if err := json.Unmarshal(data, &receipt); err != nil {
						t.Fatal(err)
					}
					if client.logCalls.Load() != 1 || executor.calls.Load() != 0 || receipt.Output != "completed log read\n" || receipt.EvidenceScope != wantScope || receipt.Truncated || receipt.TargetChanged || (receipt.ObservedAfter != nil) != !postAPIError {
						t.Fatalf("completed logs receipt changed: %+v reads=%d", receipt, client.logCalls.Load())
					}
				}
			})
		}
	}
}
