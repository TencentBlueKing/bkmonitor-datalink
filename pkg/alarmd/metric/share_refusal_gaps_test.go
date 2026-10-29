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

// A Slot given up for its share is counted from its slot_skipped line, one
// per line with that cause and none for a Slot skipped for any other cause.
func TestAShareRefusalGapIsCountedFromItsLine(t *testing.T) {
	r := NewRecorder(BuildInfo{})
	share := &observability.SlotSkipShareFacts{Phase: "normal_output", OwnUsedBytes: 530 << 20, LimitBytes: 512 << 20, Refusals: 2}
	for _, facts := range []observability.SlotSkippedFacts{
		{Path: observability.SlotSkippedQueryFree, Reason: "GAP_SKIPPED", Cause: observability.CompletionCauseResourceShareExceeded, SlotCount: 1, Share: share},
		{Path: observability.SlotSkippedQueryFree, Reason: "GAP_SKIPPED", Cause: observability.CompletionCauseResourceShareExceeded, SlotCount: 1, Share: share},
		{Path: observability.SlotSkippedQueryFree, Reason: "GAP_SKIPPED", Cause: "EXPIRED_REPLAY", SlotCount: 1},
		{Path: observability.SlotSkippedProgressStore, Reason: "GAP_SKIPPED", SlotCount: 1},
	} {
		observability.ObserveSlotSkipped(context.Background(), r, observability.TraceFields{QueryGroupKey: "qg-101"},
			observability.OperationWrite, facts)
	}
	families, err := r.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() == "alarmd_share_refusal_gaps_total" {
			if len(family.Metric) != 1 || family.Metric[0].GetCounter().GetValue() != 2 {
				t.Fatalf("share refusal gaps = %v, want the two RESOURCE_SHARE_EXCEEDED lines", family.Metric)
			}
			return
		}
	}
	t.Fatal("alarmd_share_refusal_gaps_total is not exported")
}
