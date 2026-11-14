// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// The witness gauges are absent until a store is bound, so an unwired process
// does not read as an empty cache, and then carry the store's own figures.
func TestStateWitnessGaugesReportTheBoundStore(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	items, bytes := "alarmd_state_witness_items", "alarmd_state_witness_bytes"
	if got := gatherFamily(t, r, items); got != nil {
		t.Fatalf("%s before any source: %v, want absent", items, got)
	}
	reading := state.RuntimeWitnessStats{Items: 3, Bytes: 1_000}
	r.SetStateWitnessSource(func() state.RuntimeWitnessStats { return reading })
	read := func(family string) float64 {
		got := gatherFamily(t, r, family)
		if len(got) != 1 {
			t.Fatalf("%s: %d series, want 1", family, len(got))
		}
		return got[0].GetGauge().GetValue()
	}
	if got := read(items); got != 3 {
		t.Fatalf("%s = %v, want 3", items, got)
	}
	if got := read(bytes); got != 1_000 {
		t.Fatalf("%s = %v, want 1000", bytes, got)
	}
	reading = state.RuntimeWitnessStats{}
	if got := read(items); got != 0 {
		t.Fatalf("%s after the cache emptied = %v, want 0", items, got)
	}
}
