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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
)

// Every header rebuild outcome and every renewal conflict is a series from
// the first scrape, zero included, and the counts are the repository's own.
func TestActivationHeaderRebuildsAndRenewalConflictsAreScrapedFromZero(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	read := func(family string) map[string]float64 {
		got := map[string]float64{}
		for _, m := range gatherFamily(t, r, family) {
			got[m.Label[0].GetValue()] = m.GetCounter().GetValue()
		}
		return got
	}
	rebuilds, conflicts := "bkmonitor_alarmd_activation_header_rebuild_total", "bkmonitor_alarmd_activation_renewal_conflict_total"
	if got := read(rebuilds); len(got) != len(controlplane.ActivationHeaderRebuildOutcomes) {
		t.Fatalf("rebuilds before any source: %v, want every outcome at zero", got)
	}
	if got := read(conflicts); len(got) != len(controlplane.ActivationRenewalConflicts) {
		t.Fatalf("conflicts before any source: %v, want every reason at zero", got)
	}
	r.SetActivationHeaderSource(func() controlplane.ActivationHeaderReading {
		return controlplane.ActivationHeaderReading{
			Rebuilds: map[controlplane.ActivationHeaderRebuildOutcome]uint64{
				controlplane.ActivationHeaderRebuilt: 2, controlplane.ActivationHeaderRebuildBodyPending: 1},
			RenewalConflicts: map[controlplane.ActivationRenewalConflict]uint64{controlplane.ActivationRenewalHeaderMissing: 3},
		}
	})
	if got := read(rebuilds); got["rebuilt"] != 2 || got["body_pending"] != 1 || got["conflict"] != 0 {
		t.Fatalf("rebuilds after the source: %v", got)
	}
	if got := read(conflicts); got["header_missing"] != 3 || got["header_moved"] != 0 {
		t.Fatalf("conflicts after the source: %v", got)
	}
}
