// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"testing"
	"time"
)

func TestLoggerWritesFixedEventFields(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger := New(ComponentTrigger, &output)
	logger.Info(StageDecisionACK, ResultBrokerACK, 2, 1500*time.Millisecond, slog.String("batch_id", "batch-1"))

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode log: %v", err)
	}
	want := map[string]any{
		"component":   ComponentTrigger,
		"stage":       StageDecisionACK,
		"result":      ResultBrokerACK,
		"records":     float64(2),
		"duration_ms": float64(1500),
		"batch_id":    "batch-1",
	}
	for field, value := range want {
		if event[field] != value {
			t.Fatalf("event[%q] = %#v, want %#v; event=%#v", field, event[field], value, event)
		}
	}
}

func TestDiscardLoggerAcceptsEvents(t *testing.T) {
	t.Parallel()

	Discard(ComponentTrigger).Error(StageFatal, ResultFailed, 0, 0)
}

func TestLoggingObserverWritesExactAlgorithmReasonAndProvenanceWithoutPayload(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentEvaluation,
		Stage:     StageEvaluationCompleted,
		Result:    ResultSuccess,
		AlgorithmEvaluations: []AlgorithmEvaluationFact{{
			SourceAlgorithmFamily: AlgorithmFamilyOsRestart,
			DetectorKind:          AlgorithmDetectorKindOsRestart,
			Result:                AlgorithmEvaluationResultUnavailable,
			ReasonCode:            ReasonCode("QUERY_PARTIAL"),
			Provenance:            AlgorithmProvenance{LevelID: 2},
		}},
		AlgorithmInputs: []AlgorithmInputFact{{
			SourceAlgorithmFamily: AlgorithmFamilyOsRestart,
			DetectorKind:          AlgorithmDetectorKindOsRestart,
			InputName:             AlgorithmInputNameHistory,
			DependencyPoint:       AlgorithmDependencyPointTenMinute,
			Result:                AlgorithmInputResultPartial,
			ReasonCode:            ReasonCode("QUERY_PARTIAL"),
			Provenance: AlgorithmProvenance{
				LevelID: 2, RequirementID: "uptime-history", QueryRef: "uptime-query",
				QueryRevision: "query-v1", SourceTime: 120, QueryStart: -1380, QueryEnd: 120,
			},
		}},
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode algorithm observation log: %v; log=%s", err, output.String())
	}
	evaluations, ok := event["algorithm_evaluations"].([]any)
	if !ok || len(evaluations) != 1 {
		t.Fatalf("algorithm_evaluations = %#v", event["algorithm_evaluations"])
	}
	evaluation := evaluations[0].(map[string]any)
	if evaluation["source_algorithm_family"] != "os_restart" || evaluation["detector_kind"] != "OsRestart" ||
		evaluation["result"] != "unavailable" || evaluation["reason_code"] != "QUERY_PARTIAL" {
		t.Fatalf("algorithm evaluation log = %#v", evaluation)
	}
	inputs, ok := event["algorithm_inputs"].([]any)
	if !ok || len(inputs) != 1 {
		t.Fatalf("algorithm_inputs = %#v", event["algorithm_inputs"])
	}
	input := inputs[0].(map[string]any)
	provenance := input["provenance"].(map[string]any)
	if input["input_name"] != "history" || input["dependency_point"] != "ten_minute" ||
		input["result"] != "partial" || provenance["requirement_id"] != "uptime-history" ||
		provenance["query_ref"] != "uptime-query" || provenance["source_time"] != float64(120) {
		t.Fatalf("algorithm input log = %#v", input)
	}
	if _, exists := event["payload"]; exists {
		t.Fatalf("raw payload leaked into algorithm observation log: %#v", event)
	}
}

func TestLoggingObserverWritesActionableSourceRefreshFacts(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentControlPlane,
		Stage:     StageSnapshotRefreshed,
		Result:    ResultSuccess,
		SourceRefresh: &SourceRefreshFacts{
			Status: SourceRefreshPublished, SnapshotRevision: "snapshot-new", PublicationEpoch: 7,
			CountsKnown: true, OldQueryGroups: 11, NewQueryGroups: 12,
			AddedQueryGroups: 2, RetiredQueryGroups: 1,
		},
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode source refresh observation log: %v; log=%s", err, output.String())
	}
	want := map[string]any{
		"source_refresh_status": "PUBLISHED",
		"snapshot_revision":     "snapshot-new",
		"publication_epoch":     float64(7),
		"old_query_groups":      float64(11),
		"new_query_groups":      float64(12),
		"added_query_groups":    float64(2),
		"retired_query_groups":  float64(1),
	}
	for field, value := range want {
		if event[field] != value {
			t.Fatalf("event[%q] = %#v, want %#v; event=%#v", field, event[field], value, event)
		}
	}
}

