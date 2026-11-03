// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
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

// A lookback permit held on the process's coordinator reads as the
// lookback's on the published permit series, through the same adapter and
// collector production binds: the column the adapter fills and the one the
// collector publishes once had two spellings, and the series read nothing.
func TestALookbackPermitHeldReadsAsTheLookbacksOnThePermitSeries(t *testing.T) {
	now := time.Unix(1_000, 0)
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits(),
		func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	permit, refused := flights.TryAcquireLookbackPermit()
	if permit == nil {
		t.Fatalf("lookback permit refused: %s", refused)
	}
	defer permit.Release()
	now = now.Add(3 * time.Second)
	recorder := metric.NewRecorder(metric.BuildInfo{})
	if err := recorder.BindQueryPermits(queryPermitOccupancySource(flights)); err != nil {
		t.Fatal(err)
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	held, seconds := -1.0, -1.0
	for _, family := range families {
		for _, sample := range family.GetMetric() {
			for _, label := range sample.GetLabel() {
				if label.GetName() != "kind" || label.GetValue() != metric.QueryPermitKindLookback {
					continue
				}
				switch family.GetName() {
				case "alarmd_worker_query_permits_held":
					held = sample.GetGauge().GetValue()
				case "alarmd_worker_query_permit_seconds_total":
					seconds = sample.GetCounter().GetValue()
				}
			}
		}
	}
	if held != 1 || seconds < 3 {
		t.Fatalf("lookback on the permit series: held %v, seconds %v; want the one permit held for 3 s", held, seconds)
	}
}
