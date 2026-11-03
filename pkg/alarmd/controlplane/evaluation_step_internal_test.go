// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplane

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The three copies of a Plan's evaluation step -- the schedule's cadence, the
// execution semantics' evaluation interval and each Level's trigger step --
// are held to one another where the schedule is derived. Today they are one
// number; the check is what a change that separates one of them meets first.
func TestTheEvaluationStepCopiesAreHeldTogether(t *testing.T) {
	planWith := func(evaluation uint32, steps ...int64) contract.EvaluationPlanV2 {
		plan := contract.EvaluationPlanV2{StrategyIR: contract.StrategyIRV2{ExecutionSemantics: contract.ExecutionSemanticsV2{
			EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: evaluation, AggregationInterval: evaluation, EvaluationInterval: evaluation,
		}}}
		for index, step := range steps {
			config, _ := json.Marshal(map[string]any{"required_anomalies": 1, "step_seconds": step, "window_size": 5})
			plan.StrategyIR.Levels = append(plan.StrategyIR.Levels, contract.LevelIRV2{
				Definition:  contract.LevelDefinitionV2{LevelID: uint32(index + 1), Priority: uint32(index + 1)},
				TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: config},
			})
		}
		return plan
	}

	spec, refusal, err := planScheduleSpec("7", planWith(60, 60, 60), 60)
	if err != nil || refusal != nil || spec.EvaluationIntervalSeconds != 60 {
		t.Fatalf("agreeing copies: spec=%+v refusal=%+v err=%v", spec, refusal, err)
	}

	for _, test := range []struct {
		name     string
		plan     contract.EvaluationPlanV2
		interval int64
		names    string
	}{
		{name: "schedule off the semantics", plan: planWith(60, 60), interval: 120, names: "schedule_interval=120 evaluation_interval=60"},
		{name: "one Level's step off the semantics", plan: planWith(60, 60, 120), interval: 60, names: "level_2_step_seconds=120 evaluation_interval=60"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, refusal, err := planScheduleSpec("7", test.plan, test.interval)
			if err == nil || refusal == nil {
				t.Fatalf("disagreeing copies were accepted: refusal=%+v err=%v", refusal, err)
			}
			if refusal.SourceID != "7" || refusal.Scope != "PLAN" || refusal.Disposition != DispositionUnsupported ||
				refusal.Reason != "EVALUATION_STEP_INCONSISTENT" || refusal.Detail != test.names {
				t.Fatalf("refusal=%+v, want the Plan withheld by name with %q", refusal, test.names)
			}
			if !strings.Contains(err.Error(), test.names) {
				t.Fatalf("error %q does not say which copy", err)
			}
		})
	}
}