func TestLoggingObserverWritesBoundedActivationFailureReappearedSamples(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentControlPlane,
		Stage:     StageActivationFailed,
		Result:    ResultDegraded,
		ActivationFailure: &ActivationFailureFacts{
			Stage:               ActivationFailureStageReactivation,
			Class:               ActivationFailureClassNotDrained,
			DrainingQueryGroups: 10, CandidateQueryGroups: 364, ReappearedQueryGroups: 10,
			ReappearedQueryGroupSamples: []string{
				"query-group-00", "query-group-01", "query-group-02", "query-group-03", "query-group-04",
				"query-group-05", "query-group-06", "query-group-07", "query-group-08", "query-group-09",
			},
		},
		Err: errors.New("activation read failed"),
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode activation failure log: %v; log=%s", err, output.String())
	}
	for field, want := range map[string]any{
		"activation_failure_stage": "reactivation",
		"activation_failure_class": "not_drained",
		"draining_query_groups":    float64(10),
		"candidate_query_groups":   float64(364),
		"reappeared_query_groups":  float64(10),
	} {
		if event[field] != want {
			t.Fatalf("event[%q]=%#v, want %#v; event=%#v", field, event[field], want, event)
		}
	}
	wantSamples := []any{
		"query-group-00", "query-group-01", "query-group-02", "query-group-03",
		"query-group-04", "query-group-05", "query-group-06", "query-group-07",
	}
	if !reflect.DeepEqual(event["reappeared_query_group_samples"], wantSamples) ||
		event["reappeared_query_group_samples_truncated"] != true {
		t.Fatalf("reappeared samples were not bounded: %#v", event)
	}
	if event["error"] != "activation read failed" || event["error_message"] != nil {
		t.Fatalf("sanitized error text missing from activation failure log: %#v", event)
	}
	for _, field := range []string{"query_group_key", "strategy_id", "record_id"} {
		if event[field] != nil {
			t.Fatalf("activation identity leaked into %q: %#v", field, event)
		}
	}
}

func TestLoggingObserverKeepsPendingCandidateWithoutInventingPublication(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentControlPlane, Stage: StageSnapshotRefreshed, Result: ResultSuccess,
		SourceRefresh: &SourceRefreshFacts{
			Status: SourceRefreshPending, ObservationID: "observation-candidate",
		},
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode pending source refresh log: %v; log=%s", err, output.String())
	}
	if event["source_refresh_status"] != "PENDING_CONFIRMATION" ||
		event["source_observation_id"] != "observation-candidate" ||
		event["snapshot_revision"] != nil || event["publication_epoch"] != nil {
		t.Fatalf("pending source refresh log invented publication: %#v", event)
	}
}

func TestSeriesTraceDoesNotExpandEvaluationLogBudget(t *testing.T) {
	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	observer := NewLoggingObserver(New("alarmd", &output), policy)
	for _, series := range []string{"series-a", "series-b"} {
		observer.Observe(context.Background(), Observation{
			Component: ComponentEvaluation, Stage: StageEvaluationCompleted, Result: ResultSuccess,
			Operation: OperationNormal, ReasonCode: ReasonNone,
			Trace: TraceFields{StrategyID: "7", DimensionIdentityDigest: series},
		})
	}
	lines := bytes.Split(bytes.TrimSpace(output.Bytes()), []byte("\n"))
	if len(lines) != 1 {
		t.Fatalf("series identity bypassed shared log budget: %s", output.String())
	}
	var event map[string]any
	if err := json.Unmarshal(lines[0], &event); err != nil {
		t.Fatal(err)
	}
	if event["dimension_identity_digest"] != "series-a" {
		t.Fatalf("series trace missing: %#v", event)
	}
	if _, exists := event["payload"]; exists {
		t.Fatalf("unexpected payload: %#v", event)
	}
}

