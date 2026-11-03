// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
)

var _ execution.RetainedBytesReserver = (*seriesAdapter)(nil)

// reservingConsumer is a Slot's consumer that keeps one retained total for
// its series and its reservations, with no lock of its own: the access
// source's serialization is all that keeps the two apart.
type reservingConsumer struct {
	execution.QueryExecutionConsumer
	limit, retained uint64
}

func (consumer *reservingConsumer) ConsumeSeries(context.Context, execution.SeriesExecutionBatch) error {
	consumer.retained++
	return nil
}

func (consumer *reservingConsumer) ReserveRetainedBytes(_ context.Context, bytes uint64) error {
	if consumer.retained+bytes > consumer.limit {
		return errors.New("refused")
	}
	consumer.retained += bytes
	return nil
}

func (consumer *reservingConsumer) ReleaseRetainedBytes(bytes uint64) {
	consumer.retained -= min(bytes, consumer.retained)
}

// A query's sink reserves through the consumer its series are delivered
// to - the Slot's, serialized with its deliveries - and a consumer that
// reserves nothing refuses nothing.
func TestAQuerysSinkReservesThroughItsSlotsConsumer(t *testing.T) {
	adapter := &seriesAdapter{consumer: &serializedQueryConsumer{QueryExecutionConsumer: &reservingConsumer{limit: 100}}}
	if err := adapter.ReserveRetainedBytes(context.Background(), 60); err != nil {
		t.Fatal(err)
	}
	if err := adapter.ReserveRetainedBytes(context.Background(), 60); err == nil {
		t.Fatal("a reservation past the consumer's room was granted")
	}
	adapter.ReleaseRetainedBytes(60)
	if err := adapter.ReserveRetainedBytes(context.Background(), 60); err != nil {
		t.Fatalf("the released room was not given back: %v", err)
	}
	plain := &seriesAdapter{consumer: &serializedQueryConsumer{}}
	if err := plain.ReserveRetainedBytes(context.Background(), 1<<40); err != nil {
		t.Fatalf("a consumer that reserves nothing refused: %v", err)
	}
}

// Two named queries of one Slot reserve and release while a third delivers
// its series: the reservations and the deliveries move one total, under the
// lock the series are delivered under, and none of them waits on another -
// a reservation refuses at once - so all three finish. Run under -race, the
// lock is what keeps it clean.
func TestNamedReservationsAndDeliveriesOfOneSlotSerialize(t *testing.T) {
	inner := &reservingConsumer{limit: 1 << 40}
	consumer := &serializedQueryConsumer{QueryExecutionConsumer: inner}
	const rounds = 2000
	var group sync.WaitGroup
	for query := 0; query < 2; query++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for round := 0; round < rounds; round++ {
				if err := consumer.ReserveRetainedBytes(context.Background(), 10); err != nil {
					t.Error(err)
					return
				}
				consumer.ReleaseRetainedBytes(10)
			}
		}()
	}
	group.Add(1)
	go func() {
		defer group.Done()
		for round := 0; round < rounds; round++ {
			_ = consumer.ConsumeSeries(context.Background(), execution.SeriesExecutionBatch{})
		}
	}()
	done := make(chan struct{})
	go func() { group.Wait(); close(done) }()
	select {
	case <-done:
	case <-testwait.Expired(t):
		t.Fatal("reservations and deliveries of one Slot did not finish: deadlocked")
	}
	if inner.retained != rounds {
		t.Fatalf("retained %d, want the %d delivered series and no reservation left", inner.retained, rounds)
	}
}
