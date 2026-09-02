// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

func TestConcurrentRunnerRunsDisjointRuntimeKeysTogether(t *testing.T) {
	t.Parallel()

	first := newBlockingRoutedTask(RuntimeKey{StrategyID: "1"})
	second := newBlockingRoutedTask(RuntimeKey{StrategyID: "2"})
	runner := newConcurrentRunnerForTest(t, map[string]*blockingRoutedTask{"first": first, "second": second}, ConcurrentRunnerLimits{
		PreparationWorkers: 2, StatefulWorkers: 2, MaxInflightMessages: 2, MaxInflightBytes: 1024, MaxRuntimeKeysPerMessage: 2, MaxPendingKeyRefs: 2,
	})

	if err := runner.Submit(10, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := runner.Submit(11, []byte("second")); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, first.evaluateStarted, "first stateful task")
	awaitSignal(t, second.evaluateStarted, "disjoint stateful task")
	close(first.evaluateRelease)
	close(second.evaluateRelease)
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRunnerDoesNotBlockSubmitWhileBuildingMessage(t *testing.T) {
	t.Parallel()

	buildStarted := make(chan struct{})
	buildRelease := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(buildRelease) })
	builder := &blockingTaskBuilder{
		started: buildStarted,
		release: buildRelease,
		task: &immediateRoutedTask{
			keys:    []RuntimeKey{{StrategyID: "1"}},
			outcome: MessageOutcome{Kind: MessageOutcomeCompleted, Message: &MessageResult{Receipt: &contract.MessageReceiptV1{}}},
		},
	}
	runner, err := NewConcurrentRoutedPartitionRunner(
		context.Background(), builder, completedCriticalPhase{},
		partitionOffsetCommitterFunc(func(context.Context, int64) error { return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }), nil,
		ConcurrentRunnerLimits{PreparationWorkers: 2, StatefulWorkers: 1, MaxInflightMessages: 2, MaxInflightBytes: 1024, MaxRuntimeKeysPerMessage: 1, MaxPendingKeyRefs: 2}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	submitted := make(chan error, 1)
	go func() { submitted <- runner.Submit(1, []byte("message")) }()
	awaitSignal(t, buildStarted, "message build")
	select {
	case err := <-submitted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Millisecond):
		t.Fatal("Submit remained blocked by message construction")
	}
	releaseOnce.Do(func() { close(buildRelease) })
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRunnerPreservesReceiveOrderWhenMessageBuildsFinishOutOfOrder(t *testing.T) {
	t.Parallel()

	key := RuntimeKey{StrategyID: "1"}
	first := newBlockingRoutedTask(key)
	second := newBlockingRoutedTask(key)
	firstBuildStarted := make(chan struct{})
	firstBuildRelease := make(chan struct{})
	secondBuilt := make(chan struct{})
	builder := &outOfOrderTaskBuilder{
		tasks:        map[string]RoutedMessageTask{"first": first, "second": second},
		firstStarted: firstBuildStarted, firstRelease: firstBuildRelease, secondBuilt: secondBuilt,
	}
	runner, err := NewConcurrentRoutedPartitionRunner(
		context.Background(), builder, completedCriticalPhase{},
		partitionOffsetCommitterFunc(func(context.Context, int64) error { return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }), nil,
		ConcurrentRunnerLimits{PreparationWorkers: 2, StatefulWorkers: 2, MaxInflightMessages: 2, MaxInflightBytes: 1024, MaxRuntimeKeysPerMessage: 1, MaxPendingKeyRefs: 2}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Submit(10, []byte("first")); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, firstBuildStarted, "first message build")
	if err := runner.Submit(11, []byte("second")); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, secondBuilt, "second message build")
	assertNoSignal(t, second.evaluateStarted, "later shared-key message before earlier build")

	close(firstBuildRelease)
	awaitSignal(t, first.evaluateStarted, "first shared-key message")
	assertNoSignal(t, second.evaluateStarted, "later shared-key message before earlier completion")
	close(first.evaluateRelease)
	awaitSignal(t, second.evaluateStarted, "second shared-key message")
	close(second.evaluateRelease)
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRunnerSerializesSharedRuntimeKey(t *testing.T) {
	t.Parallel()

	key := RuntimeKey{StrategyID: "1"}
	first := newBlockingRoutedTask(key)
	second := newBlockingRoutedTask(key)
	runner := newConcurrentRunnerForTest(t, map[string]*blockingRoutedTask{"first": first, "second": second}, ConcurrentRunnerLimits{
		PreparationWorkers: 2, StatefulWorkers: 2, MaxInflightMessages: 2, MaxInflightBytes: 1024, MaxRuntimeKeysPerMessage: 2, MaxPendingKeyRefs: 2,
	})

	if err := runner.Submit(20, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := runner.Submit(21, []byte("second")); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, first.evaluateStarted, "first stateful task")
	assertNoSignal(t, second.evaluateStarted, "shared-key successor")
	close(first.evaluateRelease)
	awaitSignal(t, second.evaluateStarted, "shared-key successor after release")
	close(second.evaluateRelease)
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRunnerDoesNotCommitPastEarlierIncompleteOffset(t *testing.T) {
	t.Parallel()

	first := newBlockingRoutedTask(RuntimeKey{StrategyID: "1"})
	second := newBlockingRoutedTask(RuntimeKey{StrategyID: "2"})
	commits := make(chan int64, 2)
	runner := newConcurrentRunnerForTestWithCommitter(t, map[string]*blockingRoutedTask{"first": first, "second": second}, ConcurrentRunnerLimits{
		PreparationWorkers: 2, StatefulWorkers: 2, MaxInflightMessages: 2, MaxInflightBytes: 1024, MaxRuntimeKeysPerMessage: 2, MaxPendingKeyRefs: 2,
	}, partitionOffsetCommitterFunc(func(_ context.Context, nextOffset int64) error {
		commits <- nextOffset
		return nil
	}))

	if err := runner.Submit(30, []byte("first")); err != nil {
		t.Fatal(err)
	}
	if err := runner.Submit(40, []byte("second")); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, first.evaluateStarted, "first stateful task")
	awaitSignal(t, second.evaluateStarted, "second stateful task")
	close(second.evaluateRelease)
	assertNoInt64(t, commits, "offset commit past an earlier incomplete message")
	close(first.evaluateRelease)
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case nextOffset := <-commits:
		if nextOffset != 41 {
			t.Fatalf("committed next offset = %d, want 41 for the completed registered prefix", nextOffset)
		}
	default:
		t.Fatal("completed registered prefix was not committed")
	}
}

