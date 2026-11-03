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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

// Every cell of both counters reads zero before anything is counted - "about
// zero" is the reading they exist for, and a missing series is not one. A
// placement by id or agent counts under the Plan's grouping, and counts as
// differing too, under how it differs, only when its address is not the
// host's: the differing ones are a part of the placed ones, never more.
func TestAPlacementByIDIsCountedAndADifferingAddressWithinIt(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	// Counted before anything here asks for a cell, which would create it.
	for name, want := range map[*prometheus.CounterVec]int{r.phaseTwo.cmdbPlacedByHostID: 2, r.phaseTwo.cmdbAddressDiffers: 6} {
		if cells := testutil.CollectAndCount(name); cells != want {
			t.Fatalf("%d cells from start, want %d", cells, want)
		}
	}
	placed := func(grouped string) float64 {
		return testutil.ToFloat64(r.phaseTwo.cmdbPlacedByHostID.WithLabelValues(grouped))
	}
	differs := func(grouped, cause string) float64 {
		return testutil.ToFloat64(r.phaseTwo.cmdbAddressDiffers.WithLabelValues(grouped, cause))
	}
	r.RecordHostPlacement(true, admission.AddressDiffers)
	r.RecordHostPlacement(true, admission.AddressMissing)
	r.RecordHostPlacement(true, admission.AddressSame)
	r.RecordHostPlacement(false, admission.AddressUndecodable)
	r.RecordHostPlacement(false, "not_a_cause")
	if placed("true") != 3 || placed("false") != 2 {
		t.Fatalf("placed %v/%v, want 3/2", placed("true"), placed("false"))
	}
	for cell, want := range map[[2]string]float64{
		{"true", admission.AddressDiffers}: 1, {"true", admission.AddressMissing}: 1, {"true", admission.AddressUndecodable}: 0,
		{"false", admission.AddressDiffers}: 0, {"false", admission.AddressMissing}: 0, {"false", admission.AddressUndecodable}: 1,
	} {
		if got := differs(cell[0], cell[1]); got != want {
			t.Fatalf("differs %v = %v, want %v", cell, got, want)
		}
	}
	if cells := testutil.CollectAndCount(r.phaseTwo.cmdbAddressDiffers); cells != 6 {
		t.Fatalf("%d cells after counting, want 6: a cause outside the set made its own", cells)
	}
}
