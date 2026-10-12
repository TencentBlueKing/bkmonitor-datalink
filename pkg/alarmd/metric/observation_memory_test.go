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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/memoryline"
)

// The line is read at each scrape: the headroom as one gauge, negative past
// the line, and every consumer's refusals and admitted bytes, each consumer
// present at zero.
func TestTheObservationMemoryLineIsReadAtEachScrape(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "test"})
	reading := memoryline.Reading{HeadroomBytes: 5 << 20, RefusedTotal: map[memoryline.Consumer]uint64{memoryline.ConsumerLookback: 3},
		AdmittedBytes: map[memoryline.Consumer]uint64{memoryline.ConsumerCostSummary: 1 << 20}}
	if err := recorder.BindObservationMemory(func() memoryline.Reading { return reading }); err != nil {
		t.Fatal(err)
	}
	if err := recorder.BindObservationMemory(func() memoryline.Reading { return reading }); err == nil {
		t.Fatal("a second line bound")
	}
	scrape := func() (float64, map[string]float64, map[string]float64) {
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		var headroom float64
		refused, admitted := map[string]float64{}, map[string]float64{}
		for _, family := range families {
			for _, series := range family.GetMetric() {
				switch family.GetName() {
				case "bkmonitor_alarmd_observation_memory_headroom_bytes":
					headroom = series.GetGauge().GetValue()
				case "bkmonitor_alarmd_observation_memory_refused_total":
					refused[series.GetLabel()[0].GetValue()] = series.GetCounter().GetValue()
				case "bkmonitor_alarmd_observation_memory_admitted_bytes_total":
					admitted[series.GetLabel()[0].GetValue()] = series.GetCounter().GetValue()
				}
			}
		}
		return headroom, refused, admitted
	}
	headroom, refused, admitted := scrape()
	if headroom != 5<<20 || refused["lookback"] != 3 || admitted["cost_summary"] != 1<<20 ||
		len(refused) != len(memoryline.Consumers) || len(admitted) != len(memoryline.Consumers) {
		t.Fatalf("headroom %v refused %v admitted %v, want the reading with every consumer present", headroom, refused, admitted)
	}
	reading.HeadroomBytes = -7
	if headroom, _, _ := scrape(); headroom != -7 {
		t.Fatalf("headroom %v past the line, want -7 read at this scrape", headroom)
	}
}

// Each detection budget is read at the scrape by name: its size and what it
// holds, one series each, and none for a line with no budgets.
func TestEachDetectionBudgetIsReadByName(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "test"})
	reading := memoryline.Reading{Budgets: []memoryline.BudgetReading{
		{Name: "retained", SizeBytes: 1 << 30, HeldBytes: 1 << 20},
		{Name: "object_cache", SizeBytes: 3 << 20, HeldBytes: 2 << 20},
	}}
	if err := recorder.BindObservationMemory(func() memoryline.Reading { return reading }); err != nil {
		t.Fatal(err)
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	size, held := map[string]float64{}, map[string]float64{}
	for _, family := range families {
		for _, series := range family.GetMetric() {
			switch family.GetName() {
			case "bkmonitor_alarmd_observation_memory_budget_size_bytes":
				size[series.GetLabel()[0].GetValue()] = series.GetGauge().GetValue()
			case "bkmonitor_alarmd_observation_memory_budget_held_bytes":
				held[series.GetLabel()[0].GetValue()] = series.GetGauge().GetValue()
			}
		}
	}
	if len(size) != 2 || size["retained"] != 1<<30 || size["object_cache"] != 3<<20 ||
		len(held) != 2 || held["retained"] != 1<<20 || held["object_cache"] != 2<<20 {
		t.Fatalf("sizes %v held %v, want each budget's reading under its name", size, held)
	}
}

// What observation holds now is read at the scrape.
func TestWhatObservationHoldsIsReadAtEachScrape(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "test"})
	reading := memoryline.Reading{HeldBytes: 7 << 20}
	if err := recorder.BindObservationMemory(func() memoryline.Reading { return reading }); err != nil {
		t.Fatal(err)
	}
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "bkmonitor_alarmd_observation_memory_held_bytes" {
			if got := family.GetMetric()[0].GetGauge().GetValue(); got != 7<<20 {
				t.Fatalf("held = %v, want the reading's 7 MiB", got)
			}
			return
		}
	}
	t.Fatal("no observation_memory_held_bytes")
}
