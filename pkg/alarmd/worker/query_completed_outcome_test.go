// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// queryLine runs one accepted completion through observeQueryCompleted and
// gives back the observation and the line the logging observer wrote ("" when
// it wrote none).
func queryLine(t *testing.T, completion execution.QueryExecutionCompletion, evaluated execution.EvaluationResult) (observability.Observation, map[string]any) {
	t.Helper()
	var output bytes.Buffer
	limiter, err := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{Window: time.Hour, MaxEvents: 100, MaxScopes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := observability.NewScopedBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	logged := observability.NewLoggingObserver(observability.New("alarmd", &output), policy)
	var seen observability.Observation
	c := &SlotExecutionCoordinator{ports: Ports{Observer: observability.ObserverFunc(func(ctx context.Context, o observability.Observation) {
		seen = observability.NormalizeObservation(o)
		logged.Observe(ctx, o)
	})}}
	ctx := observability.ContextWithDivertDecision(context.Background(), observability.DivertDecisionFacts{
		ClassifiedAtUnixMilli: 1_700_000_000_000, BoundSource: "fallback"})
	c.observeQueryCompleted(ctx, execution.OperationNormal, time.Now(), completion, evaluated)
	if output.Len() == 0 {
		return seen, nil
	}
	return seen, renderedQueryLine(t, output.Bytes())
}

func wholeAnswer() execution.QueryExecutionCompletion {
	return execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{
		{PhysicalQuery: "physical-301", Completeness: execution.CompletenessFull, DataState: execution.DataStateData,
			RouteFacts: execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{AttemptNo: 1,
				Endpoint: "http://uq.example.test", Result: execution.RouteAttemptSucceeded}}}}}}
}

func timedOutAnswer() execution.QueryExecutionCompletion {
	return execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{
		{PhysicalQuery: "physical-301", Completeness: execution.CompletenessUnavailable, DataState: execution.DataStateUnknown,
			RouteFacts: execution.ProviderRouteFacts{Attempts: []execution.RouteAttemptFact{{AttemptNo: 1,
				Endpoint: "http://uq.example.test", Result: execution.RouteAttemptFailed, ReasonCode: contract.ReasonQueryTimeout,
				Detail: execution.TransportRouteDetail(execution.TransportFailureTimeout)}}}}}}
}

// The production shape: a query that answered whole in one attempt, on a
// round whose Level stayed UNKNOWN under the QUERY_TIMEOUT guard an earlier
// round's timeout raised. The query line says the query succeeded, and says
// beside it what the evaluation concluded, that the reason was carried, and
// from where. Nothing on it reads as this query failing.
func TestAWholeQueryUnderACarriedTimeoutGuardReportsSuccessBesideTheEvaluation(t *testing.T) {
	origin := execution.ReasonOrigin{Slot: 1_700_000_040, Cause: contract.ReasonQueryTimeout}
	seen, line := queryLine(t, wholeAnswer(), execution.EvaluationResult{Result: observability.ResultDegraded,
		ReasonCode: contract.ReasonQueryTimeout, ReasonCarried: true, ReasonOrigin: origin})
	if seen.Result != observability.ResultSuccess || seen.ReasonCode != observability.ReasonNone {
		t.Fatalf("query result %s/%s, want success/none", seen.Result, seen.ReasonCode)
	}
	if seen.EvaluationResult != observability.ResultDegraded || seen.EvaluationReason != contract.ReasonQueryTimeout {
		t.Fatalf("evaluation %s/%s, want degraded/QUERY_TIMEOUT", seen.EvaluationResult, seen.EvaluationReason)
	}
	if seen.CarriedReason == nil || seen.CarriedReason.OriginSlot != int64(origin.Slot) || seen.CarriedReason.OriginCause != contract.ReasonQueryTimeout {
		t.Fatalf("carried %+v, want the origin %+v", seen.CarriedReason, origin)
	}
	if seen.QueryFailure != nil || seen.DivertDecision != nil || seen.QueryBudget != nil {
		t.Fatalf("a whole query carries failure facts %+v, decision %+v, budget %+v", seen.QueryFailure, seen.DivertDecision, seen.QueryBudget)
	}
	if line == nil {
		t.Fatal("the line was not written: a query held by its evaluation is not a routine success")
	}
	want := map[string]any{
		"result": "success", "reason_code": string(observability.ReasonNone), "level": "INFO",
		"evaluation_result": "degraded", "evaluation_reason": contract.ReasonQueryTimeout,
		"evaluation_reason_carried": true, "evaluation_reason_origin_slot": float64(origin.Slot),
		"evaluation_reason_origin_cause": contract.ReasonQueryTimeout,
	}
	for key, value := range want {
		if line[key] != value {
			t.Fatalf("%s = %v, want %v; line %v", key, line[key], value, line)
		}
	}
	for key := range line {
		if len(key) > len("failure_") && key[:len("failure_")] == "failure_" {
			t.Fatalf("a whole query's line names a failure field %s: %v", key, line)
		}
	}
}

