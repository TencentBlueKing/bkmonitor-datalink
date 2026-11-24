// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// 2.4: a Slot over a per-Slot cap after some of its windows were written.
// Two no-data Plans have been judged: the first's synthetic series - its
// absences - went out in an earlier window and the evaluation moved past it;
// the second's were being evaluated when the cap was passed. The first keeps
// its round: its memory is written with the tail, so the next round decides
// from it and does not send those absences again. The second is answered
// UNAVAILABLE and writes no memory nor ends: its present series are only the
// ones its landed windows got to.
func TestABudgetHitAfterWindowsKeepsTheMemoryOfAFullyLandedPlanAndWritesNoneForAPartlyLandedOne(t *testing.T) {
	fixture := newWindowedFixture(t)
	stream := fixture.stream
	ctx := context.Background()
	config := &contract.NoDataConfigV1{Continuous: 3, Level: 2, AggDimension: []string{"host"}}
	plan := func(strategyID string) execution.DuePlan {
		return execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: strategyID},
			CompiledPlan: noDataPreflightPlan(t, strategyID, config), StateGeneration: execution.StateGeneration("generation-" + strategyID),
			StateApplyEpoch: 1, ScheduleRevision: execution.PlanScheduleRevision("schedule-" + strategyID)}
	}
	landed, cut := plan("7"), plan("8")
	stream.header.DuePlans = []execution.DuePlan{landed, cut}
	// The Slot's gap load answered for each Plan, as it does for every due
	// one: neither has a marker yet.
	stream.gaps = execution.GapLoadResult{Items: []execution.GapGuardSnapshot{
		{Identity: landed.GapIdentity(), Status: execution.GapMissing},
		{Identity: cut.GapIdentity(), Status: execution.GapMissing}}}
	stream.evaluated = execution.EvaluationResult{Contract: stream.header.Contract, Result: observability.ResultSuccess,
		ReasonCode: observability.ReasonNone, Plans: []execution.PlanEvaluationResult{
			{Plan: landed.Identity, Disposition: execution.PlanDecided}, {Plan: cut.Identity, Disposition: execution.PlanDecided}}}
	stream.windows.started = true
	stream.windows.apply = &slotApply{coordinator: stream.coordinator, request: stream.request, header: stream.header,
		withheldNoData: make(map[execution.PlanIdentity]struct{})}
	// Judged in Plan order, as evaluateNoData judges them, each with its
	// memory; the evaluation is on the second's synthetic series.
	for _, due := range stream.header.DuePlans {
		stream.recordNoDataOutcome(ctx, due, nodata.OutcomeEvaluated)
		stream.noDataMutations = append(stream.noDataMutations, execution.PlanNoDataMutation{Identity: due.NoDataIdentity()})
		stream.beginEvaluating(due.Identity)
	}

	if err := stream.completeWindowsBeyondSlotBudget(ctx); err != nil {
		t.Fatal(err)
	}

	if _, replaced := stream.windows.replaced[landed.Identity]; replaced {
		t.Fatal("the Plan every window of which landed was answered UNAVAILABLE")
	}
	if _, replaced := stream.windows.replaced[cut.Identity]; !replaced {
		t.Fatal("the Plan the cap cut short was not answered UNAVAILABLE")
	}
	written := withoutWithheldNoData(stream.noDataMutations, stream.windows.apply.withheldNoData)
	if len(written) != 1 || written[0].Identity.Plan != landed.Identity {
		t.Fatalf("the tail would write the memory of %+v, want only the fully landed Plan's", written)
	}
	if _, withheld := stream.windows.apply.withheldNoData[cut.Identity]; !withheld {
		t.Fatal("the partly landed Plan's no-data ends are not withheld")
	}
	for _, outcome := range []struct {
		due  execution.DuePlan
		want nodata.SlotOutcome
	}{{landed, nodata.OutcomeEvaluated}, {cut, nodata.OutcomeSkippedSlotBudget}} {
		if got := stream.noDataOutcomes[stream.noDataOutcomeIndex[outcome.due.Key()]]; got != outcome.want {
			t.Fatalf("Plan %s outcome = %s, want %s", outcome.due.Identity.StrategyID, got, outcome.want)
		}
	}
	var cutResult *execution.PlanEvaluationResult
	for index := range stream.evaluated.Plans {
		if stream.evaluated.Plans[index].Plan == cut.Identity {
			cutResult = &stream.evaluated.Plans[index]
		}
	}
	if cutResult == nil || len(cutResult.GuardBeforeEvents) == 0 ||
		cutResult.GuardBeforeEvents[0].Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonExecutionBudgetExhausted) {
		t.Fatalf("the cut Plan's result = %+v, want its budget gap", cutResult)
	}
}