// The activation_failed line's reason_code is the classification itself,
// stage/class, verbatim -- the same word the fleet page groups the failure
// on. It carried contract_retryable, which the normaliser folds to _other, so
// the field people grep said nothing while the two beside it said
// schedule_conflict. Every pair of the two closed lists survives.
func TestActivationFailureReasonSurvivesNormalisationAndIsLogged(t *testing.T) {
	t.Parallel()

	for _, stage := range AllActivationFailureStages() {
		for _, class := range AllActivationFailureClasses() {
			reason := ActivationFailureReason(stage, class)
			if got := NormalizeReason(reason, ResultDegraded); got != reason {
				t.Fatalf("NormalizeReason(%q) = %q, want it kept", reason, got)
			}
		}
	}
	if got := NormalizeReason("schedule_cutover/made_up", ResultDegraded); got != ReasonOther {
		t.Fatalf("a class outside the list normalised to %q, want _other", got)
	}

	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentControlPlane, Stage: StageActivationFailed, Result: ResultDegraded,
		ReasonCode: ActivationFailureReason(ActivationFailureStageScheduleCutover, ActivationFailureClassScheduleConflict),
		ActivationFailure: &ActivationFailureFacts{
			Stage: ActivationFailureStageScheduleCutover, Class: ActivationFailureClassScheduleConflict,
		},
		Err: errors.New("alarmd controlplane: schedule activation conflict"),
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode activation failure log: %v; log=%s", err, output.String())
	}
	if event["reason_code"] != "schedule_cutover/schedule_conflict" {
		t.Fatalf("reason_code = %#v, want schedule_cutover/schedule_conflict; event=%#v", event["reason_code"], event)
	}
}

// The short-period completion line carries which attempt completed. A lag past
// the deadline reads two ways -- dispatched late, or a retry after an earlier
// attempt failed -- and without the attempt number on the line the live tail
// past fifteen seconds could not be told one from the other.
func TestLoggingObserverWritesTheAttemptOnAShortPeriodCompletion(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentScheduler, Stage: StageSlotCompleted, Operation: OperationRetry, Result: ResultSuccess,
		ShortPeriodCompletion: &ShortPeriodCompletionFacts{Cohort: "10s", CompletionKind: "FULL_COMPLETED", LagSeconds: 17.5, AttemptNo: 2},
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode short period completion log: %v; log=%s", err, output.String())
	}
	completion, ok := event["short_period_completion"].(map[string]any)
	if !ok {
		t.Fatalf("no short_period_completion on the line: %#v", event)
	}
	if completion["attempt_no"] != float64(2) || completion["completion_kind"] != "FULL_COMPLETED" || completion["lag_seconds"] != 17.5 {
		t.Fatalf("short_period_completion = %#v, want attempt 2, FULL_COMPLETED, lag 17.5", completion)
	}
}

// The frozen-state renewal line carries its eight numbers. They reached the
// metric and not the line, so a reader of one Slot's log saw that a renewal
// happened and nothing of what it found; a script matching *due* on the line
// found due_plan_set_digest instead and read a false positive.
func TestLoggingObserverWritesTheFrozenStateRenewalNumbers(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	facts := &FrozenStateRenewalFacts{}
	facts.RecordCensus(12, 11, 7)
	facts.Record(3, 1, 0, 0)
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentState, Stage: StageFrozenStateRenewed, Operation: OperationNormal, Result: ResultSuccess,
		FrozenStateRenewal: facts,
	})

	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode frozen state renewal log: %v; log=%s", err, output.String())
	}
	renewal, ok := event["frozen_state_renewal"].(map[string]any)
	if !ok {
		t.Fatalf("no frozen_state_renewal on the line: %#v", event)
	}
	for field, want := range map[string]float64{"due": 12, "read": 11, "written": 7, "frozen": 4, "renewed": 3, "fresh": 1, "missing": 0, "failed": 0} {
		if renewal[field] != want {
			t.Fatalf("frozen_state_renewal[%q] = %v, want %v; line=%#v", field, renewal[field], want, renewal)
		}
	}
}

