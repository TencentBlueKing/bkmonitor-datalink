// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package node

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	kt "k8s.io/client-go/testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sre/pod"
)

type testExecutor struct {
	mu          sync.Mutex
	starts      int
	cancelled   bool
	lost        bool
	getResponse string
}

func (e *testExecutor) Stream(ctx context.Context, r pod.StreamRequest) (pod.StreamResult, error) {
	var q map[string]any
	_ = json.NewDecoder(r.Stdin).Decode(&q)
	e.mu.Lock()
	defer e.mu.Unlock()
	if q["action"] == "get" && e.getResponse != "" && e.starts > 0 {
		_, _ = io.WriteString(r.Stdout, e.getResponse)
		z := 0
		return pod.StreamResult{ExitCode: &z}, nil
	}
	state := "preparing"
	switch q["action"] {
	case "start":
		if !e.cancelled {
			e.starts++
		}
		if e.lost {
			return pod.StreamResult{}, errors.New("connection lost after start")
		}
	case "cancel":
		e.cancelled = true
	}
	if e.starts > 0 {
		state = "succeeded"
	}
	if e.cancelled {
		state = "cancelled"
	}
	_, _ = io.WriteString(r.Stdout, `{"state":"`+state+`","exit_code":0,"next_cursor":"cursor"}`)
	z := 0
	return pod.StreamResult{ExitCode: &z}, nil
}
func (e *testExecutor) count() int { e.mu.Lock(); defer e.mu.Unlock(); return e.starts }
func setup(t *testing.T, lostCreate bool) (*Provider, *fake.Clientset, *testExecutor) {
	t.Helper()
	client := fake.NewSimpleClientset(&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node", UID: "node-uid"}, Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{OperatingSystem: "linux"}}})
	client.PrependReactor("create", "jobs", func(a kt.Action) (bool, runtime.Object, error) {
		j := a.(kt.CreateAction).GetObject().(*batchv1.Job).DeepCopy()
		j.UID = "job-uid"
		if err := client.Tracker().Create(schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}, j, j.Namespace); err != nil {
			return true, nil, err
		}
		yes := true
		target := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: j.Name + "-pod", Namespace: j.Namespace, UID: "pod-uid", Labels: map[string]string{"job-name": j.Name}, OwnerReferences: []metav1.OwnerReference{{Kind: "Job", UID: j.UID, Controller: &yes}}}, Spec: corev1.PodSpec{NodeName: "node"}, Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "runner", ContainerID: "container-id", ImageID: "image-id", State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}
		_ = client.Tracker().Create(schema.GroupVersionResource{Version: "v1", Resource: "pods"}, target, target.Namespace)
		if lostCreate {
			return true, nil, errors.New("lost creation response")
		}
		return true, j, nil
	})
	executor := &testExecutor{}
	p, err := New(Options{Redis: redistest.Start(t).Client(), KeyPrefix: "test:{node}:", FingerprintKeySource: func() []byte { return []byte("shared-key") }, DefaultNamespace: "ns", Image: "alarmd:1", DeploymentID: "deployment", Client: client, Executor: executor})
	if err != nil {
		t.Fatal(err)
	}
	return p, client, executor
}
func request() ExecRequest {
	id, _ := uuid.NewV7()
	return ExecRequest{ExecutionID: id.String(), Node: Node{Name: "node", UID: "node-uid"}, Mode: "host", User: "root", Cwd: "/tmp", Argv: []string{"sh", "-s"}, Stdin: "secret-script"}
}
func await(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := testwait.Deadline(t)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond) // fixed-wait: the poll interval of a condition wait bounded by the test deadline
	}
	t.Fatal("condition did not become true")
}

