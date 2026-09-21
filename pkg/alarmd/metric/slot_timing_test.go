// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestSlotTimingBoundedStagesAndNestedDurations(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	for _, o := range []observability.Observation{
		{Component: observability.ComponentScheduler, Stage: "runner_completed", Duration: 10 * time.Second},
		{Component: observability.ComponentScheduler, Stage: "slot_source_completed", Duration: 7 * time.Second},
		{Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted, Duration: 2 * time.Second},
		{Component: observability.ComponentAccess, Stage: "runner_completed", Duration: time.Hour},
		{Component: observability.ComponentScheduler, Stage: "arbitrary", Duration: time.Hour},
	} {
		r.Observe(context.Background(), o)
	}
	families, err := r.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range families {
		if f.GetName() != "bkmonitor_alarmd_slot_operation_duration_seconds" {
			continue
		}
		found = true
		if len(f.Metric) != 3 {
			t.Fatalf("stage cardinality=%d", len(f.Metric))
		}
		want := map[string]float64{"run_one": 10, "source_next": 7, "execute": 2}
		for _, m := range f.Metric {
			if len(m.Label) != 1 || m.Label[0].GetName() != "stage" {
				t.Fatal("extra label")
			}
			h := m.GetHistogram()
			if h.GetSampleCount() != 1 || h.GetSampleSum() != want[m.Label[0].GetValue()] || len(h.Bucket) != 8 {
				t.Fatalf("bad histogram %v", m)
			}
		}
	}
	if !found {
		t.Fatal("slot operation timing missing")
	}
}