// The round TestAnOpenReadGapHoldsRecoveryWhileTheLevelCarriesItsFirstStampedOrigin
// (evaluation) evaluates, seen from the query line. The series' Level loaded
// GAPPED under the QUERY_TIMEOUT guard Slot 5880 stamped, the Plan's read-gap
// marker is open from the same origin, and this round's physical query
// answered whole. The EvaluationResult below is the one that round returns:
// the Level held UNKNOWN under the carried reason, its guard rewritten GAPPED
// from the first stamp, and the open marker counted forward as a WARMUP scope
// after State.
//
// The query line says the query succeeded and nothing on it reads as this
// query failing or being diverted: no failure facts, no divert decision, no
// budget. Beside it, the evaluation's reason, carried, from 5880.
func TestAWholeQueryUnderAnOpenReadGapAndACarriedTimeoutGuardReportsSuccess(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "307"}
	reason := execution.ReasonCode(contract.ReasonQueryTimeout)
	origin := execution.ReasonOrigin{Slot: 5880, Cause: reason}
	series := execution.SeriesIdentityDigest(strings.Repeat("c", 64))
	record := execution.RecordAnchor{RecordID: strings.Repeat("b", 64), SourceTime: 6000}
	evaluated := execution.EvaluationResult{
		Result: observability.ResultDegraded, ReasonCode: reason, ReasonCarried: true, ReasonOrigin: origin,
		Plans: []execution.PlanEvaluationResult{{
			Plan: plan, Disposition: execution.PlanDecidedDegraded, ReasonCode: reason,
			ReasonCarried: true, ReasonOrigin: origin,
			LevelOutcomes: []execution.LevelOutcome{execution.LevelOutcome{Plan: plan, LevelID: 5, SeriesIdentityDigest: series, Record: record,
				Outcome: execution.LevelOutcomeUnknown, ReasonCode: reason, GuardTail: true,
				ReasonCarried: true}.WithReasonOrigin(origin)},
			StateResults: []execution.StateEvaluation{{Mutation: execution.StateMutation{
				Identity: execution.StateKeyIdentity{Plan: plan, StateGeneration: "state-v1", SeriesIdentityDigest: series},
				Levels: []execution.RuntimeLevelStateMutation{{LevelID: 5, HistoryCompleteness: execution.HistoryGapped,
					GapReasonCode: reason, LastProcessedEventTime: 6000,
					GapReasonOriginSlot: uint32(origin.Slot)}},
			}}},
			GuardAfterState: []execution.PlanGapMutation{{
				Identity: execution.PlanGapIdentity{Plan: plan, StateGeneration: "state-v1"}, ExpectedMarkerRevision: 2,
				Scopes: []execution.GapScopeMutation{{Scope: execution.GapScope{HasLevel: true, LevelID: 5},
					Kind: execution.GapWarmup, ReasonCode: reason, RequiredFullSlots: 3}},
			}},
		}},
	}
	seen, line := queryLine(t, wholeAnswer(), evaluated)
	if seen.Result != observability.ResultSuccess || seen.ReasonCode != observability.ReasonNone {
		t.Fatalf("query result %s/%s, want success/none: the query answered whole", seen.Result, seen.ReasonCode)
	}
	if seen.QueryFailure != nil || seen.DivertDecision != nil || seen.QueryBudget != nil {
		t.Fatalf("a whole query under an open read gap carries failure facts %+v, decision %+v, budget %+v",
			seen.QueryFailure, seen.DivertDecision, seen.QueryBudget)
	}
	if line == nil {
		t.Fatal("the line was not written: a query held by its evaluation is not a routine success")
	}
	want := map[string]any{
		"result": "success", "reason_code": string(observability.ReasonNone),
		"evaluation_result": "degraded", "evaluation_reason": contract.ReasonQueryTimeout,
		"evaluation_reason_carried": true, "evaluation_reason_origin_slot": float64(5880),
		"evaluation_reason_origin_cause": contract.ReasonQueryTimeout,
	}
	for key, value := range want {
		if line[key] != value {
			t.Fatalf("%s = %v, want %v; line %v", key, line[key], value, line)
		}
	}
	for key := range line {
		for _, prefix := range []string{"failure_", "divert_", "budget_"} {
			if strings.HasPrefix(key, prefix) {
				t.Fatalf("a whole query's line names %s: the round lost nothing of its own; line %v", key, line)
			}
		}
	}
}

