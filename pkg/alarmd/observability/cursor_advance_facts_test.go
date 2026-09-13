// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

import "testing"

func TestCursorAdvanceFactsKeepAClosedStatusVocabulary(t *testing.T) {
	got := NormalizeObservation(Observation{
		Component: ComponentScheduler, Stage: StageScheduleCursorAdvanced, Result: ResultSuccess, Operation: OperationWrite,
		CursorAdvance: &CursorAdvanceFacts{From: -1, To: 600, Status: "sideways"},
	})
	if got.Stage != StageScheduleCursorAdvanced || got.Component != ComponentScheduler || got.CursorAdvance == nil {
		t.Fatalf("observation = %+v, want the cursor advance stage kept", got)
	}
	if got.CursorAdvance.From != 0 || got.CursorAdvance.To != 600 || got.CursorAdvance.Status != CursorAdvanceFailed {
		t.Fatalf("facts were not normalized: %+v", got.CursorAdvance)
	}
	kept := NormalizeObservation(Observation{
		Component: ComponentScheduler, Stage: StageScheduleCursorAdvanced, Result: ResultRetrying, Operation: OperationWrite,
		CursorAdvance: &CursorAdvanceFacts{From: 120, To: 600, Status: CursorAdvanceConflict},
	})
	if kept.CursorAdvance.Status != CursorAdvanceConflict || kept.CursorAdvance.From != 120 {
		t.Fatalf("valid facts were altered: %+v", kept.CursorAdvance)
	}
}
