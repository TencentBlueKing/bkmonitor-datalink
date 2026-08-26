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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestPartitionCompletionTrackerDoesNotCrossIncompleteRegisteredMessage(t *testing.T) {
	t.Parallel()

	tracker := NewPartitionCompletionTracker()
	for _, offset := range []int64{41, 42, 44} {
		if err := tracker.Register(offset); err != nil {
			t.Fatalf("Register(%d) error = %v", offset, err)
		}
	}
	if err := tracker.Complete(42, &contract.MessageReceiptV1{MessageID: "receipt-42"}); err != nil {
		t.Fatal(err)
	}
	if err := tracker.Complete(44, &contract.MessageReceiptV1{MessageID: "receipt-44"}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := tracker.NextCommit(); ok {
		t.Fatal("NextCommit() crossed incomplete offset 41")
	}
	if err := tracker.Complete(41, &contract.MessageReceiptV1{MessageID: "receipt-41"}); err != nil {
		t.Fatal(err)
	}

	next, receipts, ok := tracker.NextCommit()
	wantReceipts := []*contract.MessageReceiptV1{{MessageID: "receipt-41"}, {MessageID: "receipt-42"}, {MessageID: "receipt-44"}}
	if !ok || next != 45 || !reflect.DeepEqual(receipts, wantReceipts) {
		t.Fatalf("NextCommit() = %d, %v, %v; want 45 and registered prefix", next, receipts, ok)
	}
	if repeated, _, ok := tracker.NextCommit(); !ok || repeated != next {
		t.Fatal("pending prefix changed before broker ACK")
	}
	if err := tracker.CommitACK(next); err != nil {
		t.Fatal(err)
	}
	if tracker.Len() != 0 {
		t.Fatalf("Len() = %d, want 0 after ACK", tracker.Len())
	}
}

func TestPartitionCompletionTrackerRejectsInvalidTransitions(t *testing.T) {
	t.Parallel()

	tracker := NewPartitionCompletionTracker()
	if err := tracker.Register(5); err != nil {
		t.Fatal(err)
	}
	for name, action := range map[string]func() error{
		"duplicate register":       func() error { return tracker.Register(5) },
		"out of order register":    func() error { return tracker.Register(4) },
		"unknown complete":         func() error { return tracker.Complete(6, &contract.MessageReceiptV1{MessageID: "receipt-6"}) },
		"ack without ready prefix": func() error { return tracker.CommitACK(6) },
	} {
		if err := action(); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
