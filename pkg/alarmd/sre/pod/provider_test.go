// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package pod

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
)

type fixtureClient struct {
	mu          sync.Mutex
	pod         corev1.Pod
	owners      map[string]OwnerResource
	getCount    int
	changeAt    int
	change      func(*corev1.Pod)
	listOptions metav1.ListOptions
	logOptions  corev1.PodLogOptions
	logs        string
	listError   error
	ownerError  error
	listNS      string
	deleted     []string
	patches     []string
	writeError  error
}

func (f *fixtureClient) DeletePod(ctx context.Context, namespace, name, uid string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeError != nil {
		return f.writeError
	}
	f.deleted = append(f.deleted, namespace+"/"+name+"@"+uid)
	return nil
}
func (f *fixtureClient) GetWorkload(ctx context.Context, namespace, kind, name string) (OwnerResource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.owners[name], f.ownerError
}
func (f *fixtureClient) PatchWorkload(ctx context.Context, namespace, kind, name string, patch []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writeError != nil {
		return f.writeError
	}
	f.patches = append(f.patches, namespace+"/"+kind+"/"+name+" "+string(patch))
	return nil
}

func (f *fixtureClient) ListPods(ctx context.Context, namespace string, options metav1.ListOptions) (*corev1.PodList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listOptions = options
	f.listNS = namespace
	return &corev1.PodList{Items: []corev1.Pod{*f.pod.DeepCopy()}}, f.listError
}
func (f *fixtureClient) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.getCount++
	if f.changeAt > 0 && f.getCount >= f.changeAt {
		f.change(&f.pod)
	}
	return f.pod.DeepCopy(), nil
}
func (f *fixtureClient) GetOwner(ctx context.Context, namespace string, owner metav1.OwnerReference) (OwnerResource, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.owners[owner.Name], f.ownerError
}
func (f *fixtureClient) OpenLogs(ctx context.Context, namespace, name string, options corev1.PodLogOptions) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logOptions = options
	return io.NopCloser(strings.NewReader(f.logs)), nil
}

