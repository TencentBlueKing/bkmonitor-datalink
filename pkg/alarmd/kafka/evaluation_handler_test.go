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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	alarmdcoordinator "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/coordinator"
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
	handler, err := NewEvaluationHandler(router, critical, offsets, receipts, gate, reportFatal)
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

type evaluationCriticalCompletionFunc func(context.Context, alarmdcoordinator.CriticalResult) error

func (function evaluationCriticalCompletionFunc) Complete(ctx context.Context, result alarmdcoordinator.CriticalResult) error {
	return function(ctx, result)
}

type evaluationReceiptPublisherFunc func(*contract.MessageReceiptV1) bool

func (function evaluationReceiptPublisherFunc) TryEnqueue(receipt *contract.MessageReceiptV1) bool {
	return function(receipt)
}