func TestConcurrentDuplicateAndCreateResponseLossDoNotReplay(t *testing.T) {
	p, client, executor := setup(t, true)
	ctx := context.Background()
	q := request()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Exec(ctx, q); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	await(t, func() bool { return executor.count() == 1 })
	other := *p
	for i := 0; i < 10; i++ {
		if _, err := other.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	q.Stdin = "different"
	if _, err := p.Exec(ctx, q); err == nil {
		t.Fatal("conflicting replay accepted")
	}
	jobs, _ := client.BatchV1().Jobs("ns").List(ctx, metav1.ListOptions{})
	if len(jobs.Items) != 1 || executor.count() != 1 {
		t.Fatalf("jobs %d starts %d", len(jobs.Items), executor.count())
	}
	stored, _ := p.options.Redis.HGetAll(ctx, p.key(q.ExecutionID)).Result()
	raw, _ := json.Marshal(stored)
	if strings.Contains(string(raw), "secret-script") {
		t.Fatal("stored input")
	}
	result, err := other.Get(ctx, GetRequest{ExecutionID: q.ExecutionID})
	if err != nil || result.State != "succeeded" {
		t.Fatalf("cross-replica result %+v %v", result, err)
	}
}
func TestLostDispatchAndReplacedPodNeverRestart(t *testing.T) {
	p, client, e := setup(t, false)
	e.lost = true
	ctx := context.Background()
	q := request()
	_, err := p.Exec(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return e.count() == 1 })
	await(t, func() bool { r, _, _, _ := p.read(ctx, q.ExecutionID); return r.State == "unknown" })
	r, _, _, _ := p.read(ctx, q.ExecutionID)
	candidate, _ := client.CoreV1().Pods("ns").Get(ctx, r.PodName, metav1.GetOptions{})
	candidate.UID = types.UID("replacement")
	_, _ = client.CoreV1().Pods("ns").Update(ctx, candidate, metav1.UpdateOptions{})
	result, err := p.Get(ctx, GetRequest{ExecutionID: q.ExecutionID})
	if err != nil || !result.OutputUnavailable {
		t.Fatalf("replacement %+v %v", result, err)
	}
	// Each repeat has to be answered from the record it already made: a
	// repeat refused before it read that record would dispatch nothing
	// either, and the count below would hold for a reason the case is not
	// about.
	for i := 0; i < 3; i++ {
		if again, err := p.Exec(ctx, q); err != nil || again.ExecutionID != q.ExecutionID || again.State != "unknown" {
			t.Fatalf("repeat %d: %+v %v, want the lost execution's own record", i, again, err)
		}
	}
	if e.count() != 1 {
		t.Fatal("replayed command")
	}
}
func TestCancelBeforeDispatchAndCAS(t *testing.T) {
	p, _, e := setup(t, false)
	ctx := context.Background()
	q := request()
	if err := normalize(&q); err != nil {
		t.Fatal(err)
	}
	r := Record{ExecutionID: q.ExecutionID, State: "preparing"}
	raw, _ := json.Marshal(r)
	_, err := createScript.Run(ctx, p.options.Redis, []string{p.key(r.ExecutionID), p.index()}, string(raw), "digest", time.Now().Unix(), r.ExecutionID).Result()
	if err != nil {
		t.Fatal(err)
	}
	result, err := p.Cancel(ctx, CancelRequest{ExecutionID: r.ExecutionID})
	if err != nil || !result.CancelRequested {
		t.Fatal(err)
	}
	next := r
	next.State = "dispatching"
	if p.replace(ctx, r, next, true) == nil {
		t.Fatal("dispatch accepted after cancel")
	}
	p.prepare(q, r)
	if e.count() != 0 {
		t.Fatal("cancelled input executed")
	}
}
func TestExpiredIDAndMissingSharedKeyRefused(t *testing.T) {
	p, client, _ := setup(t, false)
	q := request()
	q.ExecutionID = "01800000-0000-7000-8000-000000000001"
	if _, err := p.Exec(context.Background(), q); err == nil {
		t.Fatal("expired ID accepted")
	}
	q = request()
	p.options.FingerprintKeySource = func() []byte { return nil }
	if _, err := p.Exec(context.Background(), q); err == nil {
		t.Fatal("missing shared key accepted")
	}
	jobs, _ := client.BatchV1().Jobs("ns").List(context.Background(), metav1.ListOptions{})
	if len(jobs.Items) != 0 {
		t.Fatal("created job on refusal")
	}
}
func TestJobHasNoInputAndBoundedLifetime(t *testing.T) {
	p, _, _ := setup(t, false)
	j := p.job(Record{ExecutionID: "id", Node: Node{Name: "node"}, Mode: "filesystem", Deadline: time.Now().Add(time.Hour)})
	s := j.Spec.Template.Spec
	if s.HostPID || !s.Containers[0].VolumeMounts[0].ReadOnly || *s.AutomountServiceAccountToken || *j.Spec.BackoffLimit != 0 || *j.Spec.TTLSecondsAfterFinished <= 0 {
		t.Fatal("unsafe job template")
	}
	if len(s.Containers[0].Env) != 0 || len(s.Containers[0].Args) != 0 {
		t.Fatal("job template carries input")
	}
}

func TestRedisFailureRejectsBeforeCreatingCarrier(t *testing.T) {
	p, client, e := setup(t, false)
	_ = p.options.Redis.Close()
	if _, err := p.Exec(context.Background(), request()); err == nil {
		t.Fatal("accepted execution without shared store")
	}
	jobs, _ := client.BatchV1().Jobs("ns").List(context.Background(), metav1.ListOptions{})
	if len(jobs.Items) != 0 || e.count() != 0 {
		t.Fatal("started carrier or command without shared store")
	}
}

func TestOneMiBStdinJSONEscapingFitsContract(t *testing.T) {
	q := request()
	q.Stdin = strings.Repeat("\x01", 1<<20)
	if err := normalize(&q); err != nil {
		t.Fatal(err)
	}
}