func controllerRef(kind, name, uid string) metav1.OwnerReference {
	yes := true
	group := "apps/v1"
	if kind == "Job" || kind == "CronJob" {
		group = "batch/v1"
	}
	return metav1.OwnerReference{APIVersion: group, Kind: kind, Name: name, UID: types.UID(uid), Controller: &yes}
}
func fixture() (*fixtureClient, Target) {
	labels := map[string]string{"app": "web", "app.kubernetes.io/instance": "release", "app.kubernetes.io/component": "fusion"}
	client := &fixtureClient{pod: corev1.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "tenant", Name: "web-abc", UID: "pod-uid", Labels: labels, OwnerReferences: []metav1.OwnerReference{controllerRef("ReplicaSet", "web-rs", "rs-uid")}}, Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "web", Image: "web:v1", Env: []corev1.EnvVar{{Name: "PASSWORD", Value: "never-return-env"}}}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning, ContainerStatuses: []corev1.ContainerStatus{{Name: "web", ContainerID: "containerd://cid", ImageID: "sha256:image", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}}}, owners: map[string]OwnerResource{}, logs: "hello"}
	client.owners["web-rs"] = OwnerResource{Kind: "ReplicaSet", Namespace: "tenant", Name: "web-rs", UID: "rs-uid", Labels: labels, Owners: []metav1.OwnerReference{controllerRef("Deployment", "web", "deployment-uid")}, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}}
	client.owners["web"] = OwnerResource{Kind: "Deployment", Namespace: "tenant", Name: "web", UID: "deployment-uid", Labels: labels, Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}}}
	target := Target{Namespace: "tenant", Pod: "web-abc", UID: "pod-uid", Container: "web", ContainerID: "containerd://cid", ImageID: "sha256:image"}
	return client, target
}
func requireCode(t *testing.T, err error, code string) {
	t.Helper()
	var named *Error
	if !errors.As(err, &named) || named.Code != code {
		t.Fatalf("error=%v, want %s", err, code)
	}
}
func mustProvider(t *testing.T, options Options) *Provider {
	t.Helper()
	provider, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

// The provider needs a client and nothing a deployment declares: the
// namespace a request names none of and the module label key are optional,
// and must be a namespace name and a label key when given.
func TestOptionsNeedOnlyAClient(t *testing.T) {
	client, _ := fixture()
	if _, err := New(Options{}); err == nil {
		t.Fatal("a provider without a client")
	}
	if provider := mustProvider(t, Options{Client: client}); provider.ModuleLabel() != DefaultModuleLabel || provider.DefaultNamespace() != "" {
		t.Fatalf("defaults: module label %q default namespace %q", provider.ModuleLabel(), provider.DefaultNamespace())
	}
	for _, options := range []Options{{Client: client, DefaultNamespace: "../tenant"}, {Client: client, ModuleLabel: "not a key"}} {
		requireCode(t, func() error { _, err := New(options); return err }(), CodeInvalidRequest)
	}
}

func TestDiscoverInspectOnlySafeFields(t *testing.T) {
	client, target := fixture()
	provider := mustProvider(t, Options{Client: client})
	result, err := provider.Discover(context.Background(), DiscoverRequest{Namespace: "tenant", Selector: "app=web", Release: "release", Module: "fusion", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Targets) != 1 || result.Targets[0].Target != target || result.Targets[0].Workload.Name != "web" || len(result.Targets[0].OwnerChain) != 2 {
		t.Fatalf("discover: %+v", result)
	}
	for _, part := range []string{"app=web", ReleaseLabel + "=release", DefaultModuleLabel + "=fusion"} {
		if !strings.Contains(client.listOptions.LabelSelector, part) {
			t.Fatalf("selector %q lacks %q", client.listOptions.LabelSelector, part)
		}
	}
	if client.listOptions.Limit != 1 || result.Targets[0].Release != "release" || result.Targets[0].Module != "fusion" {
		t.Fatalf("unbounded list or labels unread: %+v %+v", client.listOptions, result.Targets[0])
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "never-return-env") || strings.Contains(string(encoded), "PASSWORD") {
		t.Fatalf("pod environment leaked: %s", encoded)
	}
	observation, err := provider.Inspect(context.Background(), TargetRequest{Target: Target{Namespace: "tenant", Pod: target.Pod, Container: "web"}})
	if err != nil || observation.Target != target {
		t.Fatalf("inspect=%+v err=%v", observation, err)
	}
}

// Any Pod in any namespace can be read, but only through a verified owner
// chain: an owner whose UID or selector does not match the live resource, an
// owner kind outside the supported chain, or a container the Pod does not
// run is refused.
func TestRejectOwnerChainAndContainer(t *testing.T) {
	tests := map[string]func(*fixtureClient, *Target){
		"owner UID": func(c *fixtureClient, target *Target) {
			owner := c.owners["web-rs"]
			owner.UID = "replacement"
			c.owners["web-rs"] = owner
		},
		"selector": func(c *fixtureClient, target *Target) {
			owner := c.owners["web"]
			owner.Selector.MatchLabels = map[string]string{"app": "other"}
			c.owners["web"] = owner
		},
		"unknown owner": func(c *fixtureClient, target *Target) { c.pod.OwnerReferences[0].Kind = "CustomWorkload" },
		"container":     func(c *fixtureClient, target *Target) { target.Container = "sidecar" },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			client, target := fixture()
			change(client, &target)
			provider := mustProvider(t, Options{Client: client})
			_, err := provider.Inspect(context.Background(), TargetRequest{Target: target})
			requireCode(t, err, CodeNotInScope)
		})
	}
}

