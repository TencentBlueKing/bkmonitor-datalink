// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The slot sources judge a round short of time by the same measured tail the
// access source cuts its read at, and the gauge reads that tail: a tail the
// shaper publishes is what the slot sources see and what is exported. Judged
// by a reserve of their own, or by none, they would fall back to the frozen
// deadline while the read is cut earlier.
func TestTheSlotSourcesJudgeByTheTailAccessCutsAt(t *testing.T) {
	flights := scheduler.NewFlightCoordinator()
	recorder := metric.NewRecorder(metric.BuildInfo{})
	live := newRuntimeLiveReserve(flights, recorder)
	live.Set(15 * time.Second)
	judged := slotSourceLiveReserve(flights)
	if judged != live || judged.Reserve() != 15*time.Second {
		t.Fatalf("slot sources judge by %p (%v), want the runtime's %p (15s)", judged, judged.Reserve(), live)
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "alarmd_live_query_reserve_seconds" {
			if got := family.GetMetric()[0].GetGauge().GetValue(); got != 15 {
				t.Fatalf("gauge reads %v, want 15", got)
			}
			return
		}
	}
	t.Fatal("alarmd_live_query_reserve_seconds is not exported")
}
