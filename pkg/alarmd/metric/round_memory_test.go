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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
)

// The round memory is read at scrape time: every bucket and gauge present at
// zero before the tracker is bound, the tracker's reading after.
func TestRoundMemoryReadsTheTrackerAtScrape(t *testing.T) {
	read := func(recorder *Recorder) (map[string]float64, int) {
		t.Helper()
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		values, buckets := map[string]float64{}, 0
		for _, family := range families {
			for _, sample := range family.GetMetric() {
				switch name := family.GetName(); name {
				case "bkmonitor_alarmd_fleet_round_memory_objects":
					values["objects:"+sample.GetLabel()[0].GetValue()] = sample.GetGauge().GetValue()
					buckets++
				case "bkmonitor_alarmd_fleet_round_memory_rounds", "bkmonitor_alarmd_fleet_round_memory_bytes",
					"bkmonitor_alarmd_fleet_round_memory_max_rounds", "bkmonitor_alarmd_fleet_round_memory_window_sized_objects",
					"bkmonitor_alarmd_fleet_round_memory_line_held_objects":
					values[name] = sample.GetGauge().GetValue()
				}
			}
		}
		return values, buckets
	}
	recorder := NewRecorder(BuildInfo{})
	values, buckets := read(recorder)
	if buckets != len(fleet.RoundMemoryBuckets) || len(values) != len(fleet.RoundMemoryBuckets)+5 {
		t.Fatalf("unbound series = %v over %d buckets, want every bucket and gauge", values, buckets)
	}
	for name, value := range values {
		if value != 0 {
			t.Fatalf("unbound %s = %v, want 0", name, value)
		}
	}
	recorder.SetRoundMemorySource(func() fleet.RoundMemoryFacts {
		return fleet.RoundMemoryFacts{
			Objects: map[string]int{"le_16": 5, "le_1440": 2},
			Rounds:  900, Bytes: 16384, MaxRounds: 720, WindowSized: 6, HeldByLine: 3,
		}
	})
	values, _ = read(recorder)
	want := map[string]float64{
		"objects:le_16": 5, "objects:le_64": 0, "objects:le_256": 0, "objects:le_1440": 2, "objects:gt_1440": 0,
		"bkmonitor_alarmd_fleet_round_memory_rounds":               900,
		"bkmonitor_alarmd_fleet_round_memory_bytes":                16384,
		"bkmonitor_alarmd_fleet_round_memory_max_rounds":           720,
		"bkmonitor_alarmd_fleet_round_memory_window_sized_objects": 6,
		"bkmonitor_alarmd_fleet_round_memory_line_held_objects":    3,
	}
	for name, value := range want {
		if values[name] != value {
			t.Fatalf("%s = %v, want %v (all %v)", name, values[name], value, values)
		}
	}
}