func TestFilesystemDefaultCwdAndHostRequiresExplicitContext(t *testing.T) {
	q := request()
	q.Mode = "filesystem"
	q.Cwd = ""
	q.User = ""
	if err := normalize(&q); err != nil || q.Cwd != "/" {
		t.Fatalf("filesystem default %+v %v", q, err)
	}
	q.Mode = "host"
	q.Cwd = ""
	if err := normalize(&q); err == nil {
		t.Fatal("host context silently defaulted")
	}
}

func TestCreationRejectionKeepsActionableRedactedDetail(t *testing.T) {
	p, client, _ := setup(t, false)
	client.PrependReactor("create", "jobs", func(kt.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("forbidden by PodSecurity: privileged=true password=unsafe")
	})
	q := request()
	if _, err := p.Exec(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { r, _, _, _ := p.read(context.Background(), q.ExecutionID); return r.State == "unknown" })
	r, _, _, _ := p.read(context.Background(), q.ExecutionID)
	if !strings.Contains(r.Detail, "PodSecurity") || strings.Contains(r.Detail, "unsafe") {
		t.Fatalf("detail %q", r.Detail)
	}
}

func TestOutputFailurePreservesTerminalStateAndPartialEvidence(t *testing.T) {
	for _, message := range []string{"output storage failed", "output pipe remained open after command exit"} {
		t.Run(message, func(t *testing.T) {
			p, _, e := setup(t, false)
			ctx := context.Background()
			q := request()
			e.getResponse = `{"state":"failed","exit_code":7,"error":"` + message + `","output":[{"stream":"stdout","data":"saved evidence"}],"next_cursor":"cursor"}`
			if _, err := p.Exec(ctx, q); err != nil {
				t.Fatal(err)
			}
			await(t, func() bool { return e.count() == 1 })
			await(t, func() bool { r, _, _, _ := p.read(ctx, q.ExecutionID); return r.State == "failed" })
			result, err := p.Get(ctx, GetRequest{ExecutionID: q.ExecutionID})
			if err != nil || result.State != "failed" || result.ExitCode == nil || *result.ExitCode != 7 || !result.OutputIncomplete || !strings.Contains(string(result.Runner), "saved evidence") {
				t.Fatalf("lost terminal/output facts: %+v %v", result, err)
			}
			stored, _, _, err := p.read(ctx, q.ExecutionID)
			if err != nil || stored.State != "failed" || !stored.OutputIncomplete || stored.ExitCode == nil || *stored.ExitCode != 7 {
				t.Fatalf("not persisted: %+v %v", stored, err)
			}
			if e.count() != 1 {
				t.Fatal("execution replayed")
			}
		})
	}
}

func TestInvalidCursorRemainsControlError(t *testing.T) {
	p, _, e := setup(t, false)
	ctx := context.Background()
	q := request()
	_, err := p.Exec(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return e.count() == 1 })
	e.mu.Lock()
	e.getResponse = `{"state":"succeeded","exit_code":0,"error":"invalid output cursor","control_error":true}`
	e.mu.Unlock()
	if _, err = p.Get(ctx, GetRequest{ExecutionID: q.ExecutionID, OutputCursor: "bad"}); err == nil {
		t.Fatal("invalid cursor accepted")
	}
}

func TestRetryWithNewRequestAndRenewedSessionUsesOriginalExecution(t *testing.T) {
	p, _, e := setup(t, false)
	ctx := context.Background()
	q := request()
	q.Caller = Caller{SessionID: "old-session", RequestID: "first-request"}
	if _, err := p.Exec(ctx, q); err != nil {
		t.Fatal(err)
	}
	q.Caller = Caller{SessionID: "renewed-session", RequestID: "second-request"}
	result, err := p.Exec(ctx, q)
	if err != nil {
		t.Fatalf("caller metadata changed command digest: %v", err)
	}
	if result.Caller.RequestID != "first-request" {
		t.Fatal("original audit caller replaced")
	}
	await(t, func() bool { return e.count() == 1 })
}

func TestCancelSelectedExecutionPersistsAcrossReplica(t *testing.T) {
	p, _, e := setup(t, false)
	ctx := context.Background()
	q := request()
	if _, err := p.Exec(ctx, q); err != nil {
		t.Fatal(err)
	}
	await(t, func() bool { return e.count() == 1 })
	other := *p
	result, err := other.Cancel(ctx, CancelRequest{ExecutionID: q.ExecutionID})
	if err != nil || !result.CancelRequested || !strings.Contains(string(result.Runner), "cancelled") {
		t.Fatalf("cancel %+v %v", result, err)
	}
	_, _, intent, err := p.read(ctx, q.ExecutionID)
	if err != nil || !intent {
		t.Fatal("cancel intent not durable")
	}
	if e.count() != 1 {
		t.Fatal("cancel re-executed command")
	}
}
