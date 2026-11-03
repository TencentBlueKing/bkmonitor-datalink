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
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// earlierSlot is an evaluation time before the fixtures' Slot (100): the
// round that stamped a guard these rounds carry.
const earlierSlot execution.EvaluationTime = 40

// A dependency that timed out this round raises the Level's guard: the State
// written for it carries where the guard came from - this Slot, the reason it
// stamped - and the outcome's reason is this round's own, not carried.
func TestAGuardRaisedThisRoundIsStampedWithThisSlotAndIsNotCarried(t *testing.T) {
	plan := compiledG4Plan(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20, "ceil": nil},
		strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
	request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{g4Record(99, `80`, nil)}, nil)
	input := g4Input(t, request, map[string][]contract.CanonicalRecordV2{"primary": {g4Record(99, `80`, nil)}})
	reason := execution.ReasonCode(contract.ReasonQueryTimeout)
	timedOut := false
	for index := range input.Inputs {
		binding := &input.Inputs[index]
		if binding.Role == execution.InputRolePrimary {
			continue
		}
		binding.Dataset, binding.View = nil, nil
		binding.Completeness, binding.DataState = execution.CompletenessUnavailable, execution.DataStateUnknown
		binding.Disposition, binding.ReasonCode, binding.ImpactScope = execution.AccessUnavailable, reason, execution.ImpactPlan
		timedOut = true
	}
	if !timedOut {
		t.Fatal("fixture has no dependency binding to time out")
	}
	result, err := newEvaluator(t).evaluateSeries(context.Background(), request.Header,
		[]execution.SeriesEvaluationInputRequest{input}, request.State, request.Gaps, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.LevelOutcomes) != 1 || result.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown ||
		result.LevelOutcomes[0].ReasonCode != reason {
		t.Fatalf("outcomes %+v, want one UNKNOWN under %s", result.LevelOutcomes, reason)
	}
	if outcome := result.LevelOutcomes[0]; outcome.ReasonCarried || outcome.ReasonOrigin() != (execution.ReasonOrigin{}) {
		t.Fatalf("this round's own timeout reads as carried: %+v", outcome)
	}
	if result.ReasonCarried {
		t.Fatalf("the Plan's reason reads as carried: %+v", result)
	}
	if len(result.StateResults) != 1 || len(result.StateResults[0].Mutation.Levels) != 1 {
		t.Fatalf("state results %+v, want the guard written", result.StateResults)
	}
	level := result.StateResults[0].Mutation.Levels[0]
	want := execution.ReasonOrigin{Slot: request.Header.Contract.Slot.EvaluationTime, Cause: reason}
	if level.HistoryCompleteness != execution.HistoryGapped || level.GapReasonCode != reason || level.ReasonOrigin() != want {
		t.Fatalf("guard written as %s/%s origin %+v, want GAPPED/%s origin %+v", level.HistoryCompleteness,
			level.GapReasonCode, level.ReasonOrigin(), reason, want)
	}
}

