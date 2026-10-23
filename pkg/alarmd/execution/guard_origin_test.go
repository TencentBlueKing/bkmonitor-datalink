// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// previousReleaseOriginFixtureDigest is the digest the previous release
// (d03b7a7b4, deployed as 0.4.4794) derives for originFixtureMutation without
// an origin, computed by that tree's BuildStateMutation over the same fields.
const previousReleaseOriginFixtureDigest = "2740e5776545d0f6a99515e0d824326462ed0b49951107b6dadcd3df0c752eae"

func originFixtureMutation(origin execution.ReasonOrigin) execution.StateMutation {
	level := execution.RuntimeLevelStateMutation{LevelID: 5, LevelStateCompatibility: strings.Repeat("1c", 32),
		HistoryCompleteness: execution.HistoryGapped, GapReasonCode: contract.ReasonQueryTimeout,
		WarmupRequirementRef: strings.Repeat("2d", 32), LastProcessedEventTime: 1_788_000_000}
	return execution.StateMutation{
		Identity: execution.StateKeyIdentity{Plan: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "301"},
			StateGeneration: "generation", SeriesIdentityDigest: execution.SeriesIdentityDigest(strings.Repeat("ab", 32))},
		ExpectedBlobRevision: 1,
		ApplyVersion:         execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: 1_788_000_000, SlotDigest: "slot"},
		AffectedRecords:      []execution.RecordAnchor{{RecordID: strings.Repeat("b", 64), SourceTime: 1_788_000_000}},
		Levels:               []execution.RuntimeLevelStateMutation{level.WithReasonOrigin(origin)},
		RetentionPoints:      5,
	}
}

// The origin describes the guard and decides nothing, so it stays out of the
// State mutation digest: a mutation carrying one has the digest the same
// mutation has without, which is the digest the previous release derives. A
// retry of the same Slot by either build is the same statement to the store,
// not a version conflict.
func TestAReasonOriginLeavesTheStateMutationDigestAlone(t *testing.T) {
	without, err := execution.BuildStateMutation(originFixtureMutation(execution.ReasonOrigin{}))
	if err != nil {
		t.Fatal(err)
	}
	with, err := execution.BuildStateMutation(originFixtureMutation(execution.ReasonOrigin{Slot: 1_787_999_940, Cause: contract.ReasonQueryTimeout}))
	if err != nil {
		t.Fatal(err)
	}
	if with.MutationDigest != without.MutationDigest {
		t.Fatalf("digest with origin %s, without %s: the origin reached the digest", with.MutationDigest, without.MutationDigest)
	}
	if string(without.MutationDigest) != previousReleaseOriginFixtureDigest {
		t.Fatalf("digest %s, the previous release derives %s", without.MutationDigest, previousReleaseOriginFixtureDigest)
	}
	if err := with.ValidateDigest(); err != nil {
		t.Fatalf("a mutation carrying an origin fails its own digest: %v", err)
	}
}

// The last real point stays out of the State mutation digest too: a build
// that does not write it - the old half of a rolling update, or the one
// rolled back to - reaches the same digest for the same Slot, so a retry is
// the same statement to the store and not a version conflict.
func TestALastRealPointLeavesTheStateMutationDigestAlone(t *testing.T) {
	without, err := execution.BuildStateMutation(originFixtureMutation(execution.ReasonOrigin{}))
	if err != nil {
		t.Fatal(err)
	}
	carrying := originFixtureMutation(execution.ReasonOrigin{})
	carrying.Levels[0].LastRealPointTime = 1_787_999_880
	with, err := execution.BuildStateMutation(carrying)
	if err != nil {
		t.Fatal(err)
	}
	if with.MutationDigest != without.MutationDigest || string(with.MutationDigest) != previousReleaseOriginFixtureDigest {
		t.Fatalf("digest with a last real point %s, without %s: it reached the digest", with.MutationDigest, without.MutationDigest)
	}
}

