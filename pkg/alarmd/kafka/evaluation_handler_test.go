// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/consumer"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	alarmdcoordinator "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

func TestEvaluationHandlerCompletesRawMessageBeforeOffsetAndReceipt(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, 5)
	session := newFakeSession(context.Background(), &events)
	claim := newFakeClaim("execution-envelope", 2, []*sarama.ConsumerMessage{{
		Topic: "execution-envelope", Partition: 2, Offset: 41, Value: []byte("payload"),
	}})
	handler := newTestEvaluationHandler(t,
		evaluationMessageRouterFunc(func(_ context.Context, payload []byte) (alarmdcoordinator.MessageOutcome, error) {
			events = append(events, "route:"+string(payload))
			return alarmdcoordinator.MessageOutcome{Kind: alarmdcoordinator.MessageOutcomeCompleted, Message: &alarmdcoordinator.MessageResult{
				CriticalResult: alarmdcoordinator.CriticalResult{Events: []contract.TriggerEventV1{{EventID: "event-41"}}},
				Receipt:        &contract.MessageReceiptV1{MessageID: "message-41"},
			}}, nil
		}),
		evaluationCriticalCompletionFunc(func(_ context.Context, result alarmdcoordinator.CriticalResult) error {
			events = append(events, "critical:"+result.Events[0].EventID)
			return nil
		}),
		fakeSyncOffsetCommitter{events: &events},
		evaluationReceiptPublisherFunc(func(receipt *contract.MessageReceiptV1) bool {
			events = append(events, "receipt:"+receipt.MessageID)
			return true
		}),
		alarmdcoordinator.NewCriticalDependencyGate(nil), nil,
	)
	setupEvaluationHandler(t, handler, session, claim)

	if err := handler.ConsumeClaim(session, claim); err != nil {
		t.Fatal(err)
	}
	want := []string{"route:payload", "critical:event-41", "commit", "mark", "receipt:message-41"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if session.markedOffset != 42 {
		t.Fatalf("marked offset = %d, want 42", session.markedOffset)
	}
}

func TestEvaluationHandlerRunsDisjointRuntimeKeysConcurrently(t *testing.T) {
	t.Parallel()

	first := newHandlerBlockingTask(alarmdcoordinator.RuntimeKey{StrategyID: "1"})
	second := newHandlerBlockingTask(alarmdcoordinator.RuntimeKey{StrategyID: "2"})
	router := &evaluationTaskRouter{tasks: map[string]*handlerBlockingTask{"first": first, "second": second}}
	events := []string{}
	session := newFakeSession(context.Background(), &events)
	claim := newFakeClaim("execution-envelope", 0, []*sarama.ConsumerMessage{
		{Topic: "execution-envelope", Offset: 10, Value: []byte("first")},
		{Topic: "execution-envelope", Offset: 11, Value: []byte("second")},
	})
	handler := newTestEvaluationHandler(
		t, router,
		evaluationCriticalCompletionFunc(func(context.Context, alarmdcoordinator.CriticalResult) error { return nil }),
		fakeSyncOffsetCommitter{events: &events},
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
		alarmdcoordinator.NewCriticalDependencyGate(nil), nil,
	)
	setupEvaluationHandler(t, handler, session, claim)

	done := make(chan error, 1)
	go func() { done <- handler.ConsumeClaim(session, claim) }()
	awaitHandlerSignal(t, first.started, "first RuntimeKey")
	awaitHandlerSignal(t, second.started, "disjoint RuntimeKey")
	close(first.release)
	close(second.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ConsumeClaim did not drain concurrent work")
	}
	if session.markedOffset != 12 {
		t.Fatalf("marked offset = %d, want 12", session.markedOffset)
	}
}

