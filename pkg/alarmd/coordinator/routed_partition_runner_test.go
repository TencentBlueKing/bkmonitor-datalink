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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

func TestRoutedPartitionRunnerCompletesRawMessageInOrder(t *testing.T) {
	t.Parallel()

	calls := []string{}
	runner := newTestRoutedPartitionRunner(t,
		messageOutcomeRouterFunc(func(_ context.Context, payload []byte) (MessageOutcome, error) {
			calls = append(calls, "route:"+string(payload))
			return MessageOutcome{Kind: MessageOutcomeCompleted, Message: &MessageResult{
				CriticalResult: CriticalResult{Events: []contract.TriggerEventV1{{EventID: "event-41"}}},
				Receipt:        &contract.MessageReceiptV1{MessageID: "message-41"},
			}}, nil
		}),
		criticalCompletionFunc(func(_ context.Context, result CriticalResult) error {
			calls = append(calls, "critical:"+result.Events[0].EventID)
			return nil
		}),
		partitionOffsetCommitterFunc(func(_ context.Context, next int64) error {
			calls = append(calls, "offset")
			if next != 42 {
				t.Fatalf("next offset = %d, want 42", next)
			}
			return nil
		}),
		receiptPublisherFunc(func(receipt *contract.MessageReceiptV1) bool {
			calls = append(calls, "receipt:"+receipt.MessageID)
			return true
		}),
	)

	if err := runner.Process(context.Background(), 41, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	want := []string{"route:payload", "critical:event-41", "offset", "receipt:message-41"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRoutedPartitionRunnerCommitsRejectedMessageWithoutBusinessReceipt(t *testing.T) {
	t.Parallel()

	criticalCalls, offsetCalls, receiptCalls := 0, 0, 0
	runner := newTestRoutedPartitionRunner(t,
		messageOutcomeRouterFunc(func(context.Context, []byte) (MessageOutcome, error) {
			return MessageOutcome{Kind: MessageOutcomeRejected, Rejected: &RejectedOutcome{}}, nil
		}),
		criticalCompletionFunc(func(context.Context, CriticalResult) error { criticalCalls++; return nil }),
		partitionOffsetCommitterFunc(func(context.Context, int64) error { offsetCalls++; return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { receiptCalls++; return true }),
	)

	if err := runner.Process(context.Background(), 7, []byte("bad")); err != nil {
		t.Fatal(err)
	}
	if criticalCalls != 0 || offsetCalls != 1 || receiptCalls != 0 {
		t.Fatalf("calls = critical:%d offset:%d receipt:%d, want 0/1/0", criticalCalls, offsetCalls, receiptCalls)
	}
}

func TestRoutedPartitionRunnerQueuesIdentifiedRejectedReceiptAfterOffsetCommit(t *testing.T) {
	t.Parallel()

	receipt := &contract.MessageReceiptV1{MessageID: "message-7", Status: contract.ReceiptStatusRejected}
	calls := make([]string, 0, 2)
	runner := newTestRoutedPartitionRunner(t,
		messageOutcomeRouterFunc(func(context.Context, []byte) (MessageOutcome, error) {
			return MessageOutcome{Kind: MessageOutcomeRejected, Rejected: &RejectedOutcome{Receipt: receipt}}, nil
		}),
		criticalCompletionFunc(func(context.Context, CriticalResult) error { return nil }),
		partitionOffsetCommitterFunc(func(context.Context, int64) error {
			calls = append(calls, "offset")
			return nil
		}),
		receiptPublisherFunc(func(got *contract.MessageReceiptV1) bool {
			if got != receipt {
				t.Fatalf("Receipt = %#v, want %#v", got, receipt)
			}
			calls = append(calls, "receipt")
			return true
		}),
	)

	if err := runner.Process(context.Background(), 7, []byte("bad")); err != nil {
		t.Fatal(err)
	}
	if want := []string{"offset", "receipt"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRoutedPartitionRunnerRecordsRejectedEvidenceBeforeOffset(t *testing.T) {
	t.Parallel()

	calls := []string{}
	runner, err := NewRoutedPartitionRunnerWithObserver(
		messageOutcomeRouterFunc(func(context.Context, []byte) (MessageOutcome, error) {
			return MessageOutcome{Kind: MessageOutcomeRejected, Rejected: &RejectedOutcome{}}, nil
		}),
		criticalCompletionFunc(func(context.Context, CriticalResult) error { return nil }),
		partitionOffsetCommitterFunc(func(context.Context, int64) error {
			calls = append(calls, "offset")
			return nil
		}),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
		rejectedMessageObserverFunc(func(offset int64, _ RejectedOutcome) {
			calls = append(calls, "rejected")
			if offset != 17 {
				t.Fatalf("rejected offset = %d, want 17", offset)
			}
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.Process(context.Background(), 17, []byte("bad")); err != nil {
		t.Fatal(err)
	}
	if want := []string{"rejected", "offset"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestRoutedPartitionRunnerRetriesRegisteredRoutingFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("Redis unavailable")
	routeCalls, offsetCalls := 0, 0
	runner := newTestRoutedPartitionRunner(t,
		messageOutcomeRouterFunc(func(context.Context, []byte) (MessageOutcome, error) {
			routeCalls++
			if routeCalls == 1 {
				return MessageOutcome{}, want
			}
			return MessageOutcome{Kind: MessageOutcomeCompleted, Message: &MessageResult{
				Receipt: &contract.MessageReceiptV1{MessageID: "message-9"},
			}}, nil
		}),
		criticalCompletionFunc(func(context.Context, CriticalResult) error { return nil }),
		partitionOffsetCommitterFunc(func(context.Context, int64) error { offsetCalls++; return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
	)

	if err := runner.Process(context.Background(), 9, []byte("payload")); !errors.Is(err, want) {
		t.Fatalf("Process() error = %v, want %v", err, want)
	}
	if err := runner.Process(context.Background(), 9, []byte("payload")); err == nil {
		t.Fatal("duplicate Process() bypassed registration")
	}
	if err := runner.Retry(context.Background(), 9, []byte("payload")); err != nil {
		t.Fatal(err)
	}
	if routeCalls != 2 || offsetCalls != 1 {
		t.Fatalf("calls = route:%d offset:%d, want 2/1", routeCalls, offsetCalls)
	}
}

func TestRoutedPartitionRunnerResumesAfterEventACKWithoutRerouting(t *testing.T) {
	t.Parallel()

	want := errors.New("Redis unavailable")
	routeCalls, eventCalls, stateCalls, offsetCalls := 0, 0, 0, 0
	critical := &criticalPhaseCompletionSpy{
		events: func(context.Context, []contract.TriggerEventV1) error { eventCalls++; return nil },
		state: func(context.Context, state.WriteWindowsRequest) error {
			stateCalls++
			if stateCalls == 1 {
				return want
			}
			return nil
		},
	}
	runner := newTestRoutedPartitionRunner(t,
		messageOutcomeRouterFunc(func(context.Context, []byte) (MessageOutcome, error) {
			routeCalls++
			return MessageOutcome{Kind: MessageOutcomeCompleted, Message: &MessageResult{
				CriticalResult: CriticalResult{Events: []contract.TriggerEventV1{{EventID: "event-21"}}},
				Receipt:        &contract.MessageReceiptV1{MessageID: "message-21"},
			}}, nil
		}),
		critical,
		partitionOffsetCommitterFunc(func(context.Context, int64) error { offsetCalls++; return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
	)
	payload := []byte("payload")
	if err := runner.Process(context.Background(), 21, payload); !errors.Is(err, want) {
		t.Fatalf("Process() error = %v, want %v", err, want)
	}
	if err := runner.Retry(context.Background(), 21, payload); err != nil {
		t.Fatal(err)
	}
	if routeCalls != 1 || eventCalls != 1 || stateCalls != 2 || offsetCalls != 1 {
		t.Fatalf("calls route=%d event=%d state=%d offset=%d, want 1/1/2/1", routeCalls, eventCalls, stateCalls, offsetCalls)
	}
}

func TestRoutedPartitionRunnerRejectsChangedRetryPayload(t *testing.T) {
	t.Parallel()

	want := errors.New("provider unavailable")
	runner := newTestRoutedPartitionRunner(t,
		messageOutcomeRouterFunc(func(context.Context, []byte) (MessageOutcome, error) { return MessageOutcome{}, want }),
		criticalCompletionFunc(func(context.Context, CriticalResult) error { return nil }),
		partitionOffsetCommitterFunc(func(context.Context, int64) error { return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
	)
	if err := runner.Process(context.Background(), 5, []byte("first")); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if err := runner.Retry(context.Background(), 5, []byte("changed")); err == nil {
		t.Fatal("Retry() accepted a changed payload")
	}
}

func newTestRoutedPartitionRunner(
	t testing.TB,
	router MessageOutcomeRouter,
	critical CriticalCompletion,
	offsets PartitionOffsetCommitter,
	receipts ReceiptPublisher,
) *RoutedPartitionRunner {
	t.Helper()
	runner, err := NewRoutedPartitionRunner(router, critical, offsets, receipts)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

type messageOutcomeRouterFunc func(context.Context, []byte) (MessageOutcome, error)

func (function messageOutcomeRouterFunc) Route(ctx context.Context, payload []byte) (MessageOutcome, error) {
	return function(ctx, payload)
}

type rejectedMessageObserverFunc func(int64, RejectedOutcome)

func (function rejectedMessageObserverFunc) ObserveRejected(offset int64, rejected RejectedOutcome) {
	function(offset, rejected)
}

type criticalPhaseCompletionSpy struct {
	events func(context.Context, []contract.TriggerEventV1) error
	state  func(context.Context, state.WriteWindowsRequest) error
}

func (spy *criticalPhaseCompletionSpy) Complete(ctx context.Context, result CriticalResult) error {
	if err := spy.CompleteEvents(ctx, result.Events); err != nil {
		return err
	}
	return spy.CompleteState(ctx, result.StateWrite)
}

func (spy *criticalPhaseCompletionSpy) CompleteEvents(ctx context.Context, events []contract.TriggerEventV1) error {
	return spy.events(ctx, events)
}

func (spy *criticalPhaseCompletionSpy) CompleteState(ctx context.Context, request state.WriteWindowsRequest) error {
	return spy.state(ctx, request)
}
