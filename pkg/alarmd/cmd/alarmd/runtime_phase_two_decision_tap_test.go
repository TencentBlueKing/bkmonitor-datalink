// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/lookback"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// Every series of every Slot reports its evaluation to the decision tap, and nearly all are of groups not in
// probation, whose decisions the lookback does not keep: the tap turns them away before a report is built, with
// nothing allocated. A series of a group in probation - one whose first round here follows a lowering - is built and
// handed on; the lookback keeps it for the round's read-early line (TestARoundReadEarlyIsALineWithWhatItDecided).
func TestTheDecisionTapTurnsAwayAGroupNotInProbation(t *testing.T) {
	now := time.Unix(1_700_000_060, 0)
	engine, err := lookback.New(lookback.Options{Now: func() time.Time { return now },
		LastLowering: func(queryGroup execution.QueryGroupIdentity) (execution.EvaluationTime, bool) {
			return 1_700_000_000, queryGroup == "lowered"
		},
		Recheck: func(context.Context, execution.PhysicalQuerySpec, execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
			return execution.ProviderCompletion{}, nil
		},
		Permit: func() (func(), <-chan struct{}, string) { return func() {}, nil, "" },
		Owns:   func(execution.QueryGroupIdentity) bool { return true }, Owned: func() int { return 1 }})
	if err != nil {
		t.Fatal(err)
	}
	tap := &lookbackDecisionTap{}
	tap.engine.Store(engine)
	ctx := context.Background()
	evaluated := observability.Observation{Component: observability.ComponentEvaluation,
		Stage:         observability.StageEvaluationCompleted,
		Trace:         observability.TraceFields{QueryGroupKey: "qg", EvaluationTime: 1_700_000_000, StrategyID: "7"},
		LevelOutcomes: []observability.LevelOutcomeFact{{Outcome: "NORMAL", Count: 3}, {Outcome: "ABNORMAL", Count: 1}}}
	if allocs := testing.AllocsPerRun(200, func() { tap.Observe(ctx, evaluated) }); allocs != 0 {
		t.Fatalf("the tap allocated %.1f a series of a group not in probation, want none", allocs)
	}
	query := sampledQuery("lowered")
	query.FollowingSlot = query.Contract.Slot.EvaluationTime + 60
	query.Contract.ReadHoldMillis = 60_000
	engine.Begin(query).Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	if !engine.KeepsDecisions("lowered") {
		t.Fatal("fixture: the group whose first round follows its lowering is not in probation")
	}
	evaluated.Trace.QueryGroupKey = "lowered"
	if allocs := testing.AllocsPerRun(200, func() { tap.Observe(ctx, evaluated) }); allocs == 0 {
		t.Fatal("the tap turned away a series of a group in probation")
	}
}

// The lookback asks the read-hold controller for a probe's kept rounds, and writes them, every so many clean rounds:
// the two counts are one.
func TestAProbesProgressIsWrittenAsOftenAsTheControllerKeepsIt(t *testing.T) {
	if lookback.FloorProbeProgressEvery != readhold.ProbeProgressEvery {
		t.Fatalf("the lookback writes a probe's progress every %d rounds, the controller keeps it every %d",
			lookback.FloorProbeProgressEvery, readhold.ProbeProgressEvery)
	}
}
