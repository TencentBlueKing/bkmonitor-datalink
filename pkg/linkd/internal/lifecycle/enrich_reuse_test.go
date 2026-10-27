// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type lifecycleReuseStep struct{ calls int }

func (*lifecycleReuseStep) Name() string { return "fields" }

func (*lifecycleReuseStep) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

func (s *lifecycleReuseStep) Process(_ context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	s.calls++
	patch, err := domain.NewEnrichPatch("$.title", scope.Facts().EventID+":"+scope.Evaluation().Severity)
	return enrich.ProcessorResult{Status: domain.EnrichStatusSucceeded, Patches: []domain.EnrichPatch{patch}}, err
}

type reuseQueryRepository struct {
	store.Repository
	activeReads, eventReads int
}

func (r *reuseQueryRepository) FindActiveAlert(ctx context.Context, key store.ActiveAlertKey) (store.StoredAlert, error) {
	r.activeReads++
	return r.Repository.FindActiveAlert(ctx, key)
}

func (r *reuseQueryRepository) GetEvent(ctx context.Context, tenant, id string) (store.StoredEvent, error) {
	r.eventReads++
	return r.Repository.GetEvent(ctx, tenant, id)
}

type reuseQueryCache struct {
	RecentAlertCache
	reads int
}

func (c *reuseQueryCache) GetCurrent(ctx context.Context, key store.ActiveAlertKey) (store.StoredAlert, bool, error) {
	c.reads++
	return c.RecentAlertCache.GetCurrent(ctx, key)
}

func TestActiveEnrichReuseDoesNotAddQueries(t *testing.T) {
	for _, useCache := range []bool{false, true} {
		t.Run(map[bool]string{false: "repository lookup", true: "recent alert cache"}[useCache], func(t *testing.T) {
			base := memory.New()
			repository := &reuseQueryRepository{Repository: base}
			cache := &reuseQueryCache{RecentAlertCache: NoopRecentAlertCache{}}
			if useCache {
				cache.RecentAlertCache = newMemoryRecentAlertCache()
			}
			processor := newTestProcessorWithCache(t, repository, cache, NoopFinalHook{})
			step := &lifecycleReuseStep{}
			chain, err := enrich.NewChain([]enrich.Processor{step}, enrich.Sources{}, enrich.WithReuseOnActive("fields"))
			if err != nil {
				t.Fatal(err)
			}
			processor.enricher = chain
			var openingID string
			for index, id := range []string{"opening", "repeat", "resolve", "new-lifecycle"} {
				event := testEvent(id, "warning")
				if id == "resolve" {
					event.Evaluations[0].Action = domain.EventActionResolved
				}
				result := persistAndProcess(t, repository, processor, event)
				if index == 0 {
					openingID = result.AlertIDs[0]
				}
				if index == 3 && result.AlertIDs[0] == openingID {
					t.Fatal("terminal lifecycle was reused")
				}
				stored, err := base.GetEvent(t.Context(), event.BKTenantID, event.EventID)
				if err != nil {
					t.Fatal(err)
				}
				row, _ := stored.Event.ForSeverity("warning")
				payload, err := enrich.DecodePayload(row.Data)
				if err != nil {
					t.Fatal(err)
				}
				reused := payload.Processors[0]["fields"].ReusedFromAlertID
				if index == 1 || index == 2 {
					if reused != openingID {
						t.Fatalf("event %s reused from %q, want %q", id, reused, openingID)
					}
				} else if reused != "" {
					t.Fatalf("event %s unexpectedly reused %s", id, reused)
				}
			}
			if step.calls != 2 || cache.reads != 4 || repository.eventReads != 0 {
				t.Fatalf("processor calls=%d cache reads=%d event reads=%d", step.calls, cache.reads, repository.eventReads)
			}
			wantActiveReads := 4
			if useCache {
				wantActiveReads = 1
			}
			if repository.activeReads != wantActiveReads {
				t.Fatalf("active reads=%d, want %d", repository.activeReads, wantActiveReads)
			}
			stored := mustGetStoredEvent(t, base, testEvent("repeat", "warning"))
			if _, err := processor.ProcessEvent(t.Context(), stored); err != nil {
				t.Fatal(err)
			}
			if cache.reads != 4 || repository.activeReads != wantActiveReads || step.calls != 2 {
				t.Fatal("completed event redelivery repeated lookup or enrichment")
			}
		})
	}
}

