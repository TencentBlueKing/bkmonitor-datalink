package assembly

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/description"
	"linkd/internal/lifecycle"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type contentFactsFunc func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (description.Facts, error)

func (f contentFactsFunc) Resolve(ctx context.Context, e domain.Event, v domain.EventEvaluation, a domain.Alert) (description.Facts, error) {
	return f(ctx, e, v, a)
}

func TestContentRouteCreatesPersistedDescriptionAndRetainsSourceEvent(t *testing.T) {
	event := storetest.Event("tenant-a", "opening", "fingerprint", "warning")
	event.EventSourceID = "bk"
	event.EventSourceVersion = 7
	event.Content = "source"
	calls := 0
	resolver := contentFactsFunc(func(_ context.Context, e domain.Event, v domain.EventEvaluation, a domain.Alert) (description.Facts, error) {
		calls++
		if e.Content != "source" || v.Severity != "warning" || a.Content != "source" {
			t.Fatal("opening identity lost")
		}
		n, err := description.ParseNumber(json.RawMessage(`10`))
		if err != nil {
			t.Fatal(err)
		}
		return description.Facts{ItemName: "CPU", Unit: "percent", Value: n, Connector: "and", Algorithms: []description.Algorithm{{Type: "Threshold", Groups: [][]description.Condition{{{Method: "gt", Threshold: 5}}}}}}, nil
	})
	source := config.EventSource{EventSourceID: "bk", Version: 7, RelatedTenantID: "tenant-a", Enrich: config.EnrichConfig{ContentMode: config.ContentModeBKMonitorDescription}}
	router, err := NewRouter([]config.EventSource{source}, enrich.Sources{}, WithDescriptionFacts(resolver))
	if err != nil {
		t.Fatal(err)
	}
	repo := memory.New()
	processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, router, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, discardLogger{}, lifecycle.WithAlertContentBuilder(router))
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repo.CreateEvent(t.Context(), event)
	if err != nil {
		t.Fatal(err)
	}
	result, err := processor.ProcessEvent(t.Context(), stored.StoredEvent)
	if err != nil {
		t.Fatal(err)
	}
	alert, err := repo.GetAlert(t.Context(), event.BKTenantID, result.AlertIDs[0])
	if err != nil {
		t.Fatal(err)
	}
	want := "CPU > 5.0, 当前值10%"
	if alert.Alert.Content != want || calls != 1 {
		t.Fatalf("content=%q calls=%d", alert.Alert.Content, calls)
	}
	if _, err := processor.ProcessEvent(t.Context(), stored.StoredEvent); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("replay rendered again")
	}
	kept, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Event.Content != "source" {
		t.Fatal("Event overwritten")
	}
	old := event.Clone()
	old.EventSourceVersion = 6
	_, err = router.BuildContent(t.Context(), old, event.Evaluations[0], alert.Alert)
	var contentErr *description.Error
	if !errors.As(err, &contentErr) || contentErr.Code != "content_source_version" || calls != 1 {
		t.Fatalf("version err=%v calls=%d", err, calls)
	}
}

func TestContentRoutesRejectMissingFactsAndDoNotInvokeResolverForSource(t *testing.T) {
	source := config.EventSource{EventSourceID: "bk", Version: 7, RelatedTenantID: "tenant-a", Enrich: config.EnrichConfig{ContentMode: config.ContentModeBKMonitorDescription}}
	if _, err := NewRouter([]config.EventSource{source}, enrich.Sources{}); err == nil {
		t.Fatal("missing facts resolver accepted")
	}
	source.Version = 0
	if _, err := NewRouter([]config.EventSource{source}, enrich.Sources{}, WithDescriptionFacts(contentFactsFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (description.Facts, error) {
		return description.Facts{}, nil
	}))); err == nil {
		t.Fatal("unpublished description mode accepted")
	}
	source.Version = 7
	source.Enrich.ContentMode = config.ContentModeSource
	router, err := NewRouter([]config.EventSource{source}, enrich.Sources{}, WithDescriptionFacts(contentFactsFunc(func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (description.Facts, error) {
		t.Fatal("source route invoked resolver")
		return description.Facts{}, nil
	})))
	if err != nil {
		t.Fatal(err)
	}
	event := storetest.Event("tenant-a", "e", "f", "warning")
	event.EventSourceID = "bk"
	event.EventSourceVersion = 7
	event.Content = "original"
	opening := domain.Alert{BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID}
	text, err := router.BuildContent(t.Context(), event, event.Evaluations[0], opening)
	if err != nil || text != "original" {
		t.Fatalf("source=%q err=%v", text, err)
	}
	opening.BKTenantID = "tenant-b"
	if _, err := router.BuildContent(t.Context(), event, event.Evaluations[0], opening); err == nil {
		t.Fatal("cross tenant allowed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := router.BuildContent(ctx, event, event.Evaluations[0], opening); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
