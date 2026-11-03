// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// recordingReadHolds answers one hold for every Slot and remembers which
// Slots were frozen and which were only asked about.
type recordingReadHolds struct {
	frozen, asked []execution.EvaluationTime
}

func (holds *recordingReadHolds) ReadHold(execution.QueryGroupIdentity) time.Duration { return 0 }
func (holds *recordingReadHolds) SlotReadHold(_ context.Context, _ execution.FrozenQueryGroupSchedule, at execution.EvaluationTime, _ execution.OwnerFence) (time.Duration, error) {
	holds.frozen = append(holds.frozen, at)
	return 0, nil
}
func (holds *recordingReadHolds) PeekSlotReadHold(_ execution.FrozenQueryGroupSchedule, at execution.EvaluationTime) (time.Duration, error) {
	holds.asked = append(holds.asked, at)
	return 0, nil
}

// An expired range asks its last Slot's hold only to compare it with its
// first: it asks, it does not freeze. Freezing it wrote the read hold record
// as in force from that Slot, and the Slots before it could no longer be
// written to it.
func TestAnExpiredRangeAsksItsLastSlotsHoldWithoutFreezingIt(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceWithRecoveryForTest(t, catalog, foundProgress(120, 60), time.UnixMilli(954999), testRecoveryLimits())
	holds := &recordingReadHolds{}
	source.readHolds = holds
	ctx := context.WithValue(context.Background(), rangeFlightContextKey{}, execution.QueryGroupIdentity("query-group-1"))
	slot, due, _, err := source.Next(ctx, "query-group-1")
	if err != nil || !due || slot.ExpiredRange == nil || slot.ExpiredRange.Last.Contract.Slot.EvaluationTime != 240 {
		t.Fatalf("range: %+v %v %v", slot, due, err)
	}
	if !reflect.DeepEqual(holds.frozen, []execution.EvaluationTime{120}) || !reflect.DeepEqual(holds.asked, []execution.EvaluationTime{240}) {
		t.Fatalf("froze %v and asked %v; want the first Slot frozen and the last only asked", holds.frozen, holds.asked)
	}
}