// guardedRequest is the ring-ratio Level loaded under a durable guard of
// reason with origin, on the two shapes a guard is carried through: held in
// place (a missing dependency point; nothing advances) and advancing still
// guarded (a hole before the record; the guard is rewritten).
func guardedRequests(t *testing.T, reason execution.ReasonCode, origin execution.ReasonOrigin) map[string]struct {
	request  execution.EvaluationRequest
	advances bool
} {
	projection := strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	config := func() map[string]any { return map[string]any{"floor": 20, "ceil": nil} }
	single := compiledG4Plan(t, strategy.DetectorKindSimpleRingRatio, config(), projection)
	double := compiledG4PlanWithTrigger(t, strategy.DetectorKindSimpleRingRatio, config(), projection, 2, 2)
	normal := func(plan *strategy.CompiledPlan, id string, sourceTime int64) execution.StateHistoryPoint {
		return execution.StateHistoryPoint{RecordID: strings.Repeat(id, 64), SourceTime: sourceTime,
			Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: plan.Levels().At(0).Fingerprints().Detect, Result: execution.LevelFactNormal}}}
	}
	build := func(plan *strategy.CompiledPlan, status execution.StateLoadStatus, completeness execution.HistoryCompleteness,
		history []execution.StateHistoryPoint, primary contract.CanonicalRecordV2, previous []contract.CanonicalRecordV2) execution.EvaluationRequest {
		request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{primary}, history)
		request.State.Items[0].Status = status
		level := &request.State.Items[0].Levels[0]
		level.HistoryCompleteness, level.GapReasonCode = completeness, reason
		level.GapReasonOriginSlot = uint32(origin.Slot)
		if len(history) != 0 {
			level.LastProcessedEventTime = history[len(history)-1].SourceTime
		}
		request.Inputs = []execution.SeriesEvaluationInputRequest{g4Input(t, request,
			map[string][]contract.CanonicalRecordV2{"primary": {primary}, "previous": previous})}
		return request
	}
	return map[string]struct {
		request  execution.EvaluationRequest
		advances bool
	}{
		"held in place": {request: build(single, execution.StateFoundGapped, execution.HistoryGapped, nil,
			g4Record(99, `80`, nil), []contract.CanonicalRecordV2{g4OffsetMissRecord(39, `100`)})},
		"advancing still guarded": {request: build(double, execution.StateFoundWarming, execution.HistoryWarming,
			[]execution.StateHistoryPoint{normal(double, "d", 180), normal(double, "e", 240)},
			g4Record(360, `80`, nil), []contract.CanonicalRecordV2{g4Record(300, `100`, nil)}), advances: true},
	}
}

