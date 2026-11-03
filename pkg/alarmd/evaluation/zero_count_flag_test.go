// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

// A zero is no observation for a plan whose own primary says so
// (DataRequirement.ZeroIsNoObservation, read off the plan's query); another
// plan's primary saying so, and a dependency saying so, leave it an
// observation.
func TestAZeroCountIsNoObservationOnlyWhereThePlansPrimarySaysSo(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	other := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "8"}
	requirement := func(role execution.InputRole, consumer execution.PlanIdentity, zero bool) execution.DataRequirement {
		return execution.DataRequirement{Role: role, ZeroIsNoObservation: zero,
			Consumers: []execution.DataRequirementConsumer{{Consumer: execution.ConsumerRef{Plan: consumer}}}}
	}
	for _, test := range []struct {
		name         string
		requirements []execution.DataRequirement
		want         bool
	}{
		{"the plan's primary says so", []execution.DataRequirement{requirement(execution.InputRolePrimary, plan, true)}, true},
		{"the plan's primary does not", []execution.DataRequirement{requirement(execution.InputRolePrimary, plan, false)}, false},
		{"another plan's primary says so", []execution.DataRequirement{requirement(execution.InputRolePrimary, other, true), requirement(execution.InputRolePrimary, plan, false)}, false},
		{"a dependency says so", []execution.DataRequirement{requirement(execution.InputRoleAlgorithmDependency, plan, true)}, false},
		// A grouped log count: unfilled, and its zero an observation, which
		// recovers whatever its record carries.
		{"a grouped log count", []execution.DataRequirement{func() execution.DataRequirement {
			grouped := requirement(execution.InputRolePrimary, plan, false)
			grouped.UnfilledCounts = true
			return grouped
		}()}, false},
	} {
		if got := zeroCountIsNoObservation(test.requirements, plan); got != test.want {
			t.Errorf("%s: zeroCountIsNoObservation = %t, want %t", test.name, got, test.want)
		}
	}
}

// A Level outcome carries the withheld reason its decision window names; one
// withheld for nothing and one with no window carry none.
func TestWithheldCodeReadsTheDecisionWindow(t *testing.T) {
	window := func(withheld string) *contract.DecisionWindowV1 {
		return &contract.DecisionWindowV1{Recovery: contract.RecoveryWindowEvidenceV1{Withheld: withheld}}
	}
	for _, test := range []struct {
		outcome trigger.LevelOutcomeV2
		want    execution.RecoveryWithheldCode
	}{
		{trigger.LevelOutcomeV2{DecisionWindow: window(contract.RecoveryWithheldZeroCount)}, execution.RecoveryWithheldZeroCount},
		{trigger.LevelOutcomeV2{DecisionWindow: window(contract.RecoveryWithheldNoDataLastPointUnknown)}, execution.RecoveryWithheldNoDataLastPointUnknown},
		{trigger.LevelOutcomeV2{DecisionWindow: window("")}, execution.RecoveryWithheldNone},
		{trigger.LevelOutcomeV2{}, execution.RecoveryWithheldNone},
	} {
		if got := withheldCode(test.outcome); got != test.want {
			t.Errorf("withheldCode(%+v) = %d, want %d", test.outcome.DecisionWindow, got, test.want)
		}
	}
}

// Through the evaluator: a Plan whose primary reads event counts, whose two
// Levels triggered on the round before, reads a zero. Each Level met its
// recovery window and stays NORMAL, counted as withheld, and no envelope
// goes; the result contract accepts it. Without the word the same zero
// recovers both and counts nothing.
func TestTheEvaluatorWithholdsAndCountsARecoveryOnAZeroEventCount(t *testing.T) {
	for _, arm := range []struct {
		name         string
		eventCounts  bool
		wantOutcome  execution.LevelOutcomeKind
		wantWithheld uint64
		wantEvents   int
	}{
		{"event count", true, execution.LevelOutcomeNormal, 2, 0},
		{"time series", false, execution.LevelOutcomeRecovery, 0, 1},
	} {
		t.Run(arm.name, func(t *testing.T) {
			plan := compiledTwoLevelsShaped(t, "50", "50", nativeOutputForTest)
			history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
				{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
				{LevelID: 6, DetectFingerprint: plan.Levels().At(1).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
			}}}
			req := requestFixtureTwoLevels(t, plan, json.RawMessage(`0`), history)
			req.Header.Requirements[0].ZeroIsNoObservation = arm.eventCounts
			req.OpenAlerts = oneTimeCloseStub{}
			result, err := newEvaluator(t).Evaluate(context.Background(), req)
			if err != nil {
				t.Fatalf("Evaluate()=%v", err)
			}
			plan0 := result.Plans[0]
			for _, outcome := range plan0.LevelOutcomes {
				if outcome.Outcome != arm.wantOutcome {
					t.Fatalf("outcomes=%+v want every Level %s", plan0.LevelOutcomes, arm.wantOutcome)
				}
			}
			var withheld uint64
			for _, outcome := range plan0.LevelOutcomes {
				if outcome.RecoveryWithheld != execution.RecoveryWithheldNone {
					withheld++
				}
			}
			if withheld != arm.wantWithheld {
				t.Fatalf("withheld=%d want %d", withheld, arm.wantWithheld)
			}
			if len(plan0.StateResults) != 1 || len(plan0.StateResults[0].Events) != arm.wantEvents {
				t.Fatalf("state results=%+v want one with %d envelopes", plan0.StateResults, arm.wantEvents)
			}
			if err := result.Validate(req); err != nil {
				t.Fatalf("the result contract refused the evaluator's own result: %v", err)
			}
		})
	}
}

