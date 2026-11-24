package ingestpreview

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/runtimeconfig"
)

func testRequest() Request {
	c := &Calculation{FingerprintMode: "field", FingerprintField: "source_alert_id", SeverityMapping: map[string]string{"fatal": "critical"}}
	c.Cleaner.Type = "standard"
	return Request{BKTenantID: "tenant", EventSourceID: "source", SessionID: "session", Source: Source{Kind: "draft", Calculation: c}, KACSource: KACSource{ID: "product-source", Name: "外部来源"}, FieldCatalog: []string{"owner"}, Output: "kac_alarm", Samples: []Sample{{ID: "one", ReceivedAt: time.Date(2026, 10, 12, 2, 0, 0, 0, time.UTC), Payload: json.RawMessage(`{"bk_tenant_id":"tenant","event_id":"original-event","alert_id":"original-alert","title":"CPU","content":"source content","evaluations":[{"action":"triggered","severity":"fatal"},{"action":"resolved","severity":"warning"}],"extra_data":{"item":"","bk_biz_id":0,"owner":false,"private_payload":"must not be projected"}}`)}}}
}

func testService(reader SourceReader) *Service {
	state := runtimeconfig.NewSeverity(config.DefaultSeverityConfig())
	return New(reader, state.SeveritySnapshot)
}

func TestDraftPreviewKeepsSourceFieldsAndIsDeterministic(t *testing.T) {
	s := testService(nil)
	a, err := s.Preview(t.Context(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Preview(t.Context(), testRequest())
	if err != nil {
		t.Fatal(err)
	}
	aa, _ := json.Marshal(a)
	bb, _ := json.Marshal(b)
	if string(aa) != string(bb) || a.Version != 0 || len(a.Items) != 2 {
		t.Fatalf("response=%+v", a)
	}
	row := a.Items[0]
	if row.Severity != "critical" || row.KACAlarm["level"] != "fatal" || row.KACAlarm["action"] != "firing" || row.KACAlarm["content"] != "source content" || row.KACAlarm["owner"] != false || row.KACAlarm["item"] != "" || row.KACAlarm["bk_biz_id"] != int64(0) {
		t.Fatalf("fields=%v", row.KACAlarm)
	}
	if _, ok := row.KACAlarm["private_payload"]; ok {
		t.Fatal("arbitrary source data exposed")
	}
	if row.KACAlarm["alarm_time"] != "2026-10-12 10:00:00" {
		t.Fatal(row.KACAlarm)
	}
	if len(a.Items[1].Diagnostics) != 1 {
		t.Fatal("terminal input lost history diagnostic")
	}
}

func TestPreviewRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Request)
	}{
		{"tenant", func(r *Request) { r.BKTenantID = "other" }},
		{"empty samples", func(r *Request) { r.Samples = nil }},
		{"duplicate", func(r *Request) { r.Samples = append(r.Samples, r.Samples[0]) }},
		{"timestamp", func(r *Request) { r.Samples[0].ReceivedAt = time.Time{} }},
		{"protected catalog", func(r *Request) { r.FieldCatalog = []string{"bk_tenant_id"} }},
		{"mixed modes", func(r *Request) { r.Source.Version = 1 }},
		{"invalid mapping", func(r *Request) { r.Source.Calculation.SeverityMapping["fatal"] = "unknown" }},
		{"integer", func(r *Request) {
			r.Samples[0].Payload = json.RawMessage(`{"bk_tenant_id":"tenant","event_id":"e","alert_id":"a","title":"t","evaluations":[{"action":"triggered","severity":"warning"}],"extra_data":{"bk_biz_id":"oops"}}`)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testRequest()
			tc.change(&r)
			if _, err := testService(nil).Preview(t.Context(), r); err == nil {
				t.Fatal("accepted invalid input")
			}
		})
	}
}

type sourceFixture struct {
	block   <-chan struct{}
	entered chan<- struct{}
}

func (s sourceFixture) Get(ctx context.Context, _ string) (eventsource.Record, error) {
	if s.entered != nil {
		s.entered <- struct{}{}
	}
	if s.block != nil {
		select {
		case <-s.block:
		case <-ctx.Done():
			return eventsource.Record{}, ctx.Err()
		}
	}
	return eventsource.Record{ID: "source", Published: 3}, nil
}
func (sourceFixture) GetRelease(context.Context, string, int64) (eventsource.Release, error) {
	return eventsource.Release{Version: 3, Spec: config.EventSource{EventSourceID: "source", RelatedTenantID: "tenant", Cleaner: config.CleanerConfig{Type: "standard"}, FingerprintMode: "field", FingerprintField: "source_alert_id", SeverityMapping: map[string]string{"fatal": "critical"}, Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "unavailable-external"}}}}}, nil
}

func TestPublishedUsesExplicitVersionWithoutEnrich(t *testing.T) {
	r := testRequest()
	r.Source = Source{Kind: "published", Version: 3}
	result, err := testService(sourceFixture{}).Preview(t.Context(), r)
	if err != nil || result.Version != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestPreviewCapacityAndCancellation(t *testing.T) {
	blocked := make(chan struct{})
	entered := make(chan struct{}, 4)
	s := testService(sourceFixture{block: blocked, entered: entered})
	r := testRequest()
	r.Source = Source{Kind: "published", Version: 3}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_, err := s.Preview(ctx, r)
			var e *Error
			if !errors.As(err, &e) || e.Status != 504 {
				t.Errorf("cancel=%v", err)
			}
		})
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("slots not acquired")
		}
	}
	_, err := s.Preview(t.Context(), r)
	var e *Error
	if !errors.As(err, &e) || e.Status != 429 {
		t.Fatalf("capacity=%v", err)
	}
	cancel()
	wg.Wait()
	close(blocked)
	if _, err := s.Preview(t.Context(), r); err != nil {
		t.Fatalf("slots leaked: %v", err)
	}
}

func TestAllSeverityCloseIsNotFiring(t *testing.T) {
	r := testRequest()
	r.Samples[0].Payload = json.RawMessage(`{"bk_tenant_id":"tenant","event_id":"e","alert_id":"a","title":"close","evaluations":[{"action":"closed","severity":"__ALL__"}]}`)
	result, err := testService(nil).Preview(t.Context(), r)
	if err != nil || result.Items[0].Action != domain.EventActionClosed || result.Items[0].KACAlarm["action"] != "close" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}
