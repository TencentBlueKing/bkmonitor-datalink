// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/processingerror"
	"linkd/internal/store/memory"
)

func TestControlPlaneFailureDoesNotFreezeEnrichOrContentFallback(t *testing.T) {
	for _, stage := range []string{"enrich", "content"} {
		t.Run(stage, func(t *testing.T) {
			repo := memory.New()
			p := newTestProcessor(t, repo, NoopFinalHook{})
			event := testEvent("control-plane-outage", "warning")
			created, err := repo.CreateEvent(t.Context(), event)
			if err != nil {
				t.Fatal(err)
			}
			failure := fmt.Errorf("read historical release: %w", processingerror.ErrControlPlaneUnavailable)
			if stage == "enrich" {
				p.enricher = stubEnricher{fn: func(enrich.Input) (enrich.Result, error) { return enrich.Result{}, failure }}
			} else {
				p.contentBuilder = contentBuilderFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error) {
					return "", failure
				})
			}
			for range 4 {
				if _, err := p.ProcessEvent(t.Context(), created.StoredEvent); !errors.Is(err, processingerror.ErrControlPlaneUnavailable) {
					t.Fatal(err)
				}
				stored, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
				if err != nil || stored.Processing.State != domain.EventProcessStateUnprocessed || len(stored.Event.RelatedAlertIDs) != 0 {
					t.Fatal("failed query completed Event", stored, err)
				}
				if stage == "enrich" && stored.Event.EnrichStatus != "" && stored.Event.EnrichStatus != domain.EnrichStatusPending {
					t.Fatal("failed query froze Enrich", stored.Event.EnrichStatus)
				}
			}
			p.enricher = testNoopEnricher{}
			p.contentBuilder = SourceContentBuilder{}
			stored, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := p.ProcessEvent(t.Context(), stored)
			if err != nil || len(result.AlertIDs) != 1 {
				t.Fatal("original Event did not resume", result, err)
			}
		})
	}
}