// A write whose Level state differs from the stored one only in the guard's
// origin is decision-stable for the reuse reading: the origin decides
// nothing, as it reaches no digest.
func TestAWriteThatDiffersOnlyInOriginIsDecisionStable(t *testing.T) {
	mutation, err := execution.BuildStateMutation(originFixtureMutation(execution.ReasonOrigin{Slot: 1_787_999_940, Cause: contract.ReasonQueryTimeout}))
	if err != nil {
		t.Fatal(err)
	}
	stored := mutation.Levels[0]
	view := execution.RuntimeStateView{Identity: mutation.Identity, PersistedMutationDigest: "an-earlier-statement",
		Levels: []execution.RuntimeLevelStateView{{LevelID: stored.LevelID, LevelStateCompatibility: stored.LevelStateCompatibility,
			HistoryCompleteness: stored.HistoryCompleteness, GapReasonCode: stored.GapReasonCode,
			WarmupRequirementRef: stored.WarmupRequirementRef, LastProcessedEventTime: stored.LastProcessedEventTime,
			GapReasonOriginSlot: 1_787_999_880}}}
	if got := execution.ClassifyStateWriteReuse(view, mutation); got != execution.StateWriteReuseDecisionStable {
		t.Fatalf("reuse = %s, want %s for a write differing only in origin", got, execution.StateWriteReuseDecisionStable)
	}
	view.Levels[0].GapReasonCode = contract.ReasonGapSkipped
	if got := execution.ClassifyStateWriteReuse(view, mutation); got != execution.StateWriteReuseChanged {
		t.Fatalf("reuse = %s, want %s when the reason itself differs", got, execution.StateWriteReuseChanged)
	}
}

// An origin is only on a Level that holds a guard: a stored Slot beside no
// reason is refused, not read as an origin.
func TestAStateMutationRefusesAnOriginWithoutAGuard(t *testing.T) {
	full := originFixtureMutation(execution.ReasonOrigin{})
	full.Levels[0].HistoryCompleteness, full.Levels[0].GapReasonCode = execution.HistoryFull, ""
	full.Levels[0].GapReasonOriginSlot = 1_787_999_940
	if _, err := execution.BuildStateMutation(full); err == nil {
		t.Fatal("an origin on a FULL Level was accepted")
	}
}

// A completion whose cause is a Level held only by a stored guard reports
// that its reason was carried and from where; one whose cause is the primary
// input reports a reason of this round.
func TestACompletionCauseSaysWhetherItsReasonWasCarried(t *testing.T) {
	input := validInternalExecution()
	plan := input.Inputs[0].Consumer.Plan
	origin := execution.ReasonOrigin{Slot: 1_787_999_940, Cause: contract.ReasonQueryTimeout}
	held := execution.PlanEvaluationResult{Plan: plan, Disposition: execution.PlanDecided,
		LevelOutcomes: []execution.LevelOutcome{execution.LevelOutcome{Plan: plan, LevelID: 3, Outcome: execution.LevelOutcomeUnknown,
			ReasonCode: contract.ReasonQueryTimeout, GuardTail: true, ReasonCarried: true}.WithReasonOrigin(origin)}}
	_, attribution, err := execution.DeriveCompletionAttribution(input, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{held}})
	if err != nil {
		t.Fatal(err)
	}
	if attribution.Cause != execution.CauseGapGuardWarming || !attribution.ReasonCarried || attribution.ReasonOrigin != origin {
		t.Fatalf("guard-held completion %+v, want GAP_GUARD_WARMING carried from %+v", attribution, origin)
	}
	cause := attribution.SlotCause()
	if facts := cause.CarriedReason(); facts == nil || facts.OriginSlot != int64(origin.Slot) || string(facts.OriginCause) != contract.ReasonQueryTimeout {
		t.Fatalf("completion line facts %+v, want the origin", facts)
	}

	unavailable := validInternalExecution()
	unavailable.Inputs[0].Completeness = execution.CompletenessUnavailable
	unavailable.Inputs[0].ReasonCode = contract.ReasonQueryTimeout
	_, attribution, err = execution.DeriveCompletionAttribution(unavailable, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{held}})
	if err != nil {
		t.Fatal(err)
	}
	if attribution.Cause != execution.CausePrimaryInputUnavailable || attribution.ReasonCarried || attribution.ReasonOrigin != (execution.ReasonOrigin{}) {
		t.Fatalf("primary timeout completion %+v, want this round's own reason", attribution)
	}
	if attribution.SlotCause().CarriedReason() != nil {
		t.Fatal("a primary timeout reads as carried on the completion line")
	}
}
