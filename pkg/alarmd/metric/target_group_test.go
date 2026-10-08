// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"testing"
)

// The dynamic group store is read at scrape time: every state and gauge
// present at zero before the store is bound, the store's reading after,
// the unanswered reads as a counter.
func TestTargetGroupsReadTheStoreAtScrape(t *testing.T) {
	read := func(recorder *Recorder) map[string]float64 {
		t.Helper()
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		values := map[string]float64{}
		for _, family := range families {
			for _, sample := range family.GetMetric() {
				switch name := family.GetName(); name {
				case "bkmonitor_alarmd_target_group_groups":
					values["groups:"+sample.GetLabel()[0].GetValue()] = sample.GetGauge().GetValue()
				case "bkmonitor_alarmd_target_group_refresh_failed", "bkmonitor_alarmd_target_group_oldest_failing_seconds":
					values[name] = sample.GetGauge().GetValue()
				case "bkmonitor_alarmd_target_group_unanswered_reads_total":
					values[name] = sample.GetCounter().GetValue()
				}
			}
		}
		return values
	}
	recorder := NewRecorder(BuildInfo{})
	values := read(recorder)
	if len(values) != len(TargetGroupStates)+3 {
		t.Fatalf("unbound series = %v, want every state and gauge", values)
	}
	for name, value := range values {
		if value != 0 {
			t.Fatalf("unbound %s = %v, want 0", name, value)
		}
	}
	recorder.SetTargetGroupSource(func() TargetGroupReading {
		return TargetGroupReading{
			Groups:        map[string]int{"referenced": 7, "loaded": 5, "unavailable": 2, "failing": 3, "emptied_pending": 1, "emptied_held": 1},
			RefreshFailed: true, UnansweredReads: 12, OldestFailingSeconds: 240,
		}
	})
	values = read(recorder)
	want := map[string]float64{
		"groups:referenced": 7, "groups:loaded": 5, "groups:unavailable": 2, "groups:failing": 3,
		"groups:emptied_pending": 1, "groups:emptied_held": 1,
		"bkmonitor_alarmd_target_group_refresh_failed": 1, "bkmonitor_alarmd_target_group_unanswered_reads_total": 12,
		"bkmonitor_alarmd_target_group_oldest_failing_seconds": 240,
	}
	for name, value := range want {
		if values[name] != value {
			t.Fatalf("%s = %v, want %v (all: %v)", name, values[name], value, values)
		}
	}
}
