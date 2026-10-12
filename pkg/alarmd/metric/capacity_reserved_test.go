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
	"sync/atomic"
	"testing"
)

// The reading is the source's at each scrape, not the one it had when it was
// bound, under the label capacity_budget carries the same pool's ceiling
// with; and there is one source.
func TestTheRetainedReservationIsReadAtEachScrape(t *testing.T) {
	recorder := NewRecorder(BuildInfo{Version: "test"})
	var reserved atomic.Uint64
	reserved.Store(3 << 20)
	if err := recorder.BindRetainedReservation(reserved.Load); err != nil {
		t.Fatalf("BindRetainedReservation() error = %v", err)
	}
	read := func() (float64, string, int) {
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() != "bkmonitor_alarmd_capacity_reserved" {
				continue
			}
			series := family.GetMetric()
			if len(series) == 0 || len(series[0].GetLabel()) != 1 || series[0].GetLabel()[0].GetName() != "budget" {
				t.Fatalf("capacity_reserved series = %v, want one labelled by budget", series)
			}
			return series[0].GetGauge().GetValue(), series[0].GetLabel()[0].GetValue(), len(series)
		}
		t.Fatal("no capacity_reserved family")
		return 0, "", 0
	}
	for _, want := range []uint64{3 << 20, 7 << 20, 0} {
		reserved.Store(want)
		value, budget, count := read()
		if value != float64(want) || budget != "retained_bytes" || count != 1 {
			t.Fatalf("scrape = %v under %q (%d series), want %d under retained_bytes, one series", value, budget, count, want)
		}
	}
	if err := recorder.BindRetainedReservation(reserved.Load); err == nil {
		t.Fatal("a second source was bound: two answers to how full the pool is")
	}
	if err := NewRecorder(BuildInfo{}).BindRetainedReservation(nil); err == nil {
		t.Fatal("a nil source was bound")
	}
}
