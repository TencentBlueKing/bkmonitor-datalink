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

func TestPartitionCommitterPublishesReceiptOnlyAfterOffsetACK(t *testing.T) {
	t.Parallel()

	tracker := completedTracker(t, 41, &contract.MessageReceiptV1{MessageID: "message-41"})
	calls := []string{}
	committer, err := NewPartitionCommitter(
		tracker,
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
	if err != nil {
		t.Fatal(err)
	}
	if err := committer.CommitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"offset", "receipt:message-41"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if tracker.Len() != 0 {
		t.Fatalf("tracker Len = %d, want 0", tracker.Len())
	}
}

func TestPartitionCommitterRetainsPrefixWhenOffsetACKFails(t *testing.T) {
	t.Parallel()

	tracker := completedTracker(t, 41, &contract.MessageReceiptV1{MessageID: "message-41"})
	want := errors.New("coordinator unavailable")
	receiptCalls := 0
	committer, err := NewPartitionCommitter(
		tracker,
		partitionOffsetCommitterFunc(func(context.Context, int64) error { return want }),
		receiptPublisherFunc(func(*contract.MessageReceiptV1) bool { receiptCalls++; return true }),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := committer.CommitReady(context.Background()); !errors.Is(err, want) {
		t.Fatalf("CommitReady() error = %v, want %v", err, want)
	}
	if tracker.Len() != 1 || receiptCalls != 0 {
		t.Fatalf("failure state = tracker:%d receipt calls:%d, want 1/0", tracker.Len(), receiptCalls)
	}
	if next, _, ok := tracker.NextCommit(); !ok || next != 42 {
		t.Fatal("completed prefix was not retained for retry")
	}
}

func completedTracker(t testing.TB, offset int64, receipt *contract.MessageReceiptV1) *PartitionCompletionTracker {
	t.Helper()
	tracker := NewPartitionCompletionTracker()
	if err := tracker.Register(offset); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Complete(offset, receipt); err != nil {
		t.Fatal(err)
	}
	return tracker
}

type partitionOffsetCommitterFunc func(context.Context, int64) error

func (function partitionOffsetCommitterFunc) CommitThrough(ctx context.Context, next int64) error {
	return function(ctx, next)
}

type receiptPublisherFunc func(*contract.MessageReceiptV1) bool

func (function receiptPublisherFunc) TryEnqueue(receipt *contract.MessageReceiptV1) bool {
	return function(receipt)
}
