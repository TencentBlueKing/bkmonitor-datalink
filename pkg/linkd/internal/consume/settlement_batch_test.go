// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestIndividualConfirmBatchBoundaries(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			cfg := testRuntimeConfig()
			cfg.MaxBatchMessages = 2
			s := runtimeState{runtime: New(cfg, nil, nil), capabilities: Capabilities{Settlement: SettlementIndividual, BatchIndividualConfirm: enabled}, lanes: map[string]*laneState{}}
			for _, lane := range []string{"a", "a", "a", "b", "b"} {
				s.queueSettlement(&trackedDelivery{terminal: true, delivery: Delivery{Meta: DeliveryMeta{Lane: lane}}})
			}
			want := 5
			if enabled {
				want = 3
			}
			if len(s.settleQueue) != want {
				t.Fatalf("batches=%d want=%d", len(s.settleQueue), want)
			}
			for _, batch := range s.settleQueue {
				if len(batch.entries) > 2 {
					t.Fatal("unbounded batch")
				}
				for _, entry := range batch.entries {
					if entry.delivery.Meta.Lane != batch.lane {
						t.Fatal("mixed lanes")
					}
				}
			}
		})
	}
}

func TestIndividualConfirmInflightAndRetryBatchImmutable(t *testing.T) {
	requests := make(chan *settleBatch, 1)
	cfg := testRuntimeConfig()
	s := runtimeState{runtime: New(cfg, nil, nil), capabilities: Capabilities{Settlement: SettlementIndividual, BatchIndividualConfirm: true}, lanes: map[string]*laneState{}, settleRequests: requests}
	add := func() {
		s.inflightMessages++
		s.inflightBytes++
		s.lane("a").inflight++
		s.queueSettlement(&trackedDelivery{terminal: true, delivery: Delivery{Message: Message{Body: []byte("a")}, Meta: DeliveryMeta{Lane: "a"}}})
	}
	add()
	s.trySettle(time.Now())
	first := <-requests
	add()
	add()
	if len(first.entries) != 1 || len(s.settleQueue) != 2 || len(s.settleQueue[1].entries) != 2 {
		t.Fatal("mutated executing batch or failed to aggregate")
	}
	s.handleSettleResult(settleResult{batch: first, err: errors.New("response lost")}, time.Now())
	if s.inflightMessages != 3 {
		t.Fatal("released inflight before successful confirmation")
	}
	// 重试批次未扩大；新完成项只能进入后续尚未发送的尾批。
	add()
	if len(first.entries) != 1 {
		t.Fatal("mutated retry batch")
	}
	s.trySettle(first.nextAttempt)
	if retry := <-requests; retry != first {
		t.Fatal("changed retry target")
	}
	s.handleSettleResult(settleResult{batch: first}, time.Now())
	if s.inflightMessages != 3 || s.lane("a").inflight != 3 {
		t.Fatal("released wrong entries")
	}
	s.trySettle(time.Now())
	last := <-requests
	if len(last.entries) != 3 {
		t.Fatal("pending entries not batched")
	}
	s.handleSettleResult(settleResult{batch: last}, time.Now())
	if s.inflightMessages != 0 || s.inflightBytes != 0 || len(s.settleQueue) != 0 {
		t.Fatal("did not drain")
	}
}