// A request reads the one namespace it names, or this process's own when it
// names none; with neither it is refused, and a listing across namespaces is
// not something a request can ask for.
func TestOneNamespacePerRequest(t *testing.T) {
	client, _ := fixture()
	provider := mustProvider(t, Options{Client: client, DefaultNamespace: "own"})
	if _, err := provider.Discover(context.Background(), DiscoverRequest{}); err != nil || client.listNS != "own" {
		t.Fatalf("no namespace: read %q err=%v, want own", client.listNS, err)
	}
	if _, err := provider.Discover(context.Background(), DiscoverRequest{Namespace: "kube-system"}); err != nil || client.listNS != "kube-system" {
		t.Fatalf("named namespace: read %q err=%v", client.listNS, err)
	}
	for _, namespace := range []string{"*", "a,b", "Kube-System"} {
		_, err := provider.Discover(context.Background(), DiscoverRequest{Namespace: namespace})
		requireCode(t, err, CodeInvalidRequest)
	}
	unknown := mustProvider(t, Options{Client: client})
	_, err := unknown.Discover(context.Background(), DiscoverRequest{})
	requireCode(t, err, CodeInvalidRequest)
	_, err = provider.Discover(context.Background(), DiscoverRequest{Selector: "app in (web"})
	requireCode(t, err, CodeInvalidRequest)
}

func TestRejectStaleIdentityBeforeExec(t *testing.T) {
	for _, field := range []string{"uid", "container", "image"} {
		t.Run(field, func(t *testing.T) {
			client, target := fixture()
			switch field {
			case "uid":
				target.UID = "old"
			case "container":
				target.ContainerID = "old"
			case "image":
				target.ImageID = "old"
			}
			calls := 0
			provider := mustProvider(t, Options{Client: client, Executor: executorFunc(func(ctx context.Context, request StreamRequest) (StreamResult, error) {
				calls++
				return StreamResult{}, nil
			})})
			_, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}})
			requireCode(t, err, CodeTargetChanged)
			if calls != 0 {
				t.Fatal("executed a stale target")
			}
		})
	}
}

func TestLogsBoundRedactionAndTargetChanged(t *testing.T) {
	client, target := fixture()
	client.logs = "password=visible\nsecretliteral\n" + strings.Repeat("x", 100)
	client.changeAt = 2
	client.change = func(pod *corev1.Pod) { pod.Status.ContainerStatuses[0].ContainerID = "new" }
	provider := mustProvider(t, Options{Client: client, LiteralSecrets: []string{"secretliteral"}})
	result, err := provider.Logs(context.Background(), LogsRequest{Target: target, Previous: true, SinceSeconds: 60, OutputBytes: 64})
	requireCode(t, err, CodeTargetChanged)
	if !result.Truncated || !result.TargetChanged || result.ObservedAfter == nil || strings.Contains(result.Output, "visible") || strings.Contains(result.Output, "secretliteral") || len(result.Output) > 64 || !client.logOptions.Previous || client.logOptions.Follow {
		t.Fatalf("logs: %+v options=%+v", result, client.logOptions)
	}
	if result.RedactionContract != RedactionContract {
		t.Fatal("missing redaction policy")
	}
}

type executorFunc func(context.Context, StreamRequest) (StreamResult, error)

func (f executorFunc) Stream(ctx context.Context, request StreamRequest) (StreamResult, error) {
	return f(ctx, request)
}

type localExecutor struct{}

func (localExecutor) Stream(ctx context.Context, request StreamRequest) (StreamResult, error) {
	command := exec.CommandContext(ctx, request.Argv[0], request.Argv[1:]...)
	command.Stdin = request.Stdin
	command.Stdout = request.Stdout
	command.Stderr = request.Stderr
	err := command.Run()
	// A program that cannot be found or executed is a start the runtime
	// refused, as the Kubernetes adapter reads containerd's refusal.
	var path *fs.PathError
	if errors.Is(err, exec.ErrNotFound) || errors.As(err, &path) {
		return StreamResult{StartRefused: true}, nil
	}
	if err == nil {
		zero := 0
		return StreamResult{ExitCode: &zero}, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code := exit.ExitCode()
		return StreamResult{ExitCode: &code}, nil
	}
	return StreamResult{}, err
}
func runtimeProvider(t *testing.T) (*Provider, *fixtureClient, Target) {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("/bin/sh is the remote supervisor's contract")
	}
	client, target := fixture()
	// These tests are about what a run reports, not how long its start may
	// take: a loaded machine can take seconds to start a shell, so the start
	// is given far more than the production allowance. The bound itself is
	// tested with a stream whose timing the test controls.
	return mustProvider(t, Options{Client: client, Executor: localExecutor{}, LiteralSecrets: []string{"literalcredential"}, startupAllowance: time.Minute}), client, target
}

