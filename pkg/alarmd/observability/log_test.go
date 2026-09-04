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
	"log/slog"
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