func TestEvaluationPartitionOffsetCommitterReportsFinalMark(t *testing.T) {
	t.Parallel()

	gate := alarmdcoordinator.NewCriticalDependencyGate(nil)
	retry, err := alarmdcoordinator.NewDependencyRetry(gate, alarmdcoordinator.DependencyBlocker{
		Dependency: alarmdcoordinator.DependencyInputKafka, ReasonCode: contract.ReasonKafkaUnavailable,
	}, evaluationRetryConfig())
	if err != nil {
		t.Fatal(err)
	}
	session := newFakeSession(context.Background(), &[]string{})
	events := []string{}
	var evidence OffsetMarkEvidence
	committer := evaluationPartitionOffsetCommitter{
		session: session, topic: "execution-envelope", partition: 3,
		offsets: fakeSyncOffsetCommitter{events: &events}, retry: retry,
		onMarked: func(value OffsetMarkEvidence) { evidence = value },
	}
	if err := committer.CommitThrough(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if evidence.Topic != "execution-envelope" || evidence.Partition != 3 || evidence.NextOffset != 42 ||
		evidence.Err != nil || evidence.Duration < 0 {
		t.Fatalf("offset mark evidence = %#v", evidence)
	}
}

func TestEvaluationHandlerRetriesOffsetBrokerFailureWithoutRepeatingCriticalPhases(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, 9)
	gate := alarmdcoordinator.NewCriticalDependencyGate(func(transition alarmdcoordinator.DependencyGateTransition) {
		if transition.Current.State == alarmdcoordinator.DependencyGatePaused {
			events = append(events, "paused")
		} else {
			events = append(events, "resumed")
		}
	})
	session := newFakeSession(context.Background(), &events)
	claim := newFakeClaim("execution-envelope", 2, []*sarama.ConsumerMessage{{
		Topic: "execution-envelope", Partition: 2, Offset: 41, Value: []byte("payload"),
	}})
	commitAttempts := 0
	offsets := evaluationOffsetCommitterFunc(func(context.Context, sarama.ConsumerGroupSession, consumer.Record) error {
		commitAttempts++
		if commitAttempts == 1 {
			events = append(events, "commit_failed")
			return &offsetCommitDependencyError{err: errors.New("broker unavailable")}
		}
		events = append(events, "commit_acked")
		return nil
	})
	critical := &evaluationCriticalPhaseCompletion{
		events: func(context.Context, []contract.TriggerEventV1) error {
			events = append(events, "events")
			return nil
		},
		state: func(context.Context, state.WriteWindowsRequest) error {
			events = append(events, "state")
			return nil
		},
	}
	handler := newTestEvaluationHandler(t,
		evaluationMessageRouterFunc(func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
			events = append(events, "route")
			return alarmdcoordinator.MessageOutcome{Kind: alarmdcoordinator.MessageOutcomeCompleted, Message: &alarmdcoordinator.MessageResult{
				CriticalResult: alarmdcoordinator.CriticalResult{Events: []contract.TriggerEventV1{{EventID: "event-41"}}},
				Receipt:        &contract.MessageReceiptV1{MessageID: "message-41"},
			}}, nil
		}), critical, offsets,
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool {
			events = append(events, "receipt")
			return true
		}), gate, nil,
	)
	setupEvaluationHandler(t, handler, session, claim)

	if err := handler.ConsumeClaim(session, claim); err != nil {
		t.Fatal(err)
	}
	want := []string{"route", "events", "state", "commit_failed", "paused", "commit_acked", "resumed", "mark", "receipt"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if commitAttempts != 2 || !gate.Ready() {
		t.Fatalf("commit attempts = %d, gate = %#v", commitAttempts, gate.Snapshot())
	}
}

func TestEvaluationOffsetRetryCancellationPreservesBrokerRootCause(t *testing.T) {
	t.Parallel()

	gate := alarmdcoordinator.NewCriticalDependencyGate(nil)
	retry, err := alarmdcoordinator.NewDependencyRetry(gate, alarmdcoordinator.DependencyBlocker{
		Dependency: alarmdcoordinator.DependencyInputKafka, ReasonCode: contract.ReasonKafkaUnavailable,
	}, evaluationRetryConfig())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	root := errors.New("broker unavailable")
	session := newFakeSession(ctx, &[]string{})
	committer := evaluationPartitionOffsetCommitter{
		session: session, topic: "execution-envelope", offsets: evaluationOffsetCommitterFunc(
			func(context.Context, sarama.ConsumerGroupSession, consumer.Record) error {
				cancel()
				return &offsetCommitDependencyError{err: root}
			},
		), retry: retry,
	}
	err = committer.CommitThrough(ctx, 42)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, root) {
		t.Fatalf("CommitThrough() error = %v, want cancellation and broker root cause", err)
	}
	if session.markedOffset != 0 || gate.Ready() {
		t.Fatalf("marked offset = %d, gate = %#v", session.markedOffset, gate.Snapshot())
	}
}

