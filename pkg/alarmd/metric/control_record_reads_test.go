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
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every cell of the three reads zero from start. A round line's reads count
// under their reasons and its mismatches under their fields, a name outside
// the vocabulary under none; its conflicts split into settled and again,
// again never more than there were.
func TestTheLeadersRecordReadsAreCountedByWhy(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	for name, cells := range map[string][2]int{
		"reads":      {testutil.CollectAndCount(r.phaseTwo.controlRecordReads), len(observability.RecordReadReasons)},
		"mismatches": {testutil.CollectAndCount(r.phaseTwo.controlRecordMismatches), len(observability.RecordFields)},
		"conflicts":  {testutil.CollectAndCount(r.phaseTwo.controlRecordConflicts), 2},
	} {
		if cells[0] != cells[1] {
			t.Fatalf("%s: %d cells from start, want %d", name, cells[0], cells[1])
		}
	}
	observe := func(facts *observability.ControlReadFacts) {
		r.Observe(context.Background(), observability.Observation{Component: observability.ComponentOwnership,
			Stage: observability.StageControlReadsSpent, Result: observability.ResultSuccess, ControlReads: facts})
	}
	observe(&observability.ControlReadFacts{RecordReads: map[string]int{"term_start": 3, "backstop": 1, "made_up": 9},
		RecordMismatches: map[string]int{"content_scope": 2, "made_up": 9}, RecordConflicts: 3, RecordConflictsAgain: 1})
	observe(&observability.ControlReadFacts{RecordReads: map[string]int{"backstop": 1}, RecordConflicts: 1, RecordConflictsAgain: 5})
	for _, cell := range []struct {
		value float64
		want  float64
		name  string
	}{
		{testutil.ToFloat64(r.phaseTwo.controlRecordReads.WithLabelValues("term_start")), 3, "reads term_start"},
		{testutil.ToFloat64(r.phaseTwo.controlRecordReads.WithLabelValues("backstop")), 2, "reads backstop"},
		{testutil.ToFloat64(r.phaseTwo.controlRecordMismatches.WithLabelValues("content_scope")), 2, "mismatches content_scope"},
		{testutil.ToFloat64(r.phaseTwo.controlRecordConflicts.WithLabelValues("settled")), 2, "conflicts settled"},
		{testutil.ToFloat64(r.phaseTwo.controlRecordConflicts.WithLabelValues("again")), 2, "conflicts again"},
	} {
		if cell.value != cell.want {
			t.Fatalf("%s = %v, want %v", cell.name, cell.value, cell.want)
		}
	}
	if cells := testutil.CollectAndCount(r.phaseTwo.controlRecordReads); cells != len(observability.RecordReadReasons) {
		t.Fatalf("%d reads cells after counting, want %d: a reason outside the set made its own", cells, len(observability.RecordReadReasons))
	}
}
