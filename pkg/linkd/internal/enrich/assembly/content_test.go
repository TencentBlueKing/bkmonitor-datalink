// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

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
	event.EventSourceVersion = 6
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
	if kept.Event.Content != "source" || kept.Event.EventSourceVersion != 6 || kept.Event.EnrichSourceVersion != 7 {
		t.Fatal("source facts or executed enrich version lost", kept.Event)
	}
	old := event.Clone()
	old.EventSourceVersion = 5
	oldOpening := alert.Alert.Clone()
	oldOpening.EventSourceVersion = old.EventSourceVersion
	oldOpening.Content = old.Content
	content, err := router.BuildContent(t.Context(), old, event.Evaluations[0], oldOpening)
	if err != nil || content != want || calls != 2 {
		t.Fatalf("current source config not used: content=%q err=%v calls=%d", content, err, calls)
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