func TestEvaluationHandlerReportsLocalOffsetInvariantAsFatal(t *testing.T) {
	t.Parallel()

	root := errors.New("local offset invariant")
	commitAttempts, eventCalls, stateCalls, receiptCalls := 0, 0, 0, 0
	fatal := make(chan error, 1)
	session := newFakeSession(context.Background(), &[]string{})
	claim := newFakeClaim("execution-envelope", 0, []*sarama.ConsumerMessage{{Topic: "execution-envelope", Offset: 1}})
	critical := &evaluationCriticalPhaseCompletion{
		events: func(context.Context, []contract.TriggerEventV1) error { eventCalls++; return nil },
		state:  func(context.Context, state.WriteWindowsRequest) error { stateCalls++; return nil },
	}
	handler := newTestEvaluationHandler(t,
		evaluationMessageRouterFunc(func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
			return alarmdcoordinator.MessageOutcome{Kind: alarmdcoordinator.MessageOutcomeCompleted, Message: &alarmdcoordinator.MessageResult{
				Receipt: &contract.MessageReceiptV1{MessageID: "message-1"},
			}}, nil
		}), critical,
		evaluationOffsetCommitterFunc(func(context.Context, sarama.ConsumerGroupSession, consumer.Record) error {
			commitAttempts++
			return root
		}),
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { receiptCalls++; return true }),
		alarmdcoordinator.NewCriticalDependencyGate(nil), func(err error) { fatal <- err },
	)
	setupEvaluationHandler(t, handler, session, claim)

	err := handler.ConsumeClaim(session, claim)
	if !errors.Is(err, root) {
		t.Fatalf("ConsumeClaim() error = %v, want local invariant", err)
	}
	if commitAttempts != 1 || eventCalls != 1 || stateCalls != 1 || receiptCalls != 0 || session.markedOffset != 0 {
		t.Fatalf("attempts=%d events=%d state=%d receipts=%d marked=%d", commitAttempts, eventCalls, stateCalls, receiptCalls, session.markedOffset)
	}
	select {
	case err := <-fatal:
		if !errors.Is(err, root) {
			t.Fatalf("fatal = %v, want local invariant", err)
		}
	default:
		t.Fatal("local offset invariant was not reported as fatal")
	}
}

func TestEvaluationHandlerCommitsRejectedMessageWithoutBusinessReceipt(t *testing.T) {
	t.Parallel()

	events := make([]string, 0, 2)
	session := newFakeSession(context.Background(), &events)
	claim := newFakeClaim("execution-envelope", 0, []*sarama.ConsumerMessage{{Topic: "execution-envelope", Offset: 7}})
	criticalCalls, receiptCalls := 0, 0
	handler := newTestEvaluationHandler(t,
		evaluationMessageRouterFunc(func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
			return alarmdcoordinator.MessageOutcome{Kind: alarmdcoordinator.MessageOutcomeRejected, Rejected: &alarmdcoordinator.RejectedOutcome{}}, nil
		}),
		evaluationCriticalCompletionFunc(func(context.Context, alarmdcoordinator.CriticalResult) error { criticalCalls++; return nil }),
		fakeSyncOffsetCommitter{events: &events},
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { receiptCalls++; return true }),
		alarmdcoordinator.NewCriticalDependencyGate(nil), nil,
	)
	setupEvaluationHandler(t, handler, session, claim)

	if err := handler.ConsumeClaim(session, claim); err != nil {
		t.Fatal(err)
	}
	if criticalCalls != 0 || receiptCalls != 0 || !reflect.DeepEqual(events, []string{"commit", "mark"}) {
		t.Fatalf("critical=%d receipt=%d events=%v, want 0/0/commit+mark", criticalCalls, receiptCalls, events)
	}
}

func TestEvaluationHandlerReportsRejectedTransportEvidence(t *testing.T) {
	t.Parallel()

	events := []string{}
	session := newFakeSession(context.Background(), &events)
	claim := newFakeClaim("execution-envelope", 3, []*sarama.ConsumerMessage{{Topic: "execution-envelope", Partition: 3, Offset: 12}})
	var evidence RejectedMessageEvidence
	handler, err := NewEvaluationHandlerWithDiagnostics(
		evaluationMessageRouterFunc(func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
			return alarmdcoordinator.MessageOutcome{Kind: alarmdcoordinator.MessageOutcomeRejected, Rejected: &alarmdcoordinator.RejectedOutcome{
				Terminals: []inputv2.Terminal{{ReasonCode: contract.ReasonMalformedJSON}},
			}}, nil
		}),
		evaluationCriticalCompletionFunc(func(context.Context, alarmdcoordinator.CriticalResult) error { return nil }),
		fakeSyncOffsetCommitter{events: &events},
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
		alarmdcoordinator.NewCriticalDependencyGate(nil), evaluationRetryConfig(),
		EvaluationDiagnostics{OnRejected: func(value RejectedMessageEvidence) {
			events = append(events, "rejected")
			evidence = value
		}}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	setupEvaluationHandler(t, handler, session, claim)
	if err := handler.ConsumeClaim(session, claim); err != nil {
		t.Fatal(err)
	}
	if want := []string{"rejected", "commit", "mark"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	if evidence.Topic != "execution-envelope" || evidence.Partition != 3 || evidence.Offset != 12 ||
		!reflect.DeepEqual(evidence.ReasonCodes, []string{contract.ReasonMalformedJSON}) {
		t.Fatalf("rejected evidence = %#v", evidence)
	}
}

func TestNewEvaluationHandlerRejectsMissingRejectedEvidenceObserver(t *testing.T) {
	t.Parallel()

	_, err := NewEvaluationHandlerWithDiagnostics(
		evaluationMessageRouterFunc(func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
			return alarmdcoordinator.MessageOutcome{}, nil
		}),
		evaluationCriticalCompletionFunc(func(context.Context, alarmdcoordinator.CriticalResult) error { return nil }),
		fakeSyncOffsetCommitter{},
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
		alarmdcoordinator.NewCriticalDependencyGate(nil), evaluationRetryConfig(), EvaluationDiagnostics{}, nil,
	)
	if err == nil {
		t.Fatal("NewEvaluationHandlerWithDiagnostics() accepted a missing rejected evidence observer")
	}
}

