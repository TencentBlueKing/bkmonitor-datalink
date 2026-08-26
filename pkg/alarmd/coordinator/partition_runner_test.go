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
	inputv2 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/input/adapter/v2"
)

func TestPartitionRunnerCompletesCriticalPathBeforeOffsetAndReceipt(t *testing.T) {
	t.Parallel()

	calls := []string{}
	runner := newTestPartitionRunner(t,
		messageEvaluatorFunc(func(context.Context, *inputv2.EvaluationInput) (MessageResult, error) {
			calls = append(calls, "evaluate")
			return MessageResult{Receipt: &contract.MessageReceiptV1{MessageID: "message-41"}}, nil
		}),
		criticalCompletionFunc(func(context.Context, CriticalResult) error {
			calls = append(calls, "critical")
			return nil
		}),
		partitionOffsetCommitterFunc(func(context.Context, int64) error {
			calls = append(calls, "offset")
			return nil
		}),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool {
			calls = append(calls, "receipt")
			return true
		}),
	)
	if err := runner.Process(context.Background(), 41, &inputv2.EvaluationInput{}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"evaluate", "critical", "offset", "receipt"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestPartitionRunnerRetriesOnlyOffsetAfterCommitFailure(t *testing.T) {
	t.Parallel()

	want := errors.New("offset unavailable")
	evaluateCalls, criticalCalls, offsetCalls, receiptCalls := 0, 0, 0, 0
	runner := newTestPartitionRunner(t,
		messageEvaluatorFunc(func(context.Context, *inputv2.EvaluationInput) (MessageResult, error) {
			evaluateCalls++
			return MessageResult{Receipt: &contract.MessageReceiptV1{MessageID: "message-41"}}, nil
		}),
		criticalCompletionFunc(func(context.Context, CriticalResult) error { criticalCalls++; return nil }),
		partitionOffsetCommitterFunc(func(context.Context, int64) error {
			offsetCalls++
			if offsetCalls == 1 {
				return want
			}
			return nil
		}),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { receiptCalls++; return false }),
	)
	if err := runner.Process(context.Background(), 41, &inputv2.EvaluationInput{}); !errors.Is(err, want) {
		t.Fatalf("Process() error = %v, want %v", err, want)
	}
	if err := runner.CommitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if evaluateCalls != 1 || criticalCalls != 1 || offsetCalls != 2 || receiptCalls != 1 {
		t.Fatalf("calls = evaluate:%d critical:%d offset:%d receipt:%d", evaluateCalls, criticalCalls, offsetCalls, receiptCalls)
	}
}

func TestPartitionRunnerRetriesEvaluationOnRegisteredOffset(t *testing.T) {
	t.Parallel()

	want := errors.New("provider unavailable")
	evaluateCalls, criticalCalls, offsetCalls := 0, 0, 0
	runner := newTestPartitionRunner(t,
		messageEvaluatorFunc(func(context.Context, *inputv2.EvaluationInput) (MessageResult, error) {
			evaluateCalls++
			if evaluateCalls == 1 {
				return MessageResult{}, want
			}
			return MessageResult{Receipt: &contract.MessageReceiptV1{MessageID: "message-41"}}, nil
		}),
		criticalCompletionFunc(func(context.Context, CriticalResult) error { criticalCalls++; return nil }),
		partitionOffsetCommitterFunc(func(context.Context, int64) error { offsetCalls++; return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
	)
	input := &inputv2.EvaluationInput{}
	if err := runner.Process(context.Background(), 41, input); !errors.Is(err, want) {
		t.Fatalf("Process() error = %v, want %v", err, want)
	}
	if err := runner.Process(context.Background(), 41, input); err == nil {
		t.Fatal("duplicate Process() bypassed offset registration discipline")
	}
	if evaluateCalls != 1 {
		t.Fatalf("duplicate Process() evaluation calls = %d, want 1", evaluateCalls)
	}
	if err := runner.Retry(context.Background(), 41, input); err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if evaluateCalls != 2 || criticalCalls != 1 || offsetCalls != 1 {
		t.Fatalf("calls = evaluate:%d critical:%d offset:%d", evaluateCalls, criticalCalls, offsetCalls)
	}
}

func TestPartitionRunnerRetriesCriticalCompletionOnRegisteredOffset(t *testing.T) {
	t.Parallel()

	want := errors.New("event output unavailable")
	evaluateCalls, criticalCalls, offsetCalls := 0, 0, 0
	runner := newTestPartitionRunner(t,
		messageEvaluatorFunc(func(context.Context, *inputv2.EvaluationInput) (MessageResult, error) {
			evaluateCalls++
			return MessageResult{
				CriticalResult: CriticalResult{Events: []contract.TriggerEventV1{{EventID: "stable-event-1"}}},
				Receipt:        &contract.MessageReceiptV1{MessageID: "message-41"},
			}, nil
		}),
		criticalCompletionFunc(func(_ context.Context, result CriticalResult) error {
			criticalCalls++
			if len(result.Events) != 1 || result.Events[0].EventID != "stable-event-1" {
				t.Fatalf("critical retry changed stable event identity: %#v", result.Events)
			}
			if criticalCalls == 1 {
				return want
			}
			return nil
		}),
		partitionOffsetCommitterFunc(func(context.Context, int64) error { offsetCalls++; return nil }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { return true }),
	)
	input := &inputv2.EvaluationInput{}
	if err := runner.Process(context.Background(), 41, input); !errors.Is(err, want) {
		t.Fatalf("Process() error = %v, want %v", err, want)
	}
	if err := runner.Retry(context.Background(), 41, input); err != nil {
		t.Fatalf("Retry() error = %v", err)
	}
	if evaluateCalls != 2 || criticalCalls != 2 || offsetCalls != 1 {
		t.Fatalf("calls = evaluate:%d critical:%d offset:%d", evaluateCalls, criticalCalls, offsetCalls)
	}
}

func newTestPartitionRunner(
	t testing.TB,
	evaluator MessageEvaluator,
	critical CriticalCompletion,
	offsets PartitionOffsetCommitter,
	receipts ReceiptPublisher,
) *PartitionRunner {
	t.Helper()
	runner, err := NewPartitionRunner(evaluator, critical, offsets, receipts)
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

type messageEvaluatorFunc func(context.Context, *inputv2.EvaluationInput) (MessageResult, error)

func (function messageEvaluatorFunc) EvaluateMessage(ctx context.Context, input *inputv2.EvaluationInput) (MessageResult, error) {
	return function(ctx, input)
}

type criticalCompletionFunc func(context.Context, CriticalResult) error

func (function criticalCompletionFunc) Complete(ctx context.Context, result CriticalResult) error {
	return function(ctx, result)
}
