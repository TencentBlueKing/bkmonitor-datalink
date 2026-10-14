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
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/description"
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
			if stored.Processing.State != domain.EventProcessStateUnprocessed || stored.Event.EnrichStatus == domain.EnrichStatusPending {
				t.Fatal("saved failed plan")
			}
			id, _ := processor.idGenerator.Generate(event)
			if _, err := repo.GetAlert(context.Background(), event.BKTenantID, id); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("alert error %v", err)
			}
		})
	}
}

func TestMissingPublicationFallsBackToOriginalContentAndFreezesPlan(t *testing.T) {
	repo := memory.New()
	processor := newTestProcessor(t, repo, NoopFinalHook{})
	var logs bytes.Buffer
	processor.logger = slog.New(slog.NewJSONHandler(&logs, nil))
	processor.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
		return "", fmt.Errorf("lookup: %w", &description.Error{Code: "publication_missing"})
	})
	event := testEvent("missing-publication", "warning")
	event.Content = "original detection text"
	created, err := repo.CreateEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	enriched, err := processor.enrichEvent(t.Context(), created.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := processor.preparePlan(t.Context(), enriched.Event, nil)
	if err != nil {
		t.Fatal(err)
	}
	if enriched.Event.Content != event.Content {
		t.Fatal("fallback changed source text")
	}
	if !strings.Contains(logs.String(), "source_fallback") || !strings.Contains(logs.String(), event.EventID) || strings.Contains(logs.String(), event.Content) {
		t.Fatalf("fallback diagnostic lacks identity or exposes content: %s", logs.String())
	}
	result, err := processor.executePlan(t.Context(), enriched, plan)
	if err != nil {
		t.Fatal(err)
	}
	alert, err := repo.GetAlert(t.Context(), event.BKTenantID, result.Event.RelatedAlertIDs[0])
	if err != nil || alert.Alert.Content != event.Content {
		t.Fatalf("fallback content %q error %v", alert.Alert.Content, err)
	}
}

func TestContentFallbackDoesNotHideOtherFailuresOrCancellation(t *testing.T) {
	for _, code := range []string{"publication_missing_or_ambiguous", "publication_version_mismatch", "publication_runtime_invalid", "template_invalid", "opening_identity_invalid", "content_route_identity", "configuration_identity_mismatch", "detection_evidence_missing", "process_evidence_missing"} {
		t.Run(code, func(t *testing.T) {
			processor := newTestProcessor(t, memory.New(), NoopFinalHook{})
			processor.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
				return "", &description.Error{Code: code}
			})
			event := testEvent("failed", "warning")
			event.Content = "original detection text"
			if _, err := processor.buildNewAlertContent(t.Context(), event, domain.EventEvaluation{}, domain.Alert{}); err == nil {
				t.Fatal("unrelated content failure hidden")
			}
		})
	}
	processor := newTestProcessor(t, memory.New(), NoopFinalHook{})
	processor.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
		return "", &description.Error{Code: "publication_missing"}
	})
	event := testEvent("missing", "warning")
	event.Content = "  "
	if _, err := processor.buildNewAlertContent(t.Context(), event, domain.EventEvaluation{}, domain.Alert{}); err == nil {
		t.Fatal("empty fallback content accepted")
	}
	event.Content = "source"
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := processor.buildNewAlertContent(ctx, event, domain.EventEvaluation{}, domain.Alert{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("fallback ignored cancellation: %v", err)
	}
}

func TestNoDataContentFailuresFallBackToSourceAndKeepCreatedContent(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"nodata_identity_invalid", "nodata_configuration_invalid", "nodata_evidence_missing", "nodata_evidence_invalid"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			repo := memory.New()
			processor := newTestProcessor(t, repo, NoopFinalHook{})
			var logs bytes.Buffer
			processor.logger = slog.New(slog.NewJSONHandler(&logs, nil))
			processor.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
				return "", fmt.Errorf("resolve no-data facts: %w", &description.Error{Code: code})
			})
			event := testEvent("fallback-"+code, "warning")
			event.Content = "  original no-data detection text  "
			created, err := repo.CreateEvent(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			enriched, err := processor.enrichEvent(t.Context(), created.StoredEvent)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := processor.preparePlan(t.Context(), enriched.Event, nil)
			if err != nil {
				t.Fatal(err)
			}
			saved, err := processor.executePlan(t.Context(), enriched, plan)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(logs.String(), `"reason_code":"`+code+`"`) || !strings.Contains(logs.String(), "source_fallback") || strings.Contains(logs.String(), strings.TrimSpace(event.Content)) {
				t.Fatal("fallback diagnostic omitted reason or exposed source content")
			}
			// 告警已经创建、Event 已完成后，重投不能重新生成其创建文案。
			restarted := newTestProcessor(t, repo, NoopFinalHook{})
			restarted.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
				t.Fatal("completed fallback Event rebuilt content")
				return "", nil
			})
			result, err := restarted.ProcessEvent(t.Context(), saved)
			if err != nil || len(result.AlertIDs) != 1 {
				t.Fatalf("result=%+v error=%v", result, err)
			}
			alert, err := repo.GetAlert(t.Context(), event.BKTenantID, result.AlertIDs[0])
			if err != nil || alert.Alert.Content != event.Content {
				t.Fatalf("content=%q error=%v", alert.Alert.Content, err)
			}
			if _, err := restarted.ProcessEvent(t.Context(), saved); err != nil {
				t.Fatal(err)
			}
			kept := mustGetStoredEvent(t, repo, event)
			if kept.Event.Content != event.Content || kept.Processing.State != domain.EventProcessStateAccepted {
				t.Fatal("fallback changed source text or failed to complete Event")
			}
		})
	}
}

func TestNoDataContentFallbackRejectsEmptyOversizedAndCanceledSource(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"nodata_identity_invalid", "nodata_configuration_invalid", "nodata_evidence_missing", "nodata_evidence_invalid"} {
		t.Run(code, func(t *testing.T) {
			processor := newTestProcessor(t, memory.New(), NoopFinalHook{})
			processor.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
				return "", &description.Error{Code: code}
			})
			event := testEvent("empty-"+code, "warning")
			for _, text := range []string{"", " \t\n", strings.Repeat("x", (1<<20)+1)} {
				event.Content = text
				if _, err := processor.buildNewAlertContent(t.Context(), event, domain.EventEvaluation{}, domain.Alert{}); err == nil {
					t.Fatal("invalid fallback source accepted")
				}
			}
			event.Content = "original source text"
			ctx, cancel := context.WithCancel(t.Context())
			processor.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
				cancel()
				return "", &description.Error{Code: code}
			})
			if _, err := processor.buildNewAlertContent(ctx, event, domain.EventEvaluation{}, domain.Alert{}); !errors.Is(err, context.Canceled) {
				t.Fatalf("fallback ignored cancellation: %v", err)
			}
		})
	}
}