func TestEvaluationHandlerWaitsForDependencyGateBeforeAdmission(t *testing.T) {
	t.Parallel()

	gate := alarmdcoordinator.NewCriticalDependencyGate(nil)
	if _, err := gate.Pause(alarmdcoordinator.DependencyBlocker{
		Dependency: alarmdcoordinator.DependencyRedis, ReasonCode: "REDIS_UNAVAILABLE",
	}); err != nil {
		t.Fatal(err)
	}
	var routes atomic.Int32
	events := []string{}
	session := newFakeSession(context.Background(), &events)
	claim := newFakeClaim("execution-envelope", 0, []*sarama.ConsumerMessage{{Topic: "execution-envelope", Offset: 1}})
	handler := newTestEvaluationHandler(t,
		evaluationMessageRouterFunc(func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
			routes.Add(1)
			return alarmdcoordinator.MessageOutcome{Kind: alarmdcoordinator.MessageOutcomeRejected, Rejected: &alarmdcoordinator.RejectedOutcome{}}, nil
		}),
		evaluationCriticalCompletionFunc(func(context.Context, alarmdcoordinator.CriticalResult) error { return nil }),
		fakeSyncOffsetCommitter{events: &events},
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }), gate, nil,
	)
	setupEvaluationHandler(t, handler, session, claim)

	done := make(chan error, 1)
	go func() { done <- handler.ConsumeClaim(session, claim) }()
	time.Sleep(10 * time.Millisecond)
	if routes.Load() != 0 {
		t.Fatal("message was admitted while the critical dependency gate was paused")
	}
	if _, err := gate.Resume(alarmdcoordinator.DependencyRedis); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("claim did not resume after the dependency recovered")
	}
	if routes.Load() != 1 {
		t.Fatalf("routes = %d, want 1", routes.Load())
	}
}

func TestEvaluationHandlerReportsInternalRoutingFailureAsFatal(t *testing.T) {
	t.Parallel()

	want := errors.New("internal invariant")
	fatal := make(chan error, 1)
	session := newFakeSession(context.Background(), &[]string{})
	claim := newFakeClaim("execution-envelope", 0, []*sarama.ConsumerMessage{{Topic: "execution-envelope", Offset: 1}})
	handler := newTestEvaluationHandler(t,
		evaluationMessageRouterFunc(func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
			return alarmdcoordinator.MessageOutcome{}, want
		}),
		evaluationCriticalCompletionFunc(func(context.Context, alarmdcoordinator.CriticalResult) error { return nil }),
		fakeSyncOffsetCommitter{},
		evaluationReceiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
		alarmdcoordinator.NewCriticalDependencyGate(nil), func(err error) { fatal <- err },
	)
	setupEvaluationHandler(t, handler, session, claim)

	if err := handler.ConsumeClaim(session, claim); !errors.Is(err, want) {
		t.Fatalf("ConsumeClaim() error = %v, want %v", err, want)
	}
	select {
	case err := <-fatal:
		if !errors.Is(err, want) {
			t.Fatalf("fatal = %v, want %v", err, want)
		}
	default:
		t.Fatal("internal routing failure was not reported as fatal")
	}
}

