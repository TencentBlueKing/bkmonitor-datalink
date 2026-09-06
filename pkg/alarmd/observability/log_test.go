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

	Discard(ComponentComparator).Error(StageFatal, ResultFailed, 0, 0)
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
		Err: errors.New("must-not-be-observed"),
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
	if event["error"] != nil || event["error_message"] != nil {
		t.Fatalf("raw error leaked into activation failure log: %#v", event)
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
