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
	"errors"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type contentBuilderFunc func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error)

func (f contentBuilderFunc) BuildContent(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (string, error) {
	return f(ctx, event, evaluation, opening)
}

func TestGeneratedContentPersistsAndRemainsImmutable(t *testing.T) {
	for _, policy := range []string{"close_and_create", "update_current"} {
		t.Run(policy, func(t *testing.T) {
			repo := memory.New()
			processor := newTestProcessor(t, repo, NoopFinalHook{})
			processor.upgradePolicy = policy
			calls := 0
			processor.contentBuilder = contentBuilderFunc(func(_ context.Context, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (string, error) {
				calls++
				if event.BKTenantID != opening.BKTenantID || evaluation.Severity != opening.Severity {
					t.Fatal("builder received mismatched identity")
				}
				event.Labels["mutated"] = domain.NewStringScalar("bad")
				opening.Dimensions["host"] = domain.NewStringScalar("bad")
				return "generated:" + event.EventID, nil
			})
			processor.enricher = stubEnricher{fn: func(input enrich.Input) (enrich.Result, error) {
				if strings.HasPrefix(input.Event.Content, "generated:") {
					t.Fatal("Event enrichment received generated Alert content")
				}
				return enrich.Result{}, errors.New("ordinary enrich may degrade")
			}}
			opening := testEvent("opening", "warning")
			opening.Content = "source text"
			created := persistAndProcess(t, repo, processor, opening)
			persistAndProcess(t, repo, processor, testEvent("repeat", "warning"))
			upgrade := persistAndProcess(t, repo, processor, testEvent("upgrade", "critical"))
			old, err := repo.GetAlert(context.Background(), opening.BKTenantID, created.AlertIDs[0])
			if err != nil || old.Alert.Content != "generated:opening" || old.Alert.EnrichStatus != domain.EnrichStatusFailed {
				t.Fatalf("old content/status = %q/%s error %v", old.Alert.Content, old.Alert.EnrichStatus, err)
			}
			storedEvent := mustGetStoredEvent(t, repo, opening)
			if storedEvent.Event.Content != "source text" || len(storedEvent.Event.Labels) != 0 {
				t.Fatal("builder changed persisted Event")
			}
			wantCalls, wantContent := 1, "generated:opening"
			if policy == "close_and_create" {
				wantCalls, wantContent = 2, "generated:upgrade"
			}
			current, err := repo.GetAlert(context.Background(), opening.BKTenantID, upgrade.AlertIDs[len(upgrade.AlertIDs)-1])
			if err != nil || current.Alert.Content != wantContent || calls != wantCalls {
				t.Fatalf("current content %q calls %d error %v", current.Alert.Content, calls, err)
			}
			replacement := current.Alert.Clone()
			replacement.Content = "overwrite"
			if err := domain.ValidateAlertReplacement(current.Alert, replacement); err == nil {
				t.Fatal("domain accepted content overwrite")
			}
		})
	}
}

func TestSavedPlanFreezesGeneratedContentAcrossRestart(t *testing.T) {
	repo := memory.New()
	processor := newTestProcessor(t, repo, NoopFinalHook{})
	processor.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
		return "frozen", nil
	})
	event := testEvent("planned", "warning")
	created, err := repo.CreateEvent(context.Background(), event)
	if err != nil {
		t.Fatal(err)
	}
	enriched, err := processor.enrichEvent(context.Background(), created.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := processor.preparePlan(context.Background(), enriched.Event, nil)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := processor.writeEventResult(context.Background(), enriched, store.EventResult{State: domain.EventProcessStateUnprocessed, Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	restarted := newTestProcessor(t, repo, NoopFinalHook{})
	restarted.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
		t.Fatal("saved plan called builder again")
		return "", errors.New("configuration changed")
	})
	result, err := restarted.ProcessEvent(context.Background(), saved)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.GetAlert(context.Background(), event.BKTenantID, result.AlertIDs[0])
	if err != nil || stored.Alert.Content != "frozen" {
		t.Fatalf("content %q error %v", stored.Alert.Content, err)
	}
}

func TestContentFailureDoesNotSavePlanOrCreateAlert(t *testing.T) {
	for _, failure := range []string{"error", "panic", "invalid content", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			repo := memory.New()
			processor := newTestProcessor(t, repo, NoopFinalHook{})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			processor.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
				switch failure {
				case "panic":
					panic("sensitive message must not be logged")
				case "invalid content":
					return strings.Repeat("x", (1<<20)+1), nil
				case "cancel":
					cancel()
					return "generated", nil
				default:
					return "", errors.New("unavailable")
				}
			})
			event := testEvent("content-failure", "warning")
			created, err := repo.CreateEvent(ctx, event)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := processor.ProcessEvent(ctx, created.StoredEvent); err == nil {
				t.Fatal("accepted failed content")
			}
			stored := mustGetStoredEvent(t, repo, event)
			if stored.Processing.Plan != nil || stored.Processing.State != domain.EventProcessStateUnprocessed || stored.Event.EnrichStatus == domain.EnrichStatusPending {
				t.Fatal("saved failed plan")
			}
			id, _ := processor.idGenerator.Generate(event)
			if _, err := repo.GetAlert(context.Background(), event.BKTenantID, id); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("alert error %v", err)
			}
		})
	}
}
