// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package assembly

import (
	"encoding/json"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/view"
)

func TestRouterActiveReuseSkipsCMDBAndPreservesFollowingFields(t *testing.T) {
	var source config.EventSource
	raw := `{"event_source_id":"host","enrich":{"processors":[{"type":"cmdb","reuse_on_active":true,"config":{"rules":[{"id":"host","lookup":{"model_id":"host","where":{"field":"model_inst_id","type":"keyword","operator":"eq","value":{"literal":"1"}}},"assignments":[{"target":"$.labels.owner","value":{"jsonpath":"$.lookup.attributes.owner"}}]}]}},{"type":"fields","config":{"rules":[{"id":"title","operations":[{"id":"set","type":"assign","assignments":[{"target":"$.title","value":{"template":"${title}:${owner}","variables":{"title":{"jsonpath":"$.original.title"},"owner":{"jsonpath":"$.event.labels.owner"}}}}]}]}]}}]}}`
	if err := json.Unmarshal([]byte(raw), &source); err != nil {
		t.Fatal(err)
	}
	reader := &eventQueryReader{}
	resources := enrich.Sources{CMDB: reader}
	router, err := NewRouter([]config.EventSource{source}, resources)
	if err != nil {
		t.Fatal(err)
	}
	event := baseCollectAlert("host")
	first, err := router.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil || first.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("opening: %+v %v", first, err)
	}
	active := domain.Alert{AlertID: "opening-alert", BKTenantID: event.BKTenantID, EventSourceID: event.EventSourceID,
		Fingerprint: event.Fingerprint, Status: domain.AlertStatusActive, EnrichSeverity: "warning", Enrich: first.Data.Evaluations[0].Data.Clone()}
	event.EventID, event.Title = "repeat", "changed title"
	second, err := router.Enrich(t.Context(), enrich.Input{Event: event, ActiveAlert: &active})
	if err != nil || second.Status != domain.EnrichStatusSucceeded || reader.calls.Load() != 1 {
		t.Fatalf("repeat=%+v queries=%d err=%v", second, reader.calls.Load(), err)
	}
	event.Enrich = second.Data
	effective, err := view.EnrichedEvent(event, "warning")
	if err != nil || effective.Title != "changed title:alice" {
		t.Fatalf("effective=%+v err=%v", effective, err)
	}
	source.Enrich.Processors[0].ReuseOnActive = false
	router, err = NewRouter([]config.EventSource{source}, resources)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := router.Enrich(t.Context(), enrich.Input{Event: event, ActiveAlert: &active}); err != nil {
		t.Fatal(err)
	}
	if reader.calls.Load() != 2 {
		t.Fatalf("disabled reuse queries=%d, want 2", reader.calls.Load())
	}
}
