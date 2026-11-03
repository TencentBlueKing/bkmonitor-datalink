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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The two things a timed-out round leaves behind, read together on one
// series: the Plan's read-gap marker, which keeps every recovery window
// unsettled while it is open, and the Level's own guard, which carries the
// reason and the Slot that first stamped it.
//
// A three minute window, one anomaly needed, one recovery window. The series
// was anomalous at :57 and is normal at :00 and :01, read in the Slot a
// minute after each, so both windows' holes are behind the read end. With no
// marker that is a settled quiet window and the alert closes; with the marker
// open it is not, and the Level stays UNKNOWN under the carried QUERY_TIMEOUT
// - over two rounds, the second built from the State the first wrote, the
// origin staying the first stamp's Slot and cause throughout.
//
// The query line for these rounds is not reachable here: the worker builds it
// from the EvaluationResult this returns. What it reads is pinned below - a
// degraded result, QUERY_TIMEOUT, carried, from S0 - and
// TestAWholeQueryUnderACarriedTimeoutGuardReportsSuccessBesideTheEvaluation
// (worker) pins that a whole query under exactly that result logs
// result=success with evaluation_reason_carried=true and that origin.
func TestAnOpenReadGapHoldsRecoveryWhileTheLevelCarriesItsFirstStampedOrigin(t *testing.T) {
	plan := compiledWindow(t, 3, 1)
	if got := plan.RequiredFullSlots(); got != 3 {
		t.Fatalf("marker warmup %d Slots, want W + R - 1 = 3", got)
	}
	fingerprint := plan.Levels().At(0).Fingerprints().Detect
	const (
		anomalyAt = int64(5820)
		firstAt   = int64(6000)
		secondAt  = int64(6060)
	)
	stampedAt := execution.EvaluationTime(5880)
	reason := execution.ReasonCode(contract.ReasonQueryTimeout)
	origin := execution.ReasonOrigin{Slot: stampedAt, Cause: reason}
	record := func(id string, at int64) []contract.CanonicalRecordV2 {
		return []contract.CanonicalRecordV2{{RecordID: strings.Repeat(id, 64), SourceTime: at, BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`10`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: at}}
	}
	anomalous := []execution.StateHistoryPoint{{RecordID: strings.Repeat("d", 64), SourceTime: anomalyAt,
		Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: fingerprint, Result: execution.LevelFactAnomalous}}}}
	marker := func(t *testing.T, request *execution.EvaluationRequest, observed uint32) {
		t.Helper()
		due := request.Header.DuePlans[0]
		version, err := execution.BuildApplyVersion(request.Header.Contract, due.StateApplyEpoch)
		if err != nil {
			t.Fatal(err)
		}
		status := execution.GapStatusGapped
		if observed > 0 {
			status = execution.GapStatusWarming
		}
		request.Gaps.Items[0] = execution.GapGuardSnapshot{
			Identity:       execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
			MarkerRevision: 2, PersistedApplyVersion: version, PersistedMutationDigest: "gap-digest",
			Status: execution.GapFound, LastScheduleRevision: due.ScheduleRevision,
			Scopes: []execution.GapScopeState{{
				Scope:  execution.GapScope{HasLevel: true, LevelID: 5},
				Status: status, ReasonCode: reason, RequiredFullSlots: plan.RequiredFullSlots(), ObservedFullSlots: observed,
				ReasonOriginSlot: origin.Slot, ReasonOriginCause: origin.Cause,
			}},
		}
	}
	// The first round: the Level loaded GAPPED under the guard S0 stamped.
	firstRound := func(t *testing.T, open bool) execution.EvaluationRequest {
		t.Helper()
		request := atSlot(t, plan, requestFixtureForPlan(t, plan, record("b", firstAt), anomalous), firstAt+60)
		view := &request.State.Items[0]
		view.Status = execution.StateFoundGapped
		level := &view.Levels[0]
		level.HistoryCompleteness, level.GapReasonCode = execution.HistoryGapped, reason
		level.GapReasonOriginSlot = uint32(origin.Slot)
		level.LastProcessedEventTime = anomalyAt
		if open {
			marker(t, &request, 0)
		}
		return request
	}
	evaluate := func(t *testing.T, request execution.EvaluationRequest) execution.EvaluationResult {
		t.Helper()
		result, err := newEvaluator(t).Evaluate(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		if err := result.Validate(request); err != nil {
			t.Fatalf("evaluation did not validate: %v", err)
		}
		return result
	}
	// The Level is written with the completeness the marker forces - GAPPED
	// while it has counted no whole round, WARMING once it has - and with the
	// first stamp's reason and origin either way.
	held := func(t *testing.T, round string, result execution.EvaluationResult, completeness execution.HistoryCompleteness) execution.StateMutation {
		t.Helper()
		if len(result.Plans) != 1 || len(result.Plans[0].LevelOutcomes) != 1 {
			t.Fatalf("%s: plans %+v, want one Level outcome", round, result.Plans)
		}
		outcome := result.Plans[0].LevelOutcomes[0]
		if outcome.Outcome == execution.LevelOutcomeRecovery {
			t.Fatalf("%s: RECOVERY while the read gap is open: a round nobody read counted as a quiet minute", round)
		}
		if outcome.Outcome != execution.LevelOutcomeUnknown || outcome.ReasonCode != reason || !outcome.ReasonCarried ||
			outcome.ReasonOrigin() != origin {
			t.Fatalf("%s: outcome %+v, want UNKNOWN %s carried from %+v", round, outcome, reason, origin)
		}
		if result.Result != observability.ResultDegraded || result.ReasonCode != reason || !result.ReasonCarried ||
			result.ReasonOrigin != origin {
			t.Fatalf("%s: result %s/%s carried %t origin %+v, want degraded/%s carried from %+v", round,
				result.Result, result.ReasonCode, result.ReasonCarried, result.ReasonOrigin, reason, origin)
		}
		states := result.Plans[0].StateResults
		if len(states) != 1 || len(states[0].Mutation.Levels) != 1 || len(states[0].Events) != 0 {
			t.Fatalf("%s: state results %+v, want the guard rewritten and no event", round, states)
		}
		level := states[0].Mutation.Levels[0]
		if level.HistoryCompleteness != completeness || level.GapReasonCode != reason || level.ReasonOrigin() != origin {
			t.Fatalf("%s: guard written as %s/%s from %+v, want %s/%s from the first stamp %+v", round,
				level.HistoryCompleteness, level.GapReasonCode, level.ReasonOrigin(), completeness, reason, origin)
		}
		return states[0].Mutation
	}

	// The fixture separates the two: with the same Level guard and no marker,
	// the settled hole closes the alert. The carried guard alone does not hold
	// a recovery; only the marker does.
	if outcome := evaluate(t, firstRound(t, false)).Plans[0].LevelOutcomes[0]; outcome.Outcome != execution.LevelOutcomeRecovery {
		t.Fatalf("with no read gap open the first round is %s, want RECOVERY; the fixture does not separate the two", outcome.Outcome)
	}

	first := firstRound(t, true)
	mutation := held(t, "first round", evaluate(t, first), execution.HistoryGapped)

	// The second round reads what the first wrote: its history and its Level
	// state, origin included. The marker has counted one whole round of three.
	carried, err := applyProvisional(first.State.Items[0], mutation)
	if err != nil {
		t.Fatal(err)
	}
	second := atSlot(t, plan, requestFixtureForPlan(t, plan, record("e", secondAt), nil), secondAt+60)
	carried.VersionComparison = second.State.Items[0].VersionComparison
	second.State.Items[0] = carried
	marker(t, &second, 1)
	if got := second.State.Items[0].Levels[0].ReasonOrigin(); got != origin {
		t.Fatalf("second round loads the guard from %+v, want %+v", got, origin)
	}
	held(t, "second round", evaluate(t, second), execution.HistoryWarming)
}
