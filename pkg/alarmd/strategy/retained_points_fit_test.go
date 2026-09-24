// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func planOfLevels(retentions ...uint32) *CompiledPlan {
	plan := &CompiledPlan{}
	for index, points := range retentions {
		plan.levels = append(plan.levels, CompiledLevel{
			definition:       contract.LevelDefinitionV2{LevelID: uint32(index + 1)},
			stateRequirement: StateRequirement{RequiredDetectHistoryPoints: points, RetentionPoints: points},
		})
	}
	return plan
}

// A window the stored record cannot hold is refused where someone can act on
// it, not discovered one write at a time in production.
//
// The ceiling depends on how many Levels share the record, which is why this
// is decided for the Plan rather than while compiling a Level: 1,469 points is
// comfortable for one Level and over the line for two, and the Level that
// makes it so is a different one. Nothing above this catches it -
// max_required_history_points is 4096 and the representation runs out around
// 2100 - so without this a Plan between the two compiles, activates, evaluates,
// and has every state write refused per series with no alert and nothing in the
// strategy to suggest why.
func TestAWindowTheRecordCannotHoldIsRefusedAtCompileTime(t *testing.T) {
	compiler := &PlanCompiler{limits: Limits{
		MaxLevelsPerPlan: 8,
		// Index by Level count, as the derivation produces it.
		MaxRetainedPointsByLevels: []uint32{0, 2100, 1405, 1056, 845, 0, 0, 0, 470, 0},
	}}

	if terminal := compiler.checkRetainedPointsFit(planOfLevels(1469)); terminal != nil {
		t.Fatalf("a single-Level Plan retaining 1469 points was refused (%+v); it fits, and refusing it "+
			"would stop a strategy that runs today", terminal)
	}
	// The same window, with a second Level sharing the record.
	terminal := compiler.checkRetainedPointsFit(planOfLevels(1469, 1469))
	if terminal == nil {
		t.Fatal("two Levels retaining 1469 points each compiled; that record is over what the store " +
			"accepts, so every write this Plan makes would be refused")
	}
	if terminal.ReasonCode != contract.ReasonPlanBudgetExceeded {
		t.Fatalf("reason = %q, want %q", terminal.ReasonCode, contract.ReasonPlanBudgetExceeded)
	}
	if terminal.FieldPath != "level.state_requirement" {
		t.Fatalf("field path = %q, want the window that has to change", terminal.FieldPath)
	}
	// One Level over the line is enough, wherever it sits.
	if compiler.checkRetainedPointsFit(planOfLevels(10, 3000)) == nil {
		t.Fatal("a Plan whose second Level is over the single-Level ceiling compiled")
	}
	// And a shape with no stated ceiling is not refused by accident: silence in
	// the table means unknown, which must not read as zero.
	if compiler.checkRetainedPointsFit(planOfLevels(9000, 9000, 9000, 9000, 9000)) != nil {
		t.Fatal("a Level count with no ceiling stated was refused; an absent entry is not a ceiling of zero")
	}
}

// The same rule through Compile, because the check only protects anything if
// the compile path runs it.
//
// Stated only against the function, the rule reads as satisfied while the call
// is missing from Compile - a mutation removing that call left the whole
// package green. The refusal has to be observed where a Plan actually arrives.
func TestCompileRefusesAWindowTheRecordCannotHold(t *testing.T) {
	windowed := func(points uint32) contract.LevelIRV2 {
		level := validLevel(1, 1, "50")
		level.TriggerPlan.Config = json.RawMessage(
			fmt.Sprintf(`{"window_size":%d,"required_anomalies":1,"step_seconds":60}`, points))
		return level
	}
	compile := func(t *testing.T, ceilings []uint32, points uint32) CompileResult {
		t.Helper()
		limits := testLimits()
		limits.MaxRetainedPointsByLevels = ceilings
		compiler, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), limits)
		if err != nil {
			t.Fatal(err)
		}
		plan := validPlan()
		plan.StrategyIR.Levels = []contract.LevelIRV2{windowed(points)}
		result, err := compiler.Compile(context.Background(), validRequest(plan))
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	ceilings := []uint32{0, 2100, 1405, 1056}
	if _, ok := compile(t, ceilings, 1469).Plan(); !ok {
		t.Fatal("a single-Level Plan retaining 1469 points was refused by Compile; it stores cleanly today")
	}

	over := compile(t, ceilings, 2500)
	if _, ok := over.Plan(); ok {
		t.Fatal("Compile admitted a window the stored record cannot hold; every state write this Plan " +
			"makes would be refused, per series, with nothing to say why")
	}
	terminal := over.PlanTerminal()
	if terminal == nil || terminal.ReasonCode != contract.ReasonPlanBudgetExceeded {
		t.Fatalf("plan terminal = %+v, want %q", terminal, contract.ReasonPlanBudgetExceeded)
	}

	// With no ceilings stated, Compile admits it: the refusal comes from the
	// derived table rather than from a number hidden in the compiler.
	if _, ok := compile(t, nil, 2500).Plan(); !ok {
		t.Fatal("Compile refused a 2500-point window with no ceiling stated for any Level count")
	}
}