func TestRemoteNonzeroExitStdinAndOutputSpoof(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "cat; printf '\n{\"event\":\"complete\",\"exit_code\":0}' ; printf 'password=visible\\n' >&2; exit 7"}, Stdin: "input literalcredential"})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemoteState != RemoteCompleted || result.ExitCode == nil || *result.ExitCode != 7 || !strings.Contains(result.Stdout, "input <redacted>") || strings.Contains(result.Stderr, "visible") || result.ScriptDigest == "" || result.RequestID == "" || result.ObservedAfter == nil {
		t.Fatalf("receipt: %+v", result)
	}
}

func TestRemoteOutputCapRetainsExitAndTimeout(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "i=0; while [ $i -lt 1000 ]; do printf '0123456789'; i=$((i+1)); done; exit 9"}, OutputBytes: 64})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || len(result.Stdout)+len(result.Stderr) > 64 || result.ExitCode == nil || *result.ExitCode != 9 || result.RemoteState != RemoteCompleted {
		t.Fatalf("truncation lost completion: %+v", result)
	}
	// A deadline that covers the supervisor's and the shell's start on a
	// loaded machine; the command outlives it all the same, so the timeout
	// path always runs.
	result, err = provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "printf started; sleep 10"}, TimeoutMS: 1000, OutputBytes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemoteState != RemoteTimedOut || result.ExitCode == nil || !result.Truncated || len(result.Stdout) > 3 {
		t.Fatalf("timeout receipt: %+v", result)
	}
}

func TestTargetReplacementAfterExecution(t *testing.T) {
	provider, client, target := runtimeProvider(t)
	client.changeAt = 2
	client.change = func(pod *corev1.Pod) { pod.UID = "replaced" }
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "printf evidence"}})
	requireCode(t, err, CodeTargetChanged)
	if !result.TargetChanged || result.RemoteState != RemoteCompleted || result.EvidenceScope != "target_changed" || result.Stdout != "evidence" {
		t.Fatalf("replacement receipt: %+v", result)
	}
}

func TestTransportFailureUnknownAndNoRetry(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	calls := 0
	provider.options.Executor = executorFunc(func(ctx context.Context, request StreamRequest) (StreamResult, error) {
		calls++
		return StreamResult{}, errors.New("Authorization=credential")
	})
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}})
	requireCode(t, err, RemoteUnknown)
	if calls != 1 || result.RemoteState != RemoteUnknown || strings.Contains(err.Error(), "credential") {
		t.Fatalf("transport result: %+v err=%v calls=%d", result, err, calls)
	}
}

func TestConcurrentRequestsAndBudgets(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	entered := make(chan struct{})
	unblock := make(chan struct{})
	provider.options.Executor = executorFunc(func(ctx context.Context, request StreamRequest) (StreamResult, error) {
		close(entered)
		<-unblock
		return StreamResult{}, errors.New("lost")
	})
	done := make(chan struct{})
	go func() {
		defer close(done)
		provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}})
	}()
	<-entered
	_, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}})
	requireCode(t, err, CodeBusy)
	close(unblock)
	<-done
	for _, request := range []ExecRequest{{Target: target}, {Target: target, Argv: []string{"true"}, Stdin: strings.Repeat("x", 32<<10+1)}, {Target: target, Argv: []string{"true"}, TimeoutMS: 30001}, {Target: target, Argv: []string{"true"}, OutputBytes: 512<<10 + 1}} {
		_, err := provider.Exec(context.Background(), request)
		requireCode(t, err, CodeInvalidRequest)
	}
}

