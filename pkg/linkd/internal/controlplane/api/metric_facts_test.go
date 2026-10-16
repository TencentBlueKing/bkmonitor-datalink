package api

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/domain"
)

func TestMetricFactsUseOpeningAndRejectWrongScope(t *testing.T) {
	for _, name := range []string{"opening", "wrong tenant", "external", "wrong relation", "recovery", "trigger as recovery", "invalid reference", "busy"} {
		t.Run(name, func(t *testing.T) {
			alert, event, release := enrichRecordFixture(t)
			alert.EventSourceID, event.EventSourceID = "built_in_bk", "built_in_bk"
			alert.TriggerEventID, alert.LatestEventID = event.EventID, "newer-event"
			n123, _ := domain.NewNumberScalar(123)
			n7, _ := domain.NewNumberScalar(7)
			n2, _ := domain.NewNumberScalar(2)
			n0, _ := domain.NewNumberScalar(0)
			event.Labels = domain.DimensionMap{"strategy_id": n123, "strategy_version": n7, "bk_biz_id": n2}
			event.Dimensions = domain.DimensionMap{"enabled": domain.NewBoolScalar(false), "cloud": n0}
			body := `{"bk_tenant_id":"tenant-a","severity":"fatal"}`
			want := 200
			switch name {
			case "wrong tenant":
				body = `{"bk_tenant_id":"other","severity":"fatal"}`
				want = 404
			case "external":
				alert.EventSourceID = "external"
				want = 404
			case "wrong relation":
				event.RelatedAlertIDs = []string{"other"}
				want = 404
			case "recovery", "trigger as recovery":
				body = `{"bk_tenant_id":"tenant-a","severity":"fatal","event_id":"event-1"}`
				if name == "recovery" {
					event.Evaluations[0].Action = domain.EventActionResolved
				} else {
					want = 404
				}
			case "invalid reference":
				delete(event.Labels, "strategy_version")
				want = 422
			case "busy":
				want = 429
			}
			reader := &enrichRecordStub{alert: alert, event: event, release: release}
			service := NewEnrichRecords(reader, reader)
			if name == "busy" {
				service.slots <- struct{}{}
				service.slots <- struct{}{}
			}
			api := &API{EnrichRecords: service}
			r := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(body))
			r.SetPathValue("id", alert.AlertID)
			w := httptest.NewRecorder()
			api.queryMetricFacts(w, r)
			if w.Code != want {
				t.Fatalf("status %d want %d: %s", w.Code, want, w.Body.String())
			}
			if want == 200 {
				var got map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if got["event_id"] != event.EventID || got["strategy_version"] != float64(7) || got["dimensions"].(map[string]any)["enabled"] != false {
					t.Fatalf("wrong frozen facts: %v", got)
				}
			}
		})
	}
}