// A Level held by a guard an earlier round raised says so: its UNKNOWN is the
// stored guard's reason, carried, with the origin the guard was stamped with
// - and a guard rewritten while carried keeps that origin rather than taking
// this Slot's.
func TestACarriedGuardReasonNamesItsOriginAndKeepsItWhenRewritten(t *testing.T) {
	reason := execution.ReasonCode(contract.ReasonQueryTimeout)
	origin := execution.ReasonOrigin{Slot: earlierSlot, Cause: reason}
	for name, test := range guardedRequests(t, reason, origin) {
		t.Run(name, func(t *testing.T) {
			result, err := newEvaluator(t).Evaluate(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			if err := result.Validate(test.request); err != nil {
				t.Fatalf("evaluation did not validate: %v", err)
			}
			plan := result.Plans[0]
			if len(plan.LevelOutcomes) != 1 {
				t.Fatalf("outcomes %+v", plan.LevelOutcomes)
			}
			outcome := plan.LevelOutcomes[0]
			if outcome.Outcome != execution.LevelOutcomeUnknown || outcome.ReasonCode != reason || !outcome.ReasonCarried ||
				outcome.ReasonOrigin() != origin {
				t.Fatalf("outcome %+v, want UNKNOWN %s carried from %+v", outcome, reason, origin)
			}
			if result.ReasonCode != reason || !result.ReasonCarried || result.ReasonOrigin != origin {
				t.Fatalf("result %s carried %t origin %+v, want %s carried from %+v", result.ReasonCode,
					result.ReasonCarried, result.ReasonOrigin, reason, origin)
			}
			if !test.advances {
				return
			}
			if len(plan.StateResults) != 1 || len(plan.StateResults[0].Mutation.Levels) != 1 {
				t.Fatalf("state results %+v, want the guard rewritten", plan.StateResults)
			}
			if got := plan.StateResults[0].Mutation.Levels[0].ReasonOrigin(); got != origin {
				t.Fatalf("rewritten guard origin %+v, want the original %+v kept", got, origin)
			}
		})
	}
}

// A guard stamped before origins were recorded - an older record - is still
// carried and says so, with no origin: none is invented, on the outcome or on
// the guard it rewrites.
func TestAGuardFromAnOlderRecordIsCarriedWithoutAnInventedOrigin(t *testing.T) {
	reason := execution.ReasonCode(contract.ReasonQueryTimeout)
	for name, test := range guardedRequests(t, reason, execution.ReasonOrigin{}) {
		t.Run(name, func(t *testing.T) {
			result, err := newEvaluator(t).Evaluate(context.Background(), test.request)
			if err != nil {
				t.Fatal(err)
			}
			outcome := result.Plans[0].LevelOutcomes[0]
			if !outcome.ReasonCarried || outcome.ReasonOrigin() != (execution.ReasonOrigin{}) {
				t.Fatalf("outcome %+v, want carried with no origin", outcome)
			}
			if test.advances {
				if got := result.Plans[0].StateResults[0].Mutation.Levels[0].ReasonOrigin(); got != (execution.ReasonOrigin{}) {
					t.Fatalf("rewritten guard origin %+v, want none invented", got)
				}
			}
		})
	}
}

// A Level that does not advance on a record whose mutation is written for
// another reason is carried as it stands, its origin with it, not
// restamped with this Slot.
func TestALevelCarriedAsItStandsKeepsItsOrigin(t *testing.T) {
	plan := compiledG4Plan(t, strategy.DetectorKindSimpleRingRatio, map[string]any{"floor": 20, "ceil": nil},
		strategy.AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}})
	request := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{g4Record(99, `80`, nil)}, nil)
	request.Inputs = []execution.SeriesEvaluationInputRequest{g4Input(t, request,
		map[string][]contract.CanonicalRecordV2{"primary": {g4Record(99, `80`, nil)}})}
	origin := execution.ReasonOrigin{Slot: earlierSlot, Cause: contract.ReasonQueryTimeout}
	view := request.State.Items[0]
	level := &view.Levels[0]
	level.HistoryCompleteness, level.GapReasonCode = execution.HistoryGapped, contract.ReasonQueryTimeout
	level.GapReasonOriginSlot = uint32(origin.Slot)
	level.LastProcessedEventTime = 39
	records, err := commonPrimaryRecords(request.Inputs)
	if err != nil || len(records) == 0 {
		t.Fatalf("records %d err %v", len(records), err)
	}
	mutation, err := buildMutation(request, request.Header.DuePlans[0], records[0], view, nil, nil,
		map[uint32]execution.HistoryCompleteness{},
		durableGuards{reasons: map[uint32]execution.ReasonCode{}, origins: map[uint32]execution.ReasonOrigin{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(mutation.Levels) != 1 || mutation.Levels[0].GapReasonCode != contract.ReasonQueryTimeout ||
		mutation.Levels[0].ReasonOrigin() != origin {
		t.Fatalf("carried Level %+v, want %s kept with origin %+v", mutation.Levels, contract.ReasonQueryTimeout, origin)
	}
}

// A Level held by a loaded gap marker carries the marker scope's reason and
// the scope's origin - the Slot that opened it - onto its outcome and onto
// the Level state the round writes under it.
func TestALevelUnderAGapMarkerCarriesTheScopesOrigin(t *testing.T) {
	req := unevidencedRecoveryFixture(t)
	due := req.Header.DuePlans[0]
	applyVersion, err := execution.BuildApplyVersion(req.Header.Contract, due.StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	reason := execution.ReasonCode(contract.ReasonGapSkipped)
	origin := execution.ReasonOrigin{Slot: earlierSlot, Cause: reason}
	req.Gaps.Items[0] = execution.GapGuardSnapshot{
		Identity:                execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
		MarkerRevision:          1,
		PersistedApplyVersion:   applyVersion,
		PersistedMutationDigest: "gap-digest",
		Status:                  execution.GapFound,
		LastScheduleRevision:    due.ScheduleRevision,
		Scopes: []execution.GapScopeState{{
			Status: execution.GapStatusGapped, ReasonCode: reason, RequiredFullSlots: 1,
			ReasonOriginSlot: origin.Slot, ReasonOriginCause: origin.Cause,
		}},
	}
	result, err := newEvaluator(t).Evaluate(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	plan := result.Plans[0]
	if len(plan.LevelOutcomes) != 1 || !plan.LevelOutcomes[0].ReasonCarried || plan.LevelOutcomes[0].ReasonOrigin() != origin {
		t.Fatalf("outcomes %+v, want %s carried from %+v", plan.LevelOutcomes, reason, origin)
	}
	if len(plan.StateResults) != 1 || plan.StateResults[0].Mutation.Levels[0].GapReasonCode != reason ||
		plan.StateResults[0].Mutation.Levels[0].ReasonOrigin() != origin {
		t.Fatalf("state %+v, want the Level guarded under %s from %+v", plan.StateResults, reason, origin)
	}
}
