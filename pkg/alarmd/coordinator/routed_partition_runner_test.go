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