// A view_session line carries the stream's own word as its reason_code, so a
// count by reason reaches it: every discovery miss on a live deployment read
// reason_not_reported with the one word that said what happened -- NO_LEADER
// -- two levels down in the facts. An endpoint or a detail in the same slot
// is not a word and is not promoted; a degraded line with none of the words
// still says the site did not report one.
func TestTheViewSessionLineCarriesTheStreamsWordAsItsReasonCode(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		reason string
		result Result
		want   string
	}{
		{"NO_LEADER", ResultDegraded, "NO_LEADER"},
		{"DELTA_DIGEST_MISMATCH", ResultDegraded, "DELTA_DIGEST_MISMATCH"},
		{"NOT_LEADER", ResultDegraded, "NOT_LEADER"},
		{"10.0.0.1:9000", ResultDegraded, string(ReasonNotReported)},
		{"10.0.0.1:9000", ResultSuccess, string(ReasonNone)},
	} {
		var output bytes.Buffer
		limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 10})
		if err != nil {
			t.Fatal(err)
		}
		policy, err := NewBoundedLogPolicy(limiter)
		if err != nil {
			t.Fatal(err)
		}
		NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
			Component: ComponentOwnership, Stage: StageViewSession, Result: test.result,
			ReasonCode: ViewStreamReasonCode(test.reason),
			ViewStream: &ViewStreamFacts{Event: "discovery_missed", WorkerID: "w1", Reason: test.reason},
		})
		var event map[string]any
		if err := json.Unmarshal(output.Bytes(), &event); err != nil {
			t.Fatalf("decode view_session log for %q: %v; log=%s", test.reason, err, output.String())
		}
		if event["reason_code"] != test.want {
			t.Errorf("reason %q result %s: reason_code = %v, want %s (event %v)", test.reason, test.result, event["reason_code"], test.want, event)
		}
		facts, _ := event["view_stream"].(map[string]any)
		if facts == nil || facts["reason"] != test.reason {
			t.Errorf("reason %q: the facts no longer carry it as given: %v", test.reason, event["view_stream"])
		}
	}
	// Every word in the list is admitted by the normaliser as itself.
	for _, word := range ViewStreamReasons {
		if got := NormalizeReason(word, ResultDegraded); got != word {
			t.Errorf("NormalizeReason(%s) = %s, want the word itself", word, got)
		}
	}
}

// The completion line carries the completion the Slot reached as its own
// top-level key, one word for each completion the store knows, and no key at
// all when the Slot reached none. The Observation field was pinned by the
// executor's case; this one pins the line, because a word the line drops is
// absent in exactly the way an older build's silence is, and the two cannot
// be told apart by the reader.
func TestLoggingObserverWritesTheCompletionKindTheSlotReached(t *testing.T) {
	t.Parallel()

	render := func(kind string) map[string]any {
		var output bytes.Buffer
		limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 1})
		if err != nil {
			t.Fatal(err)
		}
		policy, err := NewBoundedLogPolicy(limiter)
		if err != nil {
			t.Fatal(err)
		}
		NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
			Component: ComponentScheduler, Stage: StageSlotCompleted, Operation: OperationRetry, Result: ResultSuccess,
			SlotCompletionKind: kind,
		})
		var event map[string]any
		if err := json.Unmarshal(output.Bytes(), &event); err != nil {
			t.Fatalf("decode completion log: %v; log=%s", err, output.String())
		}
		return event
	}
	for _, kind := range []string{
		"FULL_COMPLETED", "FULL_EMPTY_COMPLETED", "COMPLETED_WITH_PARTIAL_GAP", "COMPLETED_WITH_UNAVAILABLE",
		"COMPLETED_WITH_TERMINAL", "GAP_SKIPPED", "SNAPSHOT_UNAVAILABLE",
	} {
		if got := render(kind)["completion_kind"]; got != kind {
			t.Errorf("a completion of kind %s rendered completion_kind=%v; the key is absent for the reader exactly "+
				"as it is on a build that does not report it", kind, got)
		}
	}
	if got, present := render("")["completion_kind"]; present {
		t.Errorf("a Slot that reached no completion rendered completion_kind=%v; the key is for completions, "+
			"and a word here would be one the store never produced", got)
	}
}