// A guard stamped before origins were recorded is still carried on the line:
// carried=true with no origin fields, never physical.
func TestAGuardWithoutAnOriginReadsCarriedWithNoOrigin(t *testing.T) {
	seen, line := queryLine(t, wholeAnswer(), execution.EvaluationResult{Result: observability.ResultDegraded,
		ReasonCode: contract.ReasonQueryTimeout, ReasonCarried: true})
	if seen.CarriedReason == nil || seen.CarriedReason.Known() {
		t.Fatalf("carried %+v, want carried with no origin", seen.CarriedReason)
	}
	if line == nil || line["evaluation_reason_carried"] != true {
		t.Fatalf("line %v, want evaluation_reason_carried=true", line)
	}
	for _, key := range []string{"evaluation_reason_origin_slot", "evaluation_reason_origin_cause"} {
		if _, present := line[key]; present {
			t.Fatalf("line names %s for a guard that recorded none: %v", key, line)
		}
	}
}

// A query that really timed out is a query failure: degraded under the
// timeout, with its failure facts, and its reason is this round's - not
// carried, with no origin.
func TestARealTimeoutIsTheQuerysOwnFailureAndNotCarried(t *testing.T) {
	seen, line := queryLine(t, timedOutAnswer(), execution.EvaluationResult{Result: observability.ResultDegraded,
		ReasonCode: contract.ReasonQueryTimeout})
	if seen.Result != observability.ResultDegraded || seen.ReasonCode != contract.ReasonQueryTimeout {
		t.Fatalf("query result %s/%s, want degraded/QUERY_TIMEOUT", seen.Result, seen.ReasonCode)
	}
	if seen.QueryFailure == nil || seen.QueryFailure.Code != contract.ReasonQueryTimeout {
		t.Fatalf("failure facts %+v, want the timeout", seen.QueryFailure)
	}
	if seen.CarriedReason != nil {
		t.Fatalf("a real timeout reads as carried: %+v", seen.CarriedReason)
	}
	if line == nil || line["failure_code"] != contract.ReasonQueryTimeout || line["evaluation_reason_carried"] != nil {
		t.Fatalf("line %v, want failure_code QUERY_TIMEOUT and no carried field", line)
	}
}