// Through the evaluator, on a Plan written to end on no data whose primary
// reads event counts: the Level state's last real point reaches the trigger
// - stored, the zero's recovery is withheld as zero_count; not stored, as
// no_data_last_point_unknown - and the state written keeps it where it was
// on a zero and moves it to the record on a count above zero. A Plan not
// written so writes none.
func TestTheEvaluatorReadsAndKeepsTheLastRealPoint(t *testing.T) {
	compile := func(missingData bool) *strategy.CompiledPlan {
		return compiledTwoLevelsShaped(t, "50", "50", func(p *contract.EvaluationPlanV2) {
			nativeOutputForTest(p)
			if !missingData {
				return
			}
			for index := range p.StrategyIR.Levels {
				p.StrategyIR.Levels[index].RecoveryPlan.Config = json.RawMessage(`{"enabled":true,"consecutive_windows":1,"missing_data":"evaluate_as_zero"}`)
			}
		})
	}
	run := func(plan *strategy.CompiledPlan, value string, stored int64) execution.PlanEvaluationResult {
		t.Helper()
		history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
			{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
			{LevelID: 6, DetectFingerprint: plan.Levels().At(1).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
		}}}
		req := requestFixtureTwoLevels(t, plan, json.RawMessage(value), history)
		req.Header.Requirements[0].ZeroIsNoObservation = true
		req.OpenAlerts = oneTimeCloseStub{}
		for index := range req.State.Items[0].Levels {
			req.State.Items[0].Levels[index].LastRealPointTime = stored
		}
		result, err := newEvaluator(t).Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("Evaluate()=%v", err)
		}
		if err := result.Validate(req); err != nil {
			t.Fatalf("the result contract refused the evaluator's own result: %v", err)
		}
		return result.Plans[0]
	}
	written := func(plan execution.PlanEvaluationResult) []int64 {
		var times []int64
		for _, level := range plan.StateResults[0].Mutation.Levels {
			times = append(times, level.LastRealPointTime)
		}
		return times
	}
	withheld := func(plan execution.PlanEvaluationResult) []execution.RecoveryWithheldCode {
		var codes []execution.RecoveryWithheldCode
		for _, outcome := range plan.LevelOutcomes {
			codes = append(codes, outcome.RecoveryWithheld)
		}
		return codes
	}

	zero := run(compile(true), `0`, 40)
	if got := withheld(zero); len(got) != 2 || got[0] != execution.RecoveryWithheldZeroCount || got[1] != execution.RecoveryWithheldZeroCount {
		t.Fatalf("stored last point: withheld %v, want zero_count on both Levels", got)
	}
	if got := written(zero); len(got) != 2 || got[0] != 40 || got[1] != 40 {
		t.Fatalf("a zero wrote last real points %v, want the stored 40 kept", got)
	}
	unknown := run(compile(true), `0`, 0)
	if got := withheld(unknown); got[0] != execution.RecoveryWithheldNoDataLastPointUnknown || got[1] != execution.RecoveryWithheldNoDataLastPointUnknown {
		t.Fatalf("no stored last point: withheld %v, want no_data_last_point_unknown on both Levels", got)
	}
	real := run(compile(true), `5`, 40)
	if got := written(real); got[0] != 100 || got[1] != 100 {
		t.Fatalf("a count above zero wrote last real points %v, want the record's 100", got)
	}
	plain := run(compile(false), `5`, 0)
	if got := written(plain); got[0] != 0 || got[1] != 0 {
		t.Fatalf("a Plan not written to end on no data wrote last real points %v, want none", got)
	}
}