func TestConcurrentRunnerBackpressuresAtInflightBudget(t *testing.T) {
	t.Parallel()

	first := newBlockingRoutedTask(RuntimeKey{StrategyID: "1"})
	second := newBlockingRoutedTask(RuntimeKey{StrategyID: "2"})
	builder := &mapTaskBuilder{tasks: map[string]*blockingRoutedTask{"first": first, "second": second}}
	runner, err := NewConcurrentRoutedPartitionRunner(
		context.Background(), builder, completedCriticalPhase{}, partitionOffsetCommitterFunc(func(context.Context, int64) error { return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }), nil,
		ConcurrentRunnerLimits{PreparationWorkers: 1, StatefulWorkers: 1, MaxInflightMessages: 1, MaxInflightBytes: 1024, MaxRuntimeKeysPerMessage: 1, MaxPendingKeyRefs: 1}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Submit(50, []byte("first")); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, first.evaluateStarted, "first stateful task")
	secondSubmitted := make(chan error, 1)
	go func() { secondSubmitted <- runner.Submit(51, []byte("second")) }()
	select {
	case err := <-secondSubmitted:
		t.Fatalf("second Submit returned before capacity was released: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	if builder.BuildCount("second") != 0 {
		t.Fatal("backpressured message was built before inflight capacity was available")
	}
	close(first.evaluateRelease)
	select {
	case err := <-secondSubmitted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second Submit remained blocked after capacity was released")
	}
	awaitSignal(t, second.evaluateStarted, "second stateful task")
	close(second.evaluateRelease)
	if err := runner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRunnerResourcesBoundInflightAcrossPartitions(t *testing.T) {
	t.Parallel()

	limits := ConcurrentRunnerLimits{
		PreparationWorkers: 1, StatefulWorkers: 1, MaxInflightMessages: 1,
		MaxInflightBytes: 1024, MaxRuntimeKeysPerMessage: 1, MaxPendingKeyRefs: 1,
	}
	resources, err := NewConcurrentRunnerResources(limits)
	if err != nil {
		t.Fatal(err)
	}
	first := newBlockingRoutedTask(RuntimeKey{StrategyID: "1"})
	second := newBlockingRoutedTask(RuntimeKey{StrategyID: "2"})
	newRunner := func(task *blockingRoutedTask, payload string) *ConcurrentRoutedPartitionRunner {
		runner, err := NewConcurrentRoutedPartitionRunnerWithResources(
			context.Background(), &mapTaskBuilder{tasks: map[string]*blockingRoutedTask{payload: task}},
			completedCriticalPhase{}, partitionOffsetCommitterFunc(func(context.Context, int64) error { return nil }),
			receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }), nil, resources, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		return runner
	}
	firstRunner := newRunner(first, "first")
	secondRunner := newRunner(second, "second")
	if err := firstRunner.Submit(50, []byte("first")); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, first.evaluateStarted, "first partition task")
	secondSubmitted := make(chan error, 1)
	go func() { secondSubmitted <- secondRunner.Submit(70, []byte("second")) }()
	select {
	case err := <-secondSubmitted:
		t.Fatalf("second partition Submit returned before global capacity was released: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(first.evaluateRelease)
	select {
	case err := <-secondSubmitted:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("second partition Submit remained blocked after global capacity was released")
	}
	awaitSignal(t, second.evaluateStarted, "second partition task")
	close(second.evaluateRelease)
	if err := firstRunner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := secondRunner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRunnerResourcesBoundStatefulWorkersAcrossPartitions(t *testing.T) {
	t.Parallel()

	limits := ConcurrentRunnerLimits{
		PreparationWorkers: 2, StatefulWorkers: 1, MaxInflightMessages: 2,
		MaxInflightBytes: 1024, MaxRuntimeKeysPerMessage: 1, MaxPendingKeyRefs: 2,
	}
	resources, err := NewConcurrentRunnerResources(limits)
	if err != nil {
		t.Fatal(err)
	}
	first := newBlockingRoutedTask(RuntimeKey{StrategyID: "1"})
	second := newBlockingRoutedTask(RuntimeKey{StrategyID: "2"})
	newRunner := func(task *blockingRoutedTask, payload string) *ConcurrentRoutedPartitionRunner {
		runner, err := NewConcurrentRoutedPartitionRunnerWithResources(
			context.Background(), &mapTaskBuilder{tasks: map[string]*blockingRoutedTask{payload: task}},
			completedCriticalPhase{}, partitionOffsetCommitterFunc(func(context.Context, int64) error { return nil }),
			receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }), nil, resources, nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		return runner
	}
	firstRunner := newRunner(first, "first")
	secondRunner := newRunner(second, "second")
	if err := firstRunner.Submit(50, []byte("first")); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, first.evaluateStarted, "first partition stateful task")
	if err := secondRunner.Submit(70, []byte("second")); err != nil {
		t.Fatal(err)
	}
	assertNoSignal(t, second.evaluateStarted, "second partition stateful task before shared worker was released")
	close(first.evaluateRelease)
	awaitSignal(t, second.evaluateStarted, "second partition stateful task")
	close(second.evaluateRelease)
	if err := firstRunner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := secondRunner.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentRunnerReportsMessageRuntimeKeyBudgetBeforeExecution(t *testing.T) {
	t.Parallel()

	task := newBlockingRoutedTask(RuntimeKey{StrategyID: "1"}, RuntimeKey{StrategyID: "2"})
	runner := newConcurrentRunnerForTest(t, map[string]*blockingRoutedTask{"message": task}, ConcurrentRunnerLimits{
		PreparationWorkers: 1, StatefulWorkers: 1, MaxInflightMessages: 1, MaxInflightBytes: 1024,
		MaxRuntimeKeysPerMessage: 1, MaxPendingKeyRefs: 2,
	})
	if err := runner.Submit(60, []byte("message")); err != nil {
		t.Fatal(err)
	}
	if err := runner.Drain(context.Background()); err == nil || !strings.Contains(err.Error(), "key references exceed") {
		t.Fatalf("Drain() error = %v, want RuntimeKey budget failure", err)
	}
	assertNoSignal(t, task.evaluateStarted, "over-budget task")
}

func TestConcurrentRunnerDoesNotAdvanceStateOrOffsetAfterEventACKFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("event ACK failed")
	task := &immediateRoutedTask{
		keys: []RuntimeKey{{StrategyID: "1"}},
		outcome: MessageOutcome{Kind: MessageOutcomeCompleted, Message: &MessageResult{
			CriticalResult: CriticalResult{Events: []contract.TriggerEventV1{{EventID: "event"}}},
			Receipt:        &contract.MessageReceiptV1{},
		}},
	}
	stateCalls, commits := 0, 0
	critical := &recordingCriticalPhase{
		events: func(context.Context, []contract.TriggerEventV1) error { return want },
		state:  func(context.Context, state.WriteWindowsRequest) error { stateCalls++; return nil },
	}
	runner, err := NewConcurrentRoutedPartitionRunner(
		context.Background(), &immediateTaskBuilder{task: task}, critical,
		partitionOffsetCommitterFunc(func(context.Context, int64) error { commits++; return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }), nil,
		ConcurrentRunnerLimits{PreparationWorkers: 1, StatefulWorkers: 1, MaxInflightMessages: 1, MaxInflightBytes: 1024, MaxRuntimeKeysPerMessage: 1, MaxPendingKeyRefs: 1}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Submit(70, []byte("message")); err != nil {
		t.Fatal(err)
	}
	if err := runner.Drain(context.Background()); !errors.Is(err, want) {
		t.Fatalf("Drain() error = %v, want event ACK failure", err)
	}
	if stateCalls != 0 || commits != 0 {
		t.Fatalf("state calls = %d, commits = %d, want 0/0", stateCalls, commits)
	}
}

func TestConcurrentRunnerFailureDoesNotReleaseSameKeySuccessor(t *testing.T) {
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)

	for attempt := 0; attempt < 20; attempt++ {
		key := RuntimeKey{StrategyID: "1"}
		first := newBlockingRoutedTask(key)
		first.evaluateErr = errors.New("stateful failure")
		second := newBlockingRoutedTask(key)
		runner := newConcurrentRunnerForTest(t, map[string]*blockingRoutedTask{"first": first, "second": second}, ConcurrentRunnerLimits{
			PreparationWorkers: 2, StatefulWorkers: 2, MaxInflightMessages: 2, MaxInflightBytes: 1024,
			MaxRuntimeKeysPerMessage: 1, MaxPendingKeyRefs: 2,
		})
		if err := runner.Submit(80, []byte("first")); err != nil {
			t.Fatal(err)
		}
		if err := runner.Submit(81, []byte("second")); err != nil {
			t.Fatal(err)
		}
		awaitSignal(t, first.evaluateStarted, "failing stateful task")
		close(first.evaluateRelease)
		if err := runner.Drain(context.Background()); !errors.Is(err, first.evaluateErr) {
			t.Fatalf("Drain() error = %v, want %v", err, first.evaluateErr)
		}
		select {
		case <-second.evaluateStarted:
			t.Fatalf("attempt %d: same-key successor started after predecessor failure", attempt)
		default:
		}
	}
}

func TestConcurrentRunnerStopIsBoundedWhenTaskIgnoresCancellation(t *testing.T) {
	t.Parallel()

	task := &uncooperativePrepareTask{started: make(chan struct{}), release: make(chan struct{})}
	runner, err := NewConcurrentRoutedPartitionRunner(
		context.Background(), &immediateTaskBuilder{task: task}, completedCriticalPhase{},
		partitionOffsetCommitterFunc(func(context.Context, int64) error { return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }), nil,
		ConcurrentRunnerLimits{PreparationWorkers: 1, StatefulWorkers: 1, MaxInflightMessages: 1, MaxInflightBytes: 1024, MaxRuntimeKeysPerMessage: 1, MaxPendingKeyRefs: 1}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Submit(90, []byte("message")); err != nil {
		t.Fatal(err)
	}
	awaitSignal(t, task.started, "uncooperative preparation")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := runner.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop() error = %v, want bounded deadline", err)
	}
	close(task.release)
	if err := runner.Drain(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain() after release = %v, want canceled runner", err)
	}
}

func newConcurrentRunnerForTest(
	t *testing.T,
	tasks map[string]*blockingRoutedTask,
	limits ConcurrentRunnerLimits,
) *ConcurrentRoutedPartitionRunner {
	t.Helper()
	return newConcurrentRunnerForTestWithCommitter(t, tasks, limits, partitionOffsetCommitterFunc(func(context.Context, int64) error { return nil }))
}

func newConcurrentRunnerForTestWithCommitter(
	t *testing.T,
	tasks map[string]*blockingRoutedTask,
	limits ConcurrentRunnerLimits,
	committer PartitionOffsetCommitter,
) *ConcurrentRoutedPartitionRunner {
	t.Helper()
	runner, err := NewConcurrentRoutedPartitionRunner(
		context.Background(), &mapTaskBuilder{tasks: tasks}, completedCriticalPhase{}, committer,
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }), nil, limits, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

type mapTaskBuilder struct {
	mu     sync.Mutex
	tasks  map[string]*blockingRoutedTask
	counts map[string]int
}

func (builder *mapTaskBuilder) BuildMessageTask(_ context.Context, payload []byte) (RoutedMessageTask, error) {
	name := string(payload)
	builder.mu.Lock()
	defer builder.mu.Unlock()
	if builder.counts == nil {
		builder.counts = make(map[string]int)
	}
	builder.counts[name]++
	return builder.tasks[name], nil
}

func (builder *mapTaskBuilder) BuildCount(name string) int {
	builder.mu.Lock()
	defer builder.mu.Unlock()
	return builder.counts[name]
}

type blockingRoutedTask struct {
	keys            []RuntimeKey
	evaluateStarted chan struct{}
	evaluateRelease chan struct{}
	evaluateErr     error
	startOnce       sync.Once
}

type immediateTaskBuilder struct {
	task RoutedMessageTask
}

type blockingTaskBuilder struct {
	started chan struct{}
	release chan struct{}
	task    RoutedMessageTask
}

type outOfOrderTaskBuilder struct {
	tasks        map[string]RoutedMessageTask
	firstStarted chan struct{}
	firstRelease chan struct{}
	secondBuilt  chan struct{}
}

func (builder *outOfOrderTaskBuilder) BuildMessageTask(_ context.Context, payload []byte) (RoutedMessageTask, error) {
	name := string(payload)
	if name == "first" {
		close(builder.firstStarted)
		<-builder.firstRelease
	} else if name == "second" {
		close(builder.secondBuilt)
	}
	return builder.tasks[name], nil
}

func (builder *blockingTaskBuilder) BuildMessageTask(context.Context, []byte) (RoutedMessageTask, error) {
	close(builder.started)
	<-builder.release
	return builder.task, nil
}

func (builder *immediateTaskBuilder) BuildMessageTask(context.Context, []byte) (RoutedMessageTask, error) {
	return builder.task, nil
}

type immediateRoutedTask struct {
	keys    []RuntimeKey
	outcome MessageOutcome
}

type uncooperativePrepareTask struct {
	started chan struct{}
	release chan struct{}
}

func (*uncooperativePrepareTask) RuntimeKeys() []RuntimeKey { return []RuntimeKey{{StrategyID: "1"}} }

func (task *uncooperativePrepareTask) Prepare(context.Context) error {
	close(task.started)
	<-task.release
	return nil
}

func (*uncooperativePrepareTask) Evaluate(context.Context) (MessageOutcome, error) {
	return MessageOutcome{}, errors.New("Evaluate must not run after cancellation")
}

func (task *immediateRoutedTask) RuntimeKeys() []RuntimeKey {
	return append([]RuntimeKey(nil), task.keys...)
}

func (*immediateRoutedTask) Prepare(context.Context) error { return nil }

func (task *immediateRoutedTask) Evaluate(context.Context) (MessageOutcome, error) {
	return task.outcome, nil
}

type recordingCriticalPhase struct {
	events func(context.Context, []contract.TriggerEventV1) error
	state  func(context.Context, state.WriteWindowsRequest) error
}

func (completion *recordingCriticalPhase) Complete(ctx context.Context, result CriticalResult) error {
	if err := completion.CompleteEvents(ctx, result.Events); err != nil {
		return err
	}
	return completion.CompleteState(ctx, result.StateWrite)
}

func (completion *recordingCriticalPhase) CompleteEvents(ctx context.Context, events []contract.TriggerEventV1) error {
	return completion.events(ctx, events)
}

func (completion *recordingCriticalPhase) CompleteState(ctx context.Context, request state.WriteWindowsRequest) error {
	return completion.state(ctx, request)
}

func newBlockingRoutedTask(keys ...RuntimeKey) *blockingRoutedTask {
	return &blockingRoutedTask{keys: keys, evaluateStarted: make(chan struct{}), evaluateRelease: make(chan struct{})}
}

func (task *blockingRoutedTask) RuntimeKeys() []RuntimeKey {
	return append([]RuntimeKey(nil), task.keys...)
}

func (*blockingRoutedTask) Prepare(context.Context) error { return nil }

func (task *blockingRoutedTask) Evaluate(ctx context.Context) (MessageOutcome, error) {
	task.startOnce.Do(func() { close(task.evaluateStarted) })
	select {
	case <-task.evaluateRelease:
	case <-ctx.Done():
		return MessageOutcome{}, ctx.Err()
	}
	if task.evaluateErr != nil {
		return MessageOutcome{}, task.evaluateErr
	}
	return MessageOutcome{Kind: MessageOutcomeCompleted, Message: &MessageResult{Receipt: &contract.MessageReceiptV1{}}}, nil
}

type completedCriticalPhase struct{}

func (completedCriticalPhase) Complete(context.Context, CriticalResult) error { return nil }
func (completedCriticalPhase) CompleteEvents(context.Context, []contract.TriggerEventV1) error {
	return nil
}
func (completedCriticalPhase) CompleteState(context.Context, state.WriteWindowsRequest) error {
	return nil
}

func awaitSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

func assertNoSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
		t.Fatalf("%s started unexpectedly", name)
	case <-time.After(20 * time.Millisecond):
	}
}

func assertNoInt64(t *testing.T, values <-chan int64, name string) {
	t.Helper()
	select {
	case value := <-values:
		t.Fatalf("%s: %d", name, value)
	case <-time.After(20 * time.Millisecond):
	}
}
