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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The dropped unfinished Slot is counted from its slot_skipped line, which
// the store writes once its write applied: one count per line of that path,
// none for a Slot skipped any other way.
func TestAnAbandonedUnfinishedSlotIsCountedFromItsLine(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	for _, path := range []string{observability.SlotSkippedProgressStore, observability.SlotSkippedProgressStore,
		observability.SlotSkippedQueryFree, observability.SlotSkippedExpiredRange, observability.SlotSkippedCursorSkip} {
		observability.ObserveSlotSkipped(context.Background(), r, observability.TraceFields{QueryGroupKey: "qg"},
			observability.OperationWrite, observability.SlotSkippedFacts{Path: path, Reason: "GAP_SKIPPED"})
	}
	families, err := r.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "alarmd_unfinished_slots_abandoned_total" {
			if len(family.Metric) != 1 || family.Metric[0].GetCounter().GetValue() != 2 {
				t.Fatalf("abandoned = %v, want the two progress_store lines", family.Metric)
			}
			return
		}
	}
	t.Fatal("alarmd_unfinished_slots_abandoned_total is not exported")
}
