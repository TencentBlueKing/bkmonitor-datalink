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
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Slot that completed short of whole is counted by the cause it carried
// and that cause's reason; a whole Slot is not; a completion with no cause
// is NONE, a word outside the list other, and a reason outside the
// catalogue is folded as every normalized reason is.
func TestAShortCompletionIsCountedByItsCauseAndReason(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	commit := func(kind, cause, reason string) {
		r.Observe(context.Background(), observability.Observation{Component: observability.ComponentProgress,
			Stage: observability.StageProgressCommitted, Result: observability.ResultSuccess,
			ProgressCompletionKind: kind, ProgressCompletionCause: cause, ProgressCompletionReason: reason})
	}
	commit("COMPLETED_WITH_UNAVAILABLE", "PRIMARY_INPUT_UNAVAILABLE", "QUERY_UNAVAILABLE")
	commit("COMPLETED_WITH_UNAVAILABLE", "PRIMARY_INPUT_UNAVAILABLE", "QUERY_UNAVAILABLE")
	commit("COMPLETED_WITH_UNAVAILABLE", "GAP_GUARD_WARMING", "HISTORY_GAPPED")
	commit("COMPLETED_WITH_PARTIAL_GAP", "", "")
	commit("COMPLETED_WITH_UNAVAILABLE", "SOMETHING_NEW", "")
	commit("FULL_COMPLETED", "", "")
	families, err := r.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_progress_completion_causes_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range series.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			counts[labels["completion_kind"]+"/"+labels["cause"]+"/"+labels["reason"]] = series.GetCounter().GetValue()
		}
	}
	notReported := string(observability.NormalizeReason("", observability.ResultDegraded))
	want := map[string]float64{
		"COMPLETED_WITH_UNAVAILABLE/PRIMARY_INPUT_UNAVAILABLE/QUERY_UNAVAILABLE":              2,
		"COMPLETED_WITH_UNAVAILABLE/GAP_GUARD_WARMING/HISTORY_GAPPED":                         1,
		"COMPLETED_WITH_PARTIAL_GAP/NONE/" + notReported:                                      1,
		"COMPLETED_WITH_UNAVAILABLE/" + string(observability.ReasonOther) + "/" + notReported: 1,
	}
	if len(counts) != len(want) {
		t.Fatalf("cells %v, want %v", counts, want)
	}
	for cell, value := range want {
		if counts[cell] != value {
			t.Fatalf("%s = %v, want %v; all %v", cell, counts[cell], value, counts)
		}
	}
}
