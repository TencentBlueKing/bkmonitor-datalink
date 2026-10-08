package preview

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/assembly"
	"linkd/internal/enrich/description"
	"linkd/internal/store/storetest"
)

type previewFacts struct {
	calls int
	err   error
}

func (f *previewFacts) Resolve(_ context.Context, _ domain.Event, v domain.EventEvaluation, _ domain.Alert) (description.Facts, error) {
	f.calls++
	if f.err != nil {
		return description.Facts{}, f.err
	}
	n, err := description.ParseNumber(json.RawMessage(`10`))
	if err != nil {
		return description.Facts{}, err
	}
	threshold := 5.0
	if v.Severity == "critical" {
		threshold = 8
	}
	return description.Facts{ItemName: "CPU", Unit: "percent", Connector: "and", Value: n, Algorithms: []description.Algorithm{{Type: "Threshold", Groups: [][]description.Condition{{{Method: "gt", Threshold: threshold}}}}}}, nil
}

func TestOpeningEventContentPreviewSelectsSeverityWithoutStoredAlertReads(t *testing.T) {
	stub := sourceStub{config.EventSource{EventSourceID: "host", RelatedTenantID: "tenant-a", Enrich: config.EnrichConfig{ContentMode: config.ContentModeBKMonitorDescription}}}
	facts := &previewFacts{}
	closes := 0
	service := New(stub, func(context.Context, string, string) (domain.Alert, error) {
		t.Fatal("event preview read stored Alert")
		return domain.Alert{}, nil
	}, func(_ context.Context, s config.EventSource) (Enricher, func() error, error) {
		r, err := assembly.NewRouter([]config.EventSource{s}, enrich.Sources{}, assembly.WithDescriptionFacts(facts))
		return r, func() error { closes++; return nil }, err
	})
	event := storetest.Event("tenant-a", "e", "f", "warning")
	event.EventSourceID = "host"
	event.EventSourceVersion = 7
	event.Content = "raw source"
	event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "critical", Action: domain.EventActionTriggered})
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	req := Request{BKTenantID: "tenant-a", EventSourceID: "host", Input: Input{Event: encoded, Severity: "critical"}}
	result, err := service.Preview(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	want := "CPU > 8.0, 当前值10%"
	if result.CandidateContent == nil || *result.CandidateContent != want || result.Original["content"] != "raw source" || result.EffectiveAlert["content"] != want || len(result.Changes) == 0 || facts.calls != 1 || closes != 1 {
		t.Fatalf("preview=%+v calls=%d closes=%d", result, facts.calls, closes)
	}
	if event.Content != "raw source" {
		t.Fatal("Event changed")
	}
	for _, input := range []Input{{Event: encoded}, {Event: encoded, Severity: "info"}, {Event: encoded, Severity: "critical", Alert: json.RawMessage(`{}`)}, {Alert: json.RawMessage(`{}`), Severity: "warning"}} {
		req.Input = input
		if _, err := service.Preview(t.Context(), req); err == nil {
			t.Fatal("invalid opening input accepted")
		}
	}
	if facts.calls != 1 {
		t.Fatal("invalid input reached renderer")
	}
	req.Input = Input{Event: encoded, Severity: "critical"}
	facts.err = &description.Error{Code: "history_evidence_missing"}
	_, err = service.Preview(t.Context(), req)
	var previewErr *Error
	if !errors.As(err, &previewErr) || previewErr.Status != 422 || previewErr.Message != "content facts invalid: history_evidence_missing" || closes != 2 {
		t.Fatalf("failure=%v closes=%d", err, closes)
	}
	for _, mutate := range []func(*domain.Event){func(e *domain.Event) { e.BKTenantID = "tenant-b" }, func(e *domain.Event) { e.EventSourceVersion = 6 }, func(e *domain.Event) { e.RelatedAlertIDs = []string{"existing"} }} {
		changed := event.Clone()
		mutate(&changed)
		raw, _ := json.Marshal(changed)
		req.Input = Input{Event: raw, Severity: "critical"}
		if _, err := service.Preview(t.Context(), req); err == nil {
			t.Fatal("invalid event scope accepted")
		}
	}
}
