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

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Every cell of both counters reads zero before anything is counted - "about
// zero" is the reading they exist for, and a missing series is not one. A
// placement by id or agent counts under the Plan's grouping, and counts as
// differing too only when its address differs: the differing ones are a
// part of the placed ones, never more.
func TestAPlacementByIDIsCountedAndADifferingAddressWithinIt(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	// Counted before anything here asks for a cell, which would create it.
	for name, vec := range map[string]*prometheus.CounterVec{"placed": r.phaseTwo.cmdbPlacedByHostID, "differs": r.phaseTwo.cmdbAddressDiffers} {
		if cells := testutil.CollectAndCount(vec); cells != 2 {
			t.Fatalf("%s: %d cells from start, want both", name, cells)
		}
	}
	placed := func(grouped string) float64 {
		return testutil.ToFloat64(r.phaseTwo.cmdbPlacedByHostID.WithLabelValues(grouped))
	}
	differs := func(grouped string) float64 {
		return testutil.ToFloat64(r.phaseTwo.cmdbAddressDiffers.WithLabelValues(grouped))
	}
	if placed("true") != 0 || placed("false") != 0 || differs("true") != 0 || differs("false") != 0 {
		t.Fatal("the cells do not start at zero")
	}
	r.RecordHostPlacement(true, true)
	r.RecordHostPlacement(true, false)
	r.RecordHostPlacement(false, false)
	if placed("true") != 2 || placed("false") != 1 || differs("true") != 1 || differs("false") != 0 {
		t.Fatalf("placed %v/%v, differing %v/%v; want 2/1 and 1/0", placed("true"), placed("false"), differs("true"), differs("false"))
	}
}
