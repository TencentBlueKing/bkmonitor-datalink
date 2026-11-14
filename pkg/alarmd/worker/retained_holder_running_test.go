// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/testwait"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// A Slot is named by the retained holders from when its execution begins,
// reserving nothing yet: while its query runs it is the oldest holder, at no
// bytes, and it leaves the reading when the Slot ends. A Slot that holds its
// structures while reserving nothing is then read as that, by name, rather
// than not at all.
func TestARunningSlotHoldingNothingIsTheOldestHolder(t *testing.T) {
	fixture := newFixtureWithBudget(t, worker.ProvisionalBudget{
		MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10,
	})
	fixture.ports.queryTakes = 300 * time.Millisecond
	request := slotRequest(execution.OperationNormal)
	began := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := fixture.coordinator.Execute(context.Background(), request)
		done <- err
	}()
	var reading worker.RetainedHolderReading
	testwait.Until(t, "the running Slot is listed", func() bool {
		reading = worker.RetainedHolders(fixture.coordinator, -1)
		return reading.Total == 1
	})
	if oldest := reading.Oldest; oldest == nil || oldest.Slot != request.Contract.Slot || oldest.Bytes != 0 ||
		oldest.Since.Before(began) || len(reading.Holders) != 1 {
		t.Fatalf("reading %+v while the query runs; want the Slot as the oldest holder at no bytes since it began", reading)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if reading = worker.RetainedHolders(fixture.coordinator, -1); reading.Total != 0 || reading.Oldest != nil {
		t.Fatalf("reading %+v after the Slot ended; want no holder", reading)
	}
}
