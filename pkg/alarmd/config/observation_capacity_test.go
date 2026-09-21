// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "testing"

// The share is the operator's: three percent of one and two gibibytes scale
// together, the sum of the parts stays inside the share, an unknown memory
// source enables nothing -- and so does no allocation, which is the default.
func TestObservationBudgetUsesBothResourcesAndHasNoUnknownFallback(t *testing.T) {
	share := PhaseTwoObservationConfig{MemoryPercent: 3}
	a := DeriveObservationCapacity(CapacityInputs{CPUBudget: 1, MemoryLimitBytes: 1 << 30, MemorySource: "pod_limit"}, share)
	b := DeriveObservationCapacity(CapacityInputs{CPUBudget: 2, MemoryLimitBytes: 2 << 30, MemorySource: "pod_limit"}, share)
	if a.DirectoryBytes*2 != b.DirectoryBytes || a.SampleRecordsPerMinute*2 != b.SampleRecordsPerMinute {
		t.Fatalf("budget did not scale: %+v %+v", a, b)
	}
	if a.DirectoryBytes+a.CostBytes+a.SampleBufferBytes > (1<<30)/100*3 {
		t.Fatal("exceeded memory share")
	}
	if got := DeriveObservationCapacity(CapacityInputs{CPUBudget: 2, MemoryLimitBytes: 2 << 30, MemorySource: memorySourceFallback}, share); got != (ObservationCapacity{}) {
		t.Fatalf("unknown budget enabled: %+v", got)
	}
}

// Nothing is on until an operator says how much. A container that knows its
// limit and its cores is not an allocation; the default configuration runs
// detection and none of the diagnostics that read the control plane on their
// own account. Past the bound reads as none too, though validation refuses it
// before it gets here.
func TestObservationBudgetIsOffWithoutAnOperatorAllocation(t *testing.T) {
	known := CapacityInputs{CPUBudget: 2, MemoryLimitBytes: 2 << 30, MemorySource: "pod_limit"}
	if got := DeriveObservationCapacity(known, PhaseTwoObservationConfig{}); got != (ObservationCapacity{}) {
		t.Fatalf("the default allocation enabled the diagnostics: %+v", got)
	}
	if got := DeriveObservationCapacity(known, defaultPhaseTwoRuntime().Observation); got != (ObservationCapacity{}) {
		t.Fatalf("the default phase-two configuration allocates a share: %+v", got)
	}
	if got := DeriveObservationCapacity(known, PhaseTwoObservationConfig{MemoryPercent: ObservationMemoryPercentMax + 1}); got != (ObservationCapacity{}) {
		t.Fatalf("a share past the bound enabled the diagnostics: %+v", got)
	}
	if got := DeriveObservationCapacity(known, PhaseTwoObservationConfig{MemoryPercent: 1}); got == (ObservationCapacity{}) {
		t.Fatal("one percent of a known container enabled nothing")
	}
	for percent, valid := range map[int]bool{-1: false, 0: true, 1: true, ObservationMemoryPercentMax: true, ObservationMemoryPercentMax + 1: false} {
		if err := (PhaseTwoObservationConfig{MemoryPercent: percent}).validate(); (err == nil) != valid {
			t.Errorf("memory_percent %d validates as %v, want %v", percent, err == nil, valid)
		}
	}
}
