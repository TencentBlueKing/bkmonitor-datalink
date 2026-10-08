// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/view"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type recordingEventEnricher struct {
	events    []string
	failFirst bool
}

func (e *recordingEventEnricher) Enrich(ctx context.Context, input enrich.Input) (enrich.Result, error) {
	e.events = append(e.events, input.Event.EventID)
	if e.failFirst && len(e.events) == 1 {
		return enrich.Result{}, errors.New("dependency failed")
	}
	result := enrich.Result{Status: domain.EnrichStatusSucceeded, ConfigDigest: "configured-v1"}
	for _, evaluation := range input.Event.Evaluations {
		patch, _ := domain.NewEnrichPatch("$.title", input.Event.EventID+":"+evaluation.Severity)
		raw, _ := json.Marshal([]map[string]any{{"fields": map[string]any{"status": "succeeded", "patches": []domain.EnrichPatch{patch}}}})
		result.Data.Evaluations = append(result.Data.Evaluations, domain.EvaluationEnrich{Severity: evaluation.Severity, Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{"processors": raw}})
	}
	return result, ctx.Err()
}

func TestEveryEventEnrichedAndOpeningSnapshotFrozen(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "opening succeeded", true: "opening failed"}[fail], func(t *testing.T) {
			repo := memory.New()
			p := newTestProcessor(t, repo, &recordingHook{})
			engine := &recordingEventEnricher{failFirst: fail}
			p.enricher = engine
			opening := testEvent("opening", "critical")
			opening.Evaluations = append(opening.Evaluations, domain.EventEvaluation{Severity: "warning", Action: domain.EventActionTriggered})
			first := persistAndProcess(t, repo, p, opening)
			before, err := repo.GetAlert(t.Context(), opening.BKTenantID, first.AlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			if !fail {
				effective, err := view.EnrichedAlert(before.Alert)
				if err != nil || effective.Title != "opening:critical" {
					t.Fatalf("selected opening result: %+v %v", effective, err)
				}
			}
			for _, id := range []string{"duplicate", "lower", "terminal"} {
				event := testEvent(id, "critical")
				if id == "lower" {
					event.Evaluations[0].Severity = "warning"
				}
				if id == "terminal" {
					event.Evaluations[0].Action = domain.EventActionResolved
				}
				persistAndProcess(t, repo, p, event)
				stored, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
				if err != nil || stored.Event.EnrichStatus != domain.EnrichStatusSucceeded {
					t.Fatalf("event %s result missing: %v", id, err)
				}
				if _, err := p.ProcessEvent(t.Context(), stored); err != nil {
					t.Fatal(err)
				}
			}
			after, err := repo.GetAlert(t.Context(), opening.BKTenantID, first.AlertIDs[0])
			if err != nil {
				t.Fatal(err)
			}
			if len(engine.events) != 4 || after.Alert.EnrichStatus != before.Alert.EnrichStatus || !reflect.DeepEqual(after.Alert.Enrich, before.Alert.Enrich) || after.Alert.TriggerEventID != "opening" {
				t.Fatal("later events refreshed opening snapshot or redelivery repeated enrichment")
			}
		})
	}
}

type failPlanOnce struct {
	store.Repository
	fail bool
}

func (r *failPlanOnce) CompareAndSetEventResult(ctx context.Context, tenant, id string, version store.VersionToken, result store.EventResult) (store.StoredEvent, error) {
	if r.fail {
		r.fail = false
		return store.StoredEvent{}, errors.New("plan write unavailable")
	}
	return r.Repository.CompareAndSetEventResult(ctx, tenant, id, version, result)
}

func TestEventEnrichmentSurvivesPlanFailure(t *testing.T) {
	repo := &failPlanOnce{Repository: memory.New(), fail: true}
	p := newTestProcessor(t, repo, &recordingHook{})
	engine := &recordingEventEnricher{}
	p.enricher = engine
	event := testEvent("retry", "warning")
	created, err := repo.CreateEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ProcessEvent(t.Context(), created.StoredEvent); err == nil {
		t.Fatal("expected plan failure")
	}
	pending, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Event.EnrichStatus != domain.EnrichStatusSucceeded || pending.Processing.Plan != nil {
		t.Fatal("enrich was not frozen before plan")
	}
	if _, err := p.ProcessEvent(t.Context(), pending); err != nil {
		t.Fatal(err)
	}
	if len(engine.events) != 1 {
		t.Fatal("plan retry repeated enrichment")
	}
}