func TestActiveEnrichReuseRetainsOpeningSeverityThroughUpgrade(t *testing.T) {
	base := memory.New()
	processor := newTestProcessor(t, base, NoopFinalHook{})
	processor.upgradePolicy = "update_current"
	step := &lifecycleReuseStep{}
	chain, err := enrich.NewChain([]enrich.Processor{step}, enrich.Sources{}, enrich.WithReuseOnActive("fields"))
	if err != nil {
		t.Fatal(err)
	}
	processor.enricher = chain
	opening := testEvent("opening-warning", "warning")
	first := persistAndProcess(t, base, processor, opening)
	for _, event := range []domain.Event{testEvent("upgrade", "critical"), testEvent("critical-again", "critical"), testEvent("lower-warning", "warning")} {
		persistAndProcess(t, base, processor, event)
	}
	active, err := base.GetAlert(t.Context(), opening.BKTenantID, first.AlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	if active.Alert.Severity != "critical" || active.Alert.EnrichSeverity != "warning" || step.calls != 3 {
		t.Fatalf("severity=%s snapshot severity=%s processor calls=%d", active.Alert.Severity, active.Alert.EnrichSeverity, step.calls)
	}
	row, err := enrich.DecodePayload(active.Alert.Enrich)
	if err != nil || string(row.Processors[0]["fields"].Patches[0].Value) != `"opening-warning:warning"` {
		t.Fatalf("opening snapshot changed: %+v %v", row, err)
	}
}

func TestActiveEnrichReuseSurvivesResultFailureAndConfigChange(t *testing.T) {
	base := memory.New()
	repo := &failPlanOnce{Repository: base}
	processor := newTestProcessor(t, repo, NoopFinalHook{})
	step := &lifecycleReuseStep{}
	chain, err := enrich.NewChain([]enrich.Processor{step}, enrich.Sources{}, enrich.WithReuseOnActive("fields"))
	if err != nil {
		t.Fatal(err)
	}
	processor.enricher = chain
	opening := testEvent("opening", "warning")
	first := persistAndProcess(t, repo, processor, opening)
	incoming := testEvent("retry-reused", "warning")
	created, err := repo.CreateEvent(t.Context(), incoming)
	if err != nil {
		t.Fatal(err)
	}
	repo.fail = true
	if _, err := processor.ProcessEvent(t.Context(), created.StoredEvent); err == nil {
		t.Fatal("expected final result failure")
	}
	stored, err := base.GetEvent(t.Context(), incoming.BKTenantID, incoming.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Event.EnrichStatus != domain.EnrichStatusSucceeded {
		t.Fatal("reused result was not frozen")
	}
	// 配置已经改为每次执行，重试仍必须采用该 Event 已提交的复用事实。
	processor.enricher, err = enrich.NewChain([]enrich.Processor{step}, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := processor.ProcessEvent(t.Context(), stored); err != nil {
		t.Fatal(err)
	}
	row, _ := stored.Event.ForSeverity("warning")
	payload, err := enrich.DecodePayload(row.Data)
	if err != nil || payload.Processors[0]["fields"].ReusedFromAlertID != first.AlertIDs[0] || step.calls != 1 {
		t.Fatalf("frozen reuse changed: %+v calls=%d err=%v", payload, step.calls, err)
	}
}

func TestActiveEnrichReuseDoesNotEnterNewLifecycleInSameEvent(t *testing.T) {
	for _, allLevelsClose := range []bool{false, true} {
		t.Run(map[bool]string{false: "removed active severity", true: "all-level close and new trigger"}[allLevelsClose], func(t *testing.T) {
			base := memory.New()
			processor := newTestProcessor(t, base, NoopFinalHook{})
			processor.upgradePolicy = "update_current"
			step := &lifecycleReuseStep{}
			chain, err := enrich.NewChain([]enrich.Processor{step}, enrich.Sources{}, enrich.WithReuseOnActive("fields"))
			if err != nil {
				t.Fatal(err)
			}
			processor.enricher = chain
			opening := testEvent("opening", "warning")
			first := persistAndProcess(t, base, processor, opening)
			event := testEvent("new-trigger", "warning")
			if allLevelsClose {
				event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: domain.SeverityAll, Action: domain.EventActionClosed})
			} else {
				persistAndProcess(t, base, processor, testEvent("upgrade", "critical"))
				processor.severity = liveSeverityTable{"warning": 1}
			}
			result := persistAndProcess(t, base, processor, event)
			var created domain.Alert
			for _, id := range result.AlertIDs {
				stored, err := base.GetAlert(t.Context(), event.BKTenantID, id)
				if err != nil {
					t.Fatal(err)
				}
				if stored.Alert.Status == domain.AlertStatusActive {
					created = stored.Alert
				}
			}
			if created.AlertID == "" || created.AlertID == first.AlertIDs[0] {
				t.Fatal("new lifecycle was not created")
			}
			payload, err := enrich.DecodePayload(created.Enrich)
			if err != nil {
				t.Fatal(err)
			}
			if payload.Processors[0]["fields"].ReusedFromAlertID != "" || string(payload.Processors[0]["fields"].Patches[0].Value) != `"new-trigger:warning"` {
				t.Fatalf("old enrich snapshot entered new lifecycle: %+v", payload)
			}
		})
	}
}