// The split gate is on the rebalance line, zeros included. Every round the
// gate runs says how many ready workers it asked and how many of them do not
// declare the split contract; a clear fleet reads zero rather than reading
// like a build that has no gate. The list is beside the count because the
// question a rolling release asks is which replica, not how many.
func TestLoggingObserverWritesTheSplitGateOnTheRebalanceLine(t *testing.T) {
	t.Parallel()

	line := func(gate *ShardAwareFacts) map[string]any {
		t.Helper()
		var output bytes.Buffer
		limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 8})
		if err != nil {
			t.Fatal(err)
		}
		policy, err := NewBoundedLogPolicy(limiter)
		if err != nil {
			t.Fatal(err)
		}
		NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
			Component: ComponentOwnership, Stage: StageRebalancePlanned, Result: ResultSuccess,
			Rebalance: &RebalanceFacts{ReadyWorkers: 4, ShardAware: gate},
		})
		var event map[string]any
		if err := json.Unmarshal(output.Bytes(), &event); err != nil {
			t.Fatalf("decode rebalance observation log: %v; log=%s", err, output.String())
		}
		return event
	}

	clear := line(&ShardAwareFacts{Ready: 4})
	for field, value := range map[string]any{
		"shard_aware_ready":      float64(4),
		"shard_unaware_replicas": float64(0),
		"shard_splits_held":      float64(0),
	} {
		if clear[field] != value {
			t.Fatalf("a clear gate reads %q = %#v, want %#v; event=%#v", field, clear[field], value, clear)
		}
	}

	held := line(&ShardAwareFacts{Ready: 4, Unaware: []string{"worker-2", "worker-3"}, SplitsHeld: 1})
	if held["shard_unaware_replicas"] != float64(2) || held["shard_splits_held"] != float64(1) {
		t.Fatalf("a held gate reads %#v", held)
	}
	names, ok := held["shard_unaware"].([]any)
	if !ok || len(names) != 2 || names[0] != "worker-2" || names[1] != "worker-3" {
		t.Fatalf("the replicas are not named on the line: %#v", held["shard_unaware"])
	}

	// A round from a build with no gate says nothing about it, rather than
	// saying a fleet of nobody is clear.
	if absent := line(nil); absent["shard_aware_ready"] != nil || absent["shard_unaware_replicas"] != nil {
		t.Fatalf("a round with no gate carried gate fields: %#v", absent)
	}
}

// The byte-constraint round's counts are on the same line, zeros included:
// judged and unread together say whether the round could judge at all, and
// a round that moved nothing because it judged nothing read identically to
// one that found nothing to move. Both were attached to the round and
// rendered nowhere - the page had them, the line did not.
func TestLoggingObserverWritesTheByteConstraintCountsOnTheRebalanceLine(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	limiter, err := NewWindowLogLimiter(WindowLogLimiterConfig{Window: time.Hour, MaxEvents: 2})
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewBoundedLogPolicy(limiter)
	if err != nil {
		t.Fatal(err)
	}
	NewLoggingObserver(New("alarmd", &output), policy).Observe(context.Background(), Observation{
		Component: ComponentOwnership, Stage: StageRebalancePlanned, Result: ResultSuccess,
		Rebalance: &RebalanceFacts{ReadyWorkers: 4, Bytes: &ByteConstraintFacts{
			SharePercent: 80, Judged: 4, Unread: 2046, Unsettled: []string{"w1", "w2", "w3", "w4"},
			Overloaded: []string{"w1"}, PlannedMoves: 0,
		}},
	})
	var event map[string]any
	if err := json.Unmarshal(output.Bytes(), &event); err != nil {
		t.Fatalf("decode rebalance observation log: %v; log=%s", err, output.String())
	}
	for field, value := range map[string]any{
		"byte_constraint_judged":        float64(4),
		"byte_constraint_unread":        float64(2046),
		"byte_constraint_unsettled":     float64(4),
		"byte_constraint_planned_moves": float64(0),
	} {
		if event[field] != value {
			t.Fatalf("event[%q] = %#v, want %#v; event=%#v", field, event[field], value, event)
		}
	}
	overloaded, ok := event["byte_constraint_overloaded"].([]any)
	if !ok || len(overloaded) != 1 || overloaded[0] != "w1" {
		t.Fatalf("the overloaded replica is not named: %#v", event["byte_constraint_overloaded"])
	}
}

// The gate's list is copied and bounded like every other per-replica sample
// the round carries: the observation is read after the round moves on, and
// one enormous fleet must not make one enormous line.
func TestTheSplitGatesReplicaListIsCopiedAndBounded(t *testing.T) {
	t.Parallel()

	unaware := make([]string, MaxRebalanceOwnedSamples+3)
	for index := range unaware {
		unaware[index] = "worker"
	}
	facts := &RebalanceFacts{ReadyWorkers: len(unaware), ShardAware: &ShardAwareFacts{Ready: len(unaware), Unaware: unaware}}
	normalized := normalizeRebalanceFacts(facts)
	if normalized.ShardAware == nil || len(normalized.ShardAware.Unaware) != MaxRebalanceOwnedSamples {
		t.Fatalf("the list was not bounded: %+v", normalized.ShardAware)
	}
	if normalized.ShardAware.Ready != len(unaware) {
		t.Fatalf("the ready count was truncated with the list: %+v", normalized.ShardAware)
	}
	unaware[0] = "changed after the round"
	if normalized.ShardAware.Unaware[0] != "worker" {
		t.Fatal("the observation shares the round's slice")
	}
}
