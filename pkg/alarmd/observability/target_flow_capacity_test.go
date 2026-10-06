// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// A per-Slot capacity rejection is the only resource_hard observation the
// target flow exports: the selected Query Group's line carries the budget
// kind, the phase and the own/requested/limit counts, so the operator can read
// which cap the Slot exceeded and by how much without the runtime log.
func TestTargetFlowRendersSlotCapacityRejectionFacts(t *testing.T) {
	f, b := newTestFlow(t)
	ctx := f.Context(context.Background(), flowQG)
	own := uint64(65537)
	rejection := Observation{
		Component: ComponentResource, Stage: StageResourceHard, Result: ResultPaused, Operation: OperationNormal,
		Direction: DirectionInternal, ReasonCode: ReasonCode("SLOT_BUDGET_EXCEEDED"),
		CapacityBudget:    CapacityBudgetStateMutations,
		CapacityRejection: &CapacityRejectionFacts{Phase: "slot_output", OwnUsed: &own, Requested: 1, Limit: 65536},
		Err:               errors.New("alarmd worker: provisional state_mutations budget exceeded"),
		Trace:             TraceFields{QueryGroupKey: flowQG, StrategyID: "1001", EvaluationTime: 1_788_000_000},
	}
	// A process level resource stop has no capacity facts and no Slot, and a
	// brother Query Group's rejection is not selected.
	f.Observe(ctx, Observation{Component: ComponentResource, Stage: StageResourceHard, Result: ResultPaused,
		ReasonCode: ReasonRSS, Err: errors.New("rss"), Trace: TraceFields{QueryGroupKey: flowQG}})
	brother := rejection
	brother.Trace = TraceFields{QueryGroupKey: "brother"}
	f.Observe(context.Background(), brother)
	if b.Len() != 0 || f.records != 0 {
		t.Fatalf("resource stop without capacity facts or an unselected brother rendered a line: %s", b.String())
	}

	f.Observe(ctx, rejection)
	lines := bytes.Split(bytes.TrimSpace(b.Bytes()), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("rendered %d lines, want one capacity rejection line: %s", len(lines), b.String())
	}
	var record map[string]any
	if err := json.Unmarshal(lines[0], &record); err != nil {
		t.Fatalf("decode target flow line: %v; line=%s", err, lines[0])
	}
	want := map[string]any{
		"target_flow": true, "stage": "resource_hard", "result": "paused", "reason_code": "SLOT_BUDGET_EXCEEDED",
		"query_group_key": flowQG, "strategy_id": "1001", "evaluation_time": float64(1_788_000_000),
	}
	for field, value := range want {
		if record[field] != value {
			t.Fatalf("record[%q] = %#v, want %#v; line=%s", field, record[field], value, lines[0])
		}
	}
	facts, ok := record["facts"].(map[string]any)
	if !ok {
		t.Fatalf("record facts = %#v", record["facts"])
	}
	wantFacts := map[string]any{
		"capacity_budget": "state_mutations", "capacity_phase": "slot_output", "capacity_own_used": float64(65537),
		"capacity_requested": float64(1), "capacity_limit": float64(65536),
	}
	for field, value := range wantFacts {
		if facts[field] != value {
			t.Fatalf("facts[%q] = %#v, want %#v; facts=%#v", field, facts[field], value, facts)
		}
	}
	if _, present := facts["capacity_shared_used"]; present {
		t.Fatalf("per-Slot rejection rendered a shared usage: %#v", facts)
	}
}