func TestStandardExecIsPOSTNonTTYAndNeverRetries(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/api/v1/namespaces/tenant/pods/web-abc/exec" || r.URL.Query().Get("tty") == "true" || r.URL.Query().Get("container") != "web" || strings.Join(r.URL.Query()["command"], " ") != "/bin/sh -c script" {
			t.Errorf("incorrect standard exec request: %s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(403)
		io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"Forbidden","code":403}`)
	}))
	defer server.Close()
	executor, err := NewKubernetesExecutor(&rest.Config{Host: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, target := fixture()
	_, err = executor.Stream(context.Background(), StreamRequest{Target: target, Argv: []string{"/bin/sh", "-c", "script"}, Stdout: io.Discard, Stderr: io.Discard})
	if err == nil || calls != 1 {
		t.Fatalf("exec err=%v calls=%d", err, calls)
	}
}

type startedWriter struct {
	destination io.Writer
	once        sync.Once
	started     chan struct{}
}

func (w *startedWriter) Write(data []byte) (int, error) {
	n, err := w.destination.Write(data)
	if strings.Contains(string(data), " started\n") {
		w.once.Do(func() { close(w.started) })
	}
	return n, err
}

func TestCancellationReturnsUnknownAndRemoteTimerContinues(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	started := make(chan struct{})
	remoteDone := make(chan error, 1)
	provider.options.Executor = executorFunc(func(ctx context.Context, request StreamRequest) (StreamResult, error) {
		command := exec.Command(request.Argv[0], request.Argv[1:]...)
		command.Stdin = request.Stdin
		command.Stdout = request.Stdout
		command.Stderr = &startedWriter{destination: request.Stderr, started: started}
		if err := command.Start(); err != nil {
			return StreamResult{}, err
		}
		go func() { remoteDone <- command.Wait() }()
		select {
		case err := <-remoteDone:
			zero := 0
			return StreamResult{ExitCode: &zero}, err
		case <-ctx.Done():
			return StreamResult{}, ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan ExecReceipt, 1)
	go func() {
		result, err := provider.Exec(ctx, ExecRequest{Target: target, Argv: []string{"/bin/sh", "-c", "sleep 10"}, TimeoutMS: 5000})
		if err == nil {
			t.Error("cancel did not return a named failure")
		}
		done <- result
	}()
	// The waits below only guard a hang. Promptness is an order, not a
	// margin: the canceled answer arrives while the remote command, under its
	// own 5 s deadline, is still running, and the remote then ends by itself.
	select {
	case <-started:
	case <-time.After(time.Minute):
		t.Fatal("remote supervisor did not start")
	}
	cancel()
	select {
	case result := <-done:
		if result.RemoteState != RemoteUnknown || result.ExitCode != nil || result.EvidenceScope != "post_observation_unavailable" {
			t.Fatalf("canceled receipt: %+v", result)
		}
	case <-time.After(time.Minute):
		t.Fatal("cancellation did not return")
	}
	select {
	case err := <-remoteDone:
		t.Fatalf("cancellation waited for the remote command to end: %v", err)
	default:
	}
	select {
	case err := <-remoteDone:
		if err != nil {
			t.Fatalf("independent remote timer failed: %v", err)
		}
	case <-time.After(time.Minute):
		t.Fatal("remote process outlived its independent deadline")
	}
}

func TestContainerAndProbeStatusWithoutMessages(t *testing.T) {
	client, target := fixture()
	client.pod.Spec.NodeName = "node-a"
	deleted := metav1.Now()
	client.pod.DeletionTimestamp = &deleted
	client.pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse, Reason: "ContainersNotReady", Message: "credential=hidden"}}
	status := &client.pod.Status.ContainerStatuses[0]
	status.RestartCount = 3
	status.State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff", Message: "secret=hidden"}}
	status.LastTerminationState.Terminated = &corev1.ContainerStateTerminated{Reason: "OOMKilled", ExitCode: 137, Signal: 9}
	provider := mustProvider(t, Options{Client: client})
	observed, err := provider.Inspect(context.Background(), TargetRequest{Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if observed.RestartCount != 3 || observed.WaitingReason != "CrashLoopBackOff" || observed.LastTermination == nil || observed.LastTermination.ExitCode != 137 || len(observed.Conditions) != 1 || observed.NodeName != "node-a" || observed.DeletionTimestamp == nil || observed.Running {
		t.Fatalf("missing status: %+v", observed)
	}
	encoded, _ := json.Marshal(observed)
	if strings.Contains(string(encoded), "hidden") {
		t.Fatalf("status message leaked: %s", encoded)
	}
}

// The module label key is the deployment's: discovery filters on it and
// targets read their module from it. Under the default key a label of another
// key is not a module.
func TestTheModuleLabelKeyIsTheDeployments(t *testing.T) {
	client, _ := fixture()
	client.pod.Labels["example.com/module"] = "fusion"
	delete(client.pod.Labels, DefaultModuleLabel)
	provider := mustProvider(t, Options{Client: client, ModuleLabel: "example.com/module"})
	result, err := provider.Discover(context.Background(), DiscoverRequest{Namespace: "tenant", Module: "fusion"})
	if err != nil || len(result.Targets) != 1 || result.Targets[0].Module != "fusion" {
		t.Fatalf("discovery by the deployment's key: %+v err=%v", result, err)
	}
	if !strings.Contains(client.listOptions.LabelSelector, "example.com/module=fusion") || strings.Contains(client.listOptions.LabelSelector, DefaultModuleLabel) {
		t.Fatalf("selector %q", client.listOptions.LabelSelector)
	}
	plain := mustProvider(t, Options{Client: client})
	result, err = plain.Discover(context.Background(), DiscoverRequest{Namespace: "tenant"})
	if err != nil || len(result.Targets) != 1 || result.Targets[0].Module != "" {
		t.Fatalf("default key read another key's module: %+v err=%v", result, err)
	}
}

func TestInvalidProtocolCannotClaimNotStarted(t *testing.T) {
	provider, _, target := runtimeProvider(t)
	provider.options.Executor = executorFunc(func(ctx context.Context, request StreamRequest) (StreamResult, error) {
		io.WriteString(request.Stderr, "wrong complete 0\n")
		zero := 0
		return StreamResult{ExitCode: &zero}, nil
	})
	result, err := provider.Exec(context.Background(), ExecRequest{Target: target, Argv: []string{"true"}})
	requireCode(t, err, RemoteUnknown)
	if result.RemoteState != RemoteUnknown {
		t.Fatalf("invalid receipt claimed no command ran: %+v", result)
	}
}

// The output policy masks URL userinfo under any scheme, as its contract
// says, and keeps an address that is not a URL.
func TestOutputPolicyMasksURLUserinfoUnderAnyScheme(t *testing.T) {
	out, _ := newRedactor(nil).bounded("redis://user:redis-credential@192.0.2.10:6379 amqp://guest:amqp-credential@mq/ ops@example.test", 4096)
	if strings.Contains(out, "redis-credential") || strings.Contains(out, "amqp-credential") || !strings.Contains(out, "ops@example.test") {
		t.Fatalf("policy output %q", out)
	}
}

// A Provider printed into a log line names its settings, never the literal
// credentials it redacts.
func TestAFormattedProviderCarriesNoCredential(t *testing.T) {
	client, _ := fixture()
	provider := mustProvider(t, Options{Client: client, LiteralSecrets: []string{"literalcredential"}})
	if out := fmt.Sprintf("%v %+v", provider, provider); strings.Contains(out, "literalcredential") {
		t.Fatalf("formatted provider %q", out)
	}
}
