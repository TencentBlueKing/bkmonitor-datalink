// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// Enrichment 构造具有两种等级隔离输出的完成结果，供后端契约使用。
func Enrichment(event domain.Event) domain.EventEnrichment {
	at := event.CreateAt.Add(time.Second)
	result := domain.EventEnrichment{EnrichStatus: domain.EnrichStatusSucceeded, EnrichedAt: &at, EnrichConfigDigest: "config-v1"}
	for _, evaluation := range event.Evaluations {
		patch, _ := domain.NewEnrichPatch("$.title", "enriched "+evaluation.Severity)
		raw, _ := json.Marshal([]map[string]any{{"fields": map[string]any{"status": "succeeded", "patches": []domain.EnrichPatch{patch}}}})
		result.Enrich.Evaluations = append(result.Enrich.Evaluations, domain.EvaluationEnrich{Severity: evaluation.Severity, Status: domain.EnrichStatusSucceeded, Data: domain.JSONObject{"processors": raw}})
	}
	return result
}

func runEventEnrichmentContract(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("suppressed event without alert remains queryable", func(t *testing.T) {
		repo := factory(t)
		ctx := context.Background()
		event := Event("tenant-suppressed", "event-suppressed", "fp-suppressed", "warning")
		created, err := repo.CreateEvent(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		enriched, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, created.Version, Enrichment(event))
		if err != nil {
			t.Fatal(err)
		}
		evaluation := store.EvaluationResult{Severity: "warning", Action: domain.EventActionTriggered, State: domain.EventProcessStateSuppressed, Outcome: "event_suppressed", ReasonCode: "clip_below_threshold"}
		plan := &store.EventPlan{UpgradePolicy: "close_and_create", State: domain.EventProcessStateSuppressed, Outcome: evaluation.Outcome, ReasonCode: evaluation.ReasonCode, Evaluations: []store.EvaluationResult{evaluation}}
		planned, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, enriched.Version, store.EventResult{State: domain.EventProcessStateUnprocessed, Plan: plan})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, planned.Version, Enrichment(event)); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("enrichment changed after plan: %v", err)
		}
		_, err = repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, planned.Version, store.EventResult{State: plan.State, Outcome: plan.Outcome, ReasonCode: plan.ReasonCode, Evaluations: plan.Evaluations, ProcessedAt: event.CreateAt.Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		found, err := repo.QueryAlertByEvent(ctx, event.BKTenantID, event.EventID)
		if err != nil {
			t.Fatal(err)
		}
		if found.Event.Processing.State != domain.EventProcessStateSuppressed || len(found.Alerts) != 0 || found.Event.Event.EnrichStatus != domain.EnrichStatusSucceeded {
			t.Fatalf("suppressed event lost: %+v", found)
		}
		if err := found.Event.Validate(); err != nil {
			t.Fatal(err)
		}
		again, err := repo.CreateEvent(ctx, event)
		if err != nil || again.Created || again.Processing.State != domain.EventProcessStateSuppressed {
			t.Fatalf("suppressed redelivery: %v", err)
		}
	})

	t.Run("enrichment freezes every evaluation without replacing facts", func(t *testing.T) {
		ctx := context.Background()
		repo := factory(t)
		event := Event("tenant-enrich", "event-enrich", "fp-enrich", "warning")
		event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionResolved})
		created, err := repo.CreateEvent(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		candidate := Enrichment(created.Event)
		enriched, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, created.Version, candidate)
		if err != nil {
			t.Fatal(err)
		}
		if enriched.Processing.State != domain.EventProcessStateUnprocessed || enriched.Event.Title != event.Title || len(enriched.Event.RelatedAlertIDs) != 0 {
			t.Fatalf("enrichment changed facts or processing: %+v", enriched)
		}
		source := enriched.Event.Clone()
		source.EventEnrichment = created.Event.EventEnrichment
		if !reflect.DeepEqual(source, created.Event) {
			t.Fatal("enrichment changed source facts")
		}
		// 调用方输入和返回快照均不得共享存储持有的 JSON 内存。
		candidate.Enrich.Evaluations[0].Data["processors"][0] = '!'
		enriched.Event.Enrich.Evaluations[0].Data["processors"][0] = '!'
		saved, err := repo.GetEvent(ctx, event.BKTenantID, event.EventID)
		if err != nil {
			t.Fatal(err)
		}
		if err := saved.Event.Validate(); err != nil {
			t.Fatal(err)
		}
		if _, ok := saved.Event.ForSeverity("critical"); !ok {
			t.Fatal("missing critical result")
		}
		duplicate, err := repo.CreateEvent(ctx, event)
		if err != nil || duplicate.Created || !reflect.DeepEqual(duplicate.Event, saved.Event) {
			t.Fatalf("redelivery erased enrichment: %v", err)
		}
		changed := event.Clone()
		changed.Title = "different"
		if _, err := repo.CreateEvent(ctx, changed); !errors.Is(err, store.ErrIdentityConflict) {
			t.Fatalf("changed facts: %v", err)
		}
		if _, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, created.Version, Enrichment(created.Event)); !errors.Is(err, store.ErrVersionConflict) {
			t.Fatalf("stale CAS: %v", err)
		}
		changedResult := Enrichment(created.Event)
		changedResult.EnrichConfigDigest = "config-v2"
		if _, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, saved.Version, changedResult); !errors.Is(err, store.ErrInvalidArgument) {
			t.Fatalf("overwrote frozen result: %v", err)
		}
		same, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, saved.Version, Enrichment(created.Event))
		if err != nil || !reflect.DeepEqual(same.Event, saved.Event) {
			t.Fatalf("idempotent result: %v", err)
		}
		if _, err := repo.CompareAndSetEventEnrichment(ctx, "other-tenant", event.EventID, same.Version, Enrichment(created.Event)); !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrVersionConflict) {
			t.Fatalf("tenant isolation: %v", err)
		}
		processed, err := repo.CompareAndSetEventResult(ctx, event.BKTenantID, event.EventID, same.Version, store.EventResult{State: domain.EventProcessStateAccepted, RelatedAlertIDs: []string{"alert-enrich"}, Outcome: "alert_created", ProcessedAt: event.CreateAt.Add(time.Minute)})
		if err != nil || !reflect.DeepEqual(processed.Event.EventEnrichment, saved.Event.EventEnrichment) {
			t.Fatalf("result CAS discarded enrichment: %v", err)
		}
		if _, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, processed.Version, Enrichment(created.Event)); !errors.Is(err, store.ErrInvalidTransition) {
			t.Fatalf("enrich after processing: %v", err)
		}
	})
	t.Run("enrichment rejects partial results and races", func(t *testing.T) {
		repo := factory(t)
		ctx := context.Background()
		event := Event("tenant-enrich", "event-race", "fp-race", "warning")
		created, err := repo.CreateEvent(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		invalid := Enrichment(created.Event)
		invalid.Enrich.Evaluations = nil
		if _, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, created.Version, invalid); !errors.Is(err, store.ErrInvalidArgument) {
			t.Fatalf("partial result: %v", err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := repo.CompareAndSetEventEnrichment(canceled, event.BKTenantID, event.EventID, created.Version, Enrichment(event)); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation: %v", err)
		}
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for range 2 {
			wg.Go(func() {
				_, err := repo.CompareAndSetEventEnrichment(ctx, event.BKTenantID, event.EventID, created.Version, Enrichment(event))
				results <- err
			})
		}
		wg.Wait()
		close(results)
		success, conflict := 0, 0
		for err := range results {
			if err == nil {
				success++
			} else if errors.Is(err, store.ErrVersionConflict) {
				conflict++
			} else {
				t.Fatal(err)
			}
		}
		if success != 1 || conflict != 1 {
			t.Fatalf("CAS winners=%d conflicts=%d", success, conflict)
		}
	})
}
