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

func gatherCapacity(t *testing.T, load CapacityLoad) map[string]map[string]float64 {
	t.Helper()
	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindCapacityLoad(func() CapacityLoad { return load }); err != nil {
		t.Fatalf("BindCapacityLoad() error = %v", err)
	}
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]map[string]float64{}
	for _, family := range families {
		for _, series := range family.Metric {
			label := ""
			for _, pair := range series.Label {
				label = pair.GetValue()
			}
			if gathered[family.GetName()] == nil {
				gathered[family.GetName()] = map[string]float64{}
			}
			value := series.GetGauge().GetValue()
			if series.Counter != nil {
				value = series.GetCounter().GetValue()
			}
			gathered[family.GetName()][label] = value
		}
	}
	return gathered
}

func fullCapacityLoad() CapacityLoad {
	return CapacityLoad{
		Budgets: map[string]float64{
			"series": 524288, "retained_bytes": 2147483648, "state_mutations": 65536,
			"events": 65536, "gap_mutations": 65536,
		},
		MemoryLimitBytes: 8589934592, MemorySource: "pod_limit",
		CPUSource: "environment_override", CPUCores: 8,
		MemoryUsedBytes: 528482304, MemoryUsedKnown: true,
		ThrottledSeconds: 12.5, ThrottledKnown: true,
	}
}

// The ceiling has to carry the same budget label the rejection carries.
// Otherwise capacity_transition_total{result="rejected"} can be seen firing
// with no way to learn what limit it hit, which is the state this family exists
// to end -- the limit was previously only printed once at startup.
func TestCapacityBudgetsUseTheSameLabelsAsRejections(t *testing.T) {
	gathered := gatherCapacity(t, fullCapacityLoad())
	budgets := gathered["bkmonitor_alarmd_capacity_budget"]
	for _, budget := range phaseTwoBudgets {
		if budget == "other" {
			// "other" collects unknown budgets on the rejection side; there is
			// no ceiling to publish for a budget this build does not know.
			if _, present := budgets[budget]; present {
				t.Fatalf("a ceiling was published for the catch-all budget: %v", budgets)
			}
			continue
		}
		if _, present := budgets[budget]; !present {
			t.Fatalf("budget %q can be rejected but has no published ceiling: %v", budget, budgets)
		}
	}
	if budgets["state_mutations"] != 65536 {
		t.Fatalf("state_mutations ceiling = %v, want 65536", budgets["state_mutations"])
	}
}

// The limit and where it was read from travel together. A limit found outside a
// container is a guess, and a guess presented as the container's limit makes
// every budget derived from it look authoritative.
func TestCapacityLoadCarriesTheSourceOfEveryLimit(t *testing.T) {
	gathered := gatherCapacity(t, fullCapacityLoad())
	if got := gathered["bkmonitor_alarmd_container_memory_limit_bytes"]["pod_limit"]; got != 8589934592 {
		t.Fatalf("memory limit not labelled with its source: %v",
			gathered["bkmonitor_alarmd_container_memory_limit_bytes"])
	}
	if got := gathered["bkmonitor_alarmd_container_cpu_cores"]["environment_override"]; got != 8 {
		t.Fatalf("cpu cores not labelled with its source: %v", gathered["bkmonitor_alarmd_container_cpu_cores"])
	}
	// A source this build does not know must land in a visible bucket rather
	// than opening a new series.
	invented := fullCapacityLoad()
	invented.MemorySource = "a_source_from_the_future"
	if got := gatherCapacity(t, invented)["bkmonitor_alarmd_container_memory_limit_bytes"]["other"]; got == 0 {
		t.Fatal("an unknown limit source escaped the closed label set")
	}
}

// Outside a container these files do not exist. Reporting zero would say the
// process is using no memory and is never throttled, which is a far more
// confident claim than "not measured here".
func TestCapacityLoadLeavesUnreadableUsageAbsentRatherThanZero(t *testing.T) {
	load := fullCapacityLoad()
	load.MemoryUsedKnown, load.ThrottledKnown = false, false
	gathered := gatherCapacity(t, load)
	if _, present := gathered["bkmonitor_alarmd_container_memory_used_bytes"]; present {
		t.Fatal("unreadable memory usage was published as a value")
	}
	if _, present := gathered["bkmonitor_alarmd_container_cpu_throttled_seconds_total"]; present {
		t.Fatal("unreadable throttling was published as a value")
	}
	// The limits are still published: what the process was given is known even
	// where what it uses is not.
	if len(gathered["bkmonitor_alarmd_container_memory_limit_bytes"]) != 1 {
		t.Fatalf("limits disappeared with the usage: %v", gathered)
	}
}