// The last real point moves to a record that is a real point, stays on a
// zero that is no observation - unless a read gap of the Level is open,
// when the record counts as real whatever its value, since the unread round
// may have held one - and never goes back.
func TestTheLastRealPointMovesOnRealPointsAndThroughReadGaps(t *testing.T) {
	plan := compiledTwoLevelsShaped(t, "50", "50", func(p *contract.EvaluationPlanV2) {
		p.StrategyIR.Levels[0].RecoveryPlan.Config = json.RawMessage(`{"enabled":true,"consecutive_windows":1,"missing_data":"evaluate_as_zero"}`)
	})
	due := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}, CompiledPlan: plan, StateGeneration: "state-v1"}
	dataset := execution.NewDataset([]contract.CanonicalRecordV2{{
		RecordID: strings.Repeat("b", 64), SourceTime: 100, BusinessID: "2",
		DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
		Values:            map[string]json.RawMessage{"value": json.RawMessage(`0`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 100,
	}})
	datasetView, err := execution.NewDatasetView(dataset, []uint32{0})
	if err != nil {
		t.Fatal(err)
	}
	record, _ := datasetView.Record(0)
	view := func(stored int64) execution.RuntimeStateView {
		return execution.RuntimeStateView{Levels: []execution.RuntimeLevelStateView{{LevelID: 5, LastRealPointTime: stored}}}
	}
	zero := []trigger.LevelOutcomeV2{{LevelID: 5}}
	real := []trigger.LevelOutcomeV2{{LevelID: 5, RealPoint: true}}
	gap := execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{Identity: due.GapIdentity(), Status: execution.GapFound,
		Scopes: []execution.GapScopeState{{Scope: execution.GapScope{HasLevel: true, LevelID: 5}, Status: execution.GapStatusGapped}}}}}
	for name, test := range map[string]struct {
		gaps     execution.GapLoadResult
		outcomes []trigger.LevelOutcomeV2
		stored   int64
		want     int64
	}{
		"a real point":                {execution.GapLoadResult{}, real, 40, 100},
		"a zero":                      {execution.GapLoadResult{}, zero, 40, 40},
		"a zero under a read gap":     {gap, zero, 40, 100},
		"a real point before the one": {execution.GapLoadResult{}, real, 120, 120},
	} {
		if got := lastRealPointAfter(due, test.gaps, view(test.stored), record, test.outcomes, 5); got != test.want {
			t.Errorf("%s: last real point %d, want %d", name, got, test.want)
		}
	}
	if got := lastRealPointAfter(due, execution.GapLoadResult{}, view(40), record, real, 6); got != 0 {
		t.Errorf("a Level not written to end on no data kept a last real point %d", got)
	}
}

// nativeOutputForTest gives a Plan the consumer's protocol and an alert
// identity, so its series have an alert the open set can be asked about.
func nativeOutputForTest(p *contract.EvaluationPlanV2) {
	p.WireFormat = contract.WireFormatStandardRawEvent
	p.StrategyRef.SnapshotRevision = 7
	p.StrategyIR.StrategyRef.SnapshotRevision = 7
	p.OutputIdentity = &contract.MonitorOutputIdentity{DimensionFields: []string{"host"}}
}

// A recovery withheld on a zero is counted only where it holds an alert
// open: the same zeros on a series the consumer holds no alert for - a
// quiet series that never triggered - count nothing, though the decision
// window still says why the round did not recover.
func TestAWithheldRecoveryIsCountedOnlyWhereItHoldsAnAlertOpen(t *testing.T) {
	plan := compiledTwoLevelsShaped(t, "50", "50", nativeOutputForTest)
	history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
		{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
		{LevelID: 6, DetectFingerprint: plan.Levels().At(1).Fingerprints().Detect, Result: execution.LevelFactAnomalous},
	}}}
	req := requestFixtureTwoLevels(t, plan, json.RawMessage(`0`), history)
	req.Header.Requirements[0].ZeroIsNoObservation = true
	req.OpenAlerts = openAlertSetStub{}
	result, err := newEvaluator(t).Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for _, outcome := range result.Plans[0].LevelOutcomes {
		if outcome.Outcome != execution.LevelOutcomeNormal || outcome.RecoveryWithheld != execution.RecoveryWithheldNone {
			t.Fatalf("outcomes = %+v, want NORMAL and not counted withheld with no open alert", result.Plans[0].LevelOutcomes)
		}
	}
}