// A timeout this round beside an evaluation whose reason is a stored guard's:
// the query line reports the query's own failure, not the guard's word.
func TestARealTimeoutUnderACarriedEvaluationReportsTheQuerysOwnReason(t *testing.T) {
	seen, _ := queryLine(t, timedOutAnswer(), execution.EvaluationResult{Result: observability.ResultDegraded,
		ReasonCode: contract.ReasonGapSkipped, ReasonCarried: true,
		ReasonOrigin: execution.ReasonOrigin{Slot: 1_700_000_040, Cause: contract.ReasonGapSkipped}})
	if seen.Result != observability.ResultDegraded || seen.ReasonCode != contract.ReasonQueryTimeout {
		t.Fatalf("query result %s/%s, want degraded under the query's own QUERY_TIMEOUT", seen.Result, seen.ReasonCode)
	}
	if seen.EvaluationReason != contract.ReasonGapSkipped || seen.CarriedReason == nil {
		t.Fatalf("evaluation %s carried %+v, want GAP_SKIPPED carried", seen.EvaluationReason, seen.CarriedReason)
	}
}

// An evaluation outcome with no reason is still explained, by the
// evaluation's own category, though the query beside it succeeded: the fleet
// files it under evaluation, and it must keep reading it.
func TestAnUnexplainedEvaluationIsStillExplainedBesideAWholeQuery(t *testing.T) {
	seen, _ := queryLine(t, wholeAnswer(), execution.EvaluationResult{Result: observability.ResultDegraded})
	if seen.Result != observability.ResultSuccess {
		t.Fatalf("query result %s, want success", seen.Result)
	}
	if seen.QueryFailure == nil || seen.QueryFailure.Category != observability.QueryFailureCategoryEvaluation ||
		seen.QueryFailure.Code != "UNEXPLAINED_OUTCOME" {
		t.Fatalf("failure facts %+v, want the evaluation's UNEXPLAINED_OUTCOME", seen.QueryFailure)
	}
}

// The evaluation line counts a Level that repeats a stored guard's reason in
// a cell of its own, apart from the same reason met this round; that cell is
// what level_outcome_total{origin="carried"} counts.
func TestALevelOutcomeCellSeparatesACarriedReason(t *testing.T) {
	plan := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "306"}
	unknown := func(level uint32, carried bool) execution.LevelOutcome {
		return execution.LevelOutcome{Plan: plan, LevelID: level, Outcome: execution.LevelOutcomeUnknown,
			ReasonCode: contract.ReasonQueryTimeout, ReasonCarried: carried}
	}
	facts := levelOutcomeFacts(execution.DuePlan{Identity: plan}, execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{{
		Plan: plan, LevelOutcomes: []execution.LevelOutcome{unknown(1, false), unknown(2, true), unknown(3, true)}}}})
	want := []observability.LevelOutcomeFact{
		{Outcome: "UNKNOWN", Reason: contract.ReasonQueryTimeout, Count: 1},
		{Outcome: "UNKNOWN", Reason: contract.ReasonQueryTimeout, Carried: true, Count: 2},
	}
	if len(facts) != len(want) || facts[0] != want[0] || facts[1] != want[1] {
		t.Fatalf("level outcome facts %+v, want %+v", facts, want)
	}
}

// A whole query under a whole evaluation is the routine line it always was:
// counted, not written, with nothing carried.
func TestAWholeQueryUnderAWholeEvaluationIsRoutine(t *testing.T) {
	seen, line := queryLine(t, wholeAnswer(), execution.EvaluationResult{Result: observability.ResultSuccess,
		ReasonCode: observability.ReasonNone})
	if seen.Result != observability.ResultSuccess || seen.CarriedReason != nil || line != nil {
		t.Fatalf("routine query: result %s carried %+v line %v", seen.Result, seen.CarriedReason, line)
	}
}
