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

func gatherPermits(t *testing.T, source QueryPermitOccupancySource) map[string]map[string]float64 {
	t.Helper()
	recorder := NewRecorder(BuildInfo{})
	if err := recorder.BindQueryPermits(source); err != nil {
		t.Fatalf("BindQueryPermits() error = %v", err)
	}
	families, err := recorder.registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gathered := map[string]map[string]float64{}
	for _, family := range families {
		name := family.GetName()
		for _, series := range family.Metric {
			label := ""
			for _, pair := range series.Label {
				label = pair.GetValue()
			}
			if gathered[name] == nil {
				gathered[name] = map[string]float64{}
			}
			value := series.GetGauge().GetValue()
			if series.Counter != nil {
				value = series.GetCounter().GetValue()
			}
			gathered[name][label] = value
		}
	}
	return gathered
}

// The reason this family exists: occupancy has to be readable as an average
// over a window, not only as an instant. A level published only when a permit
// changes hands reports the boundary rather than the interval -- in production
// that gauge never rose above one while thousands of permits were granted every
// minute. A counter of held time survives being sampled at any moment.
func TestPermitOccupancyIsExportedAsAccumulatedHoldTime(t *testing.T) {
	gathered := gatherPermits(t, func() QueryPermitOccupancy {
		return QueryPermitOccupancy{
			Inflight:    map[string]int{"normal": 31, "retry": 1},
			Waiting:     map[string]int{"normal": 12, "recovery": 0},
			HeldSeconds: map[string]float64{"normal": 1840.5, "retry": 3.25},
			Budget:      32, RecoveryBudget: 8,
		}
	})

	held := gathered["bkmonitor_alarmd_worker_query_permit_seconds_total"]
	if held["normal"] != 1840.5 || held["retry"] != 3.25 {
		t.Fatalf("accumulated hold time not exported: %v", held)
	}
	if got := gathered["bkmonitor_alarmd_worker_query_permits_held"]["normal"]; got != 31 {
		t.Fatalf("instantaneous held permits = %v, want 31", got)
	}
	if got := gathered["bkmonitor_alarmd_worker_query_permits_waiting"]["normal"]; got != 12 {
		t.Fatalf("waiting callers = %v, want 12", got)
	}
	// The ceiling travels with the occupancy. Without it a reader has to go find
	// the deployment's configuration to know whether 31 is comfortable or full.
	if got := gathered["bkmonitor_alarmd_worker_query_permit_budget"]["normal"]; got != 32 {
		t.Fatalf("budget = %v, want 32", got)
	}
	if got := gathered["bkmonitor_alarmd_worker_query_permit_budget"]["recovery"]; got != 8 {
		t.Fatalf("recovery budget = %v, want 8", got)
	}
}

// An operation with no activity must still report zero. An absent series and a
// zero series look the same on a chart but not to an alert: "no replays are
// running" and "replays stopped being reported" have to stay distinguishable.
func TestPermitOccupancyPublishesEveryOperationEvenAtZero(t *testing.T) {
	gathered := gatherPermits(t, func() QueryPermitOccupancy {
		return QueryPermitOccupancy{
			Inflight: map[string]int{"normal": 2}, Waiting: map[string]int{},
			HeldSeconds: map[string]float64{"normal": 9}, Budget: 32,
		}
	})
	for _, kind := range []string{"normal", "retry", "replay", "probe"} {
		if _, present := gathered["bkmonitor_alarmd_worker_query_permits_held"][kind]; !present {
			t.Fatalf("operation %q absent from held permits: %v", kind,
				gathered["bkmonitor_alarmd_worker_query_permits_held"])
		}
		if _, present := gathered["bkmonitor_alarmd_worker_query_permit_seconds_total"][kind]; !present {
			t.Fatalf("operation %q absent from hold time: %v", kind,
				gathered["bkmonitor_alarmd_worker_query_permit_seconds_total"])
		}
	}
}

// Binding twice would mean two answers to "is the budget full", which is the
// one thing this family exists to make unambiguous.
func TestPermitOccupancyRefusesASecondSource(t *testing.T) {
	recorder := NewRecorder(BuildInfo{})
	source := func() QueryPermitOccupancy { return QueryPermitOccupancy{} }
	if err := recorder.BindQueryPermits(source); err != nil {
		t.Fatalf("first BindQueryPermits() error = %v", err)
	}
	if err := recorder.BindQueryPermits(source); err == nil {
		t.Fatal("a second permit occupancy source was accepted")
	}
	if err := recorder.BindQueryPermits(nil); err == nil {
		t.Fatal("a nil permit occupancy source was accepted")
	}
}