func newTestEvaluationHandler(
	t testing.TB,
	router alarmdcoordinator.MessageOutcomeRouter,
	critical alarmdcoordinator.CriticalCompletion,
	offsets OffsetCommitter,
	receipts alarmdcoordinator.ReceiptPublisher,
	gate *alarmdcoordinator.CriticalDependencyGate,
	reportFatal func(error),
) *EvaluationHandler {
	t.Helper()
	handler, err := NewEvaluationHandlerWithDiagnostics(
		router, critical, offsets, receipts, gate, evaluationRetryConfig(),
		EvaluationDiagnostics{OnRejected: func(RejectedMessageEvidence) {}}, reportFatal,
	)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func setupEvaluationHandler(t testing.TB, handler *EvaluationHandler, session *fakeSession, claims ...*fakeClaim) {
	t.Helper()
	session.claims = make(map[string][]int32)
	for _, claim := range claims {
		session.claims[claim.topic] = append(session.claims[claim.topic], claim.partition)
	}
	if err := handler.Setup(session); err != nil {
		t.Fatal(err)
	}
}

type evaluationMessageRouterFunc func(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error)

func (function evaluationMessageRouterFunc) Route(ctx context.Context, payload []byte) (alarmdcoordinator.MessageOutcome, error) {
	return function(ctx, payload)
}

type evaluationTaskRouter struct {
	tasks map[string]*handlerBlockingTask
}

func (*evaluationTaskRouter) Route(context.Context, []byte) (alarmdcoordinator.MessageOutcome, error) {
	return alarmdcoordinator.MessageOutcome{}, errors.New("legacy route must not be used for staged tasks")
}

func (router *evaluationTaskRouter) BuildMessageTask(
	_ context.Context,
	payload []byte,
) (alarmdcoordinator.RoutedMessageTask, error) {
	return router.tasks[string(payload)], nil
}

type handlerBlockingTask struct {
	keys    []alarmdcoordinator.RuntimeKey
	started chan struct{}
	release chan struct{}
}

func newHandlerBlockingTask(keys ...alarmdcoordinator.RuntimeKey) *handlerBlockingTask {
	return &handlerBlockingTask{keys: keys, started: make(chan struct{}), release: make(chan struct{})}
}

func (task *handlerBlockingTask) RuntimeKeys() []alarmdcoordinator.RuntimeKey {
	return append([]alarmdcoordinator.RuntimeKey(nil), task.keys...)
}

func (*handlerBlockingTask) Prepare(context.Context) error { return nil }

func (task *handlerBlockingTask) Evaluate(ctx context.Context) (alarmdcoordinator.MessageOutcome, error) {
	close(task.started)
	select {
	case <-task.release:
	case <-ctx.Done():
		return alarmdcoordinator.MessageOutcome{}, ctx.Err()
	}
	return alarmdcoordinator.MessageOutcome{
		Kind:    alarmdcoordinator.MessageOutcomeCompleted,
		Message: &alarmdcoordinator.MessageResult{Receipt: &contract.MessageReceiptV1{}},
	}, nil
}

func awaitHandlerSignal(t *testing.T, signal <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}

type evaluationCriticalCompletionFunc func(context.Context, alarmdcoordinator.CriticalResult) error

func (function evaluationCriticalCompletionFunc) Complete(ctx context.Context, result alarmdcoordinator.CriticalResult) error {
	return function(ctx, result)
}

type evaluationCriticalPhaseCompletion struct {
	events func(context.Context, []contract.TriggerEventV1) error
	state  func(context.Context, state.WriteWindowsRequest) error
}

func (completion *evaluationCriticalPhaseCompletion) Complete(ctx context.Context, result alarmdcoordinator.CriticalResult) error {
	if err := completion.CompleteEvents(ctx, result.Events); err != nil {
		return err
	}
	return completion.CompleteState(ctx, result.StateWrite)
}

func (completion *evaluationCriticalPhaseCompletion) CompleteEvents(ctx context.Context, events []contract.TriggerEventV1) error {
	return completion.events(ctx, events)
}

func (completion *evaluationCriticalPhaseCompletion) CompleteState(ctx context.Context, request state.WriteWindowsRequest) error {
	return completion.state(ctx, request)
}

type evaluationOffsetCommitterFunc func(context.Context, sarama.ConsumerGroupSession, consumer.Record) error

func (function evaluationOffsetCommitterFunc) CommitOffset(
	ctx context.Context,
	session sarama.ConsumerGroupSession,
	record consumer.Record,
) error {
	return function(ctx, session, record)
}

func evaluationRetryConfig() alarmdcoordinator.DependencyRetryConfig {
	return alarmdcoordinator.DependencyRetryConfig{MinDelay: time.Nanosecond, MaxDelay: time.Nanosecond}
}

type evaluationReceiptPublisherFunc func(*contract.MessageReceiptV1) bool

func (function evaluationReceiptPublisherFunc) TryEnqueue(receipt *contract.MessageReceiptV1) bool {
	return function(receipt)
}
