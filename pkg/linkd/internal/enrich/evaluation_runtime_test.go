// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich/custom"
	"linkd/internal/jsonpath"
)

func runtimeAssignment(t *testing.T, path string, value any) domain.EnrichAssignment {
	t.Helper()
	target, err := jsonpath.ParseTarget(path)
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := domain.NewEnrichAssignment(target, value)
	if err != nil {
		t.Fatal(err)
	}
	return assignment
}

func TestEvaluationRuntimeSharesOneObjectAndOriginalAcrossGrades(t *testing.T) {
	var config map[string]any
	if err := json.Unmarshal([]byte(`{"rules":[{"id":"title","operations":[{"id":"set","type":"assign","assignments":[{"target":"$.title","value":{"template":"${grade}:${original}","variables":{"grade":{"jsonpath":"$.evaluation.severity"},"original":{"jsonpath":"$.original.title"}}}}]}]}]}`), &config); err != nil {
		t.Fatal(err)
	}
	program, err := custom.Compile("fields", config)
	if err != nil {
		t.Fatal(err)
	}
	event := testAlert()
	event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionTriggered})
	states := map[string]*domain.EnrichEvaluation{}
	var original *domain.Event
	first := &testProcessor{name: "fields", fn: func(ctx context.Context, scope *Scope) (ProcessorResult, error) {
		if original == nil {
			original = scope.original
		} else if original != scope.original {
			t.Fatal("copied original Event between grades")
		}
		result, err := scope.ExecuteRules(ctx, program)
		states[scope.Evaluation().Severity] = scope.state
		return result, err
	}}
	last := &testProcessor{name: "verify", fn: func(_ context.Context, scope *Scope) (ProcessorResult, error) {
		state, err := scope.EvaluationState()
		if err != nil {
			return ProcessorResult{}, err
		}
		severity := scope.Evaluation().Severity
		if state != states[severity] || state.Document()["title"] != severity+":"+event.Title || state.OriginalDocument()["title"] != event.Title {
			t.Fatal("lost shared current/original view")
		}
		return ProcessorResult{Status: domain.EnrichStatusSucceeded, RuntimePatches: []domain.EnrichAssignment{}}, nil
	}}
	chain, err := NewChain([]Processor{first, last}, Sources{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	for severity, state := range states {
		if result.Evaluations[severity] != state {
			t.Fatal("replaced evaluation at result boundary")
		}
		if err := state.Apply([]domain.EnrichAssignment{runtimeAssignment(t, "$.title", "late")}); err == nil {
			t.Fatal("result was not frozen")
		}
	}
	if event.Title != "CPU high" {
		t.Fatal("changed caller Event")
	}
	saved, err := event.WithEnrichment(domain.EventEnrichment{EnrichStatus: result.Status, Enrich: result.Data, EnrichedAt: &event.CreateAt, EnrichConfigDigest: "test"})
	if err != nil {
		t.Fatal(err)
	}
	saved = saved.WithEnrichEvaluations(result.Evaluations)
	for severity, state := range states {
		got, err := saved.EnrichEvaluationView(severity, func() (*domain.EnrichEvaluation, error) {
			t.Fatal("replayed freshly persisted payload")
			return nil, nil
		})
		if err != nil || got != state {
			t.Fatal("persisted result lost its working object")
		}
	}
}

func TestEvaluationRuntimeRollsBackRejectedProcessor(t *testing.T) {
	for _, mode := range []string{"panic", "error", "invalid", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var state *domain.EnrichEvaluation
			broken := &testProcessor{name: "broken", fn: func(_ context.Context, scope *Scope) (ProcessorResult, error) {
				var err error
				state, err = scope.EvaluationState()
				if err != nil {
					return ProcessorResult{}, err
				}
				patches := []domain.EnrichAssignment{runtimeAssignment(t, "$.title", "bad"), runtimeAssignment(t, "$.extra_data.created.value", true)}
				if err := state.Apply(patches); err != nil {
					return ProcessorResult{}, err
				}
				switch mode {
				case "panic":
					panic("dependency failed")
				case "error":
					return ProcessorResult{}, errors.New("dependency failed")
				case "cancel":
					cancel()
				}
				return ProcessorResult{Status: "invalid", RuntimePatches: patches}, nil
			}}
			last := &testProcessor{name: "verify", fn: func(_ context.Context, scope *Scope) (ProcessorResult, error) {
				if scope.state != state || state.Document()["title"] != "CPU high" {
					t.Fatal("failed change survived")
				}
				if _, exists := state.Document()["extra_data"].(map[string]any)["created"]; exists {
					t.Fatal("new parent survived")
				}
				return ProcessorResult{Status: domain.EnrichStatusSucceeded, RuntimePatches: []domain.EnrichAssignment{}}, nil
			}}
			chain, err := NewChain([]Processor{broken, last}, Sources{})
			if err != nil {
				t.Fatal(err)
			}
			_, err = chain.Enrich(ctx, Input{Event: testAlert()})
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) || state.Document()["title"] != "CPU high" {
					t.Fatalf("cancel did not rollback: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
