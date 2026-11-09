package kaccompat

import (
	"encoding/json"
	"io"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/store/storetest"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewMappingAndBulkDocumentDoNotGenerateMetricUniqueID(t *testing.T) {
	t.Parallel()
	templates, mappings, documents := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_ilm/policy/alarm_policy":
			_, _ = w.Write([]byte(`{}`))
		case "/_template/alarm":
			if r.Method == http.MethodGet {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			var body struct {
				Mappings struct {
					Properties map[string]any `json:"properties"`
				} `json:"mappings"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if _, exists := body.Mappings.Properties["metric_unique_id"]; exists {
				t.Error("new template retained removed field")
			}
			if body.Mappings.Properties["item"] == nil {
				t.Error("item mapping was lost")
			}
			templates++
			_, _ = w.Write([]byte(`{}`))
		case "/_alias/alarm":
			_, _ = w.Write([]byte(`{"alarm-000001":{"aliases":{"alarm":{"is_write_index":true}}}}`))
		case "/alarm/_mapping":
			var body struct {
				Properties map[string]any `json:"properties"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			if _, exists := body.Properties["metric_unique_id"]; exists {
				t.Error("new mapping retained removed field")
			}
			mappings++
			_, _ = w.Write([]byte(`{}`))
		case "/alarm/_search":
			_, _ = w.Write([]byte(`{"hits":{"hits":[]}}`))
		case "/_bulk":
			decoder := json.NewDecoder(r.Body)
			var head map[string]any
			if err := decoder.Decode(&head); err != nil {
				t.Error(err)
				return
			}
			var body struct {
				Script struct {
					Params struct {
						Create map[string]any `json:"create"`
						Fields map[string]any `json:"fields"`
					} `json:"params"`
				} `json:"script"`
			}
			if err := decoder.Decode(&body); err != nil {
				t.Error(err)
				return
			}
			for _, fields := range []map[string]any{body.Script.Params.Create, body.Script.Params.Fields} {
				if _, exists := fields["metric_unique_id"]; exists {
					t.Error("KAC bulk request generated removed field")
				}
			}
			fields := body.Script.Params.Create
			if fields["metric_name"] != "usage" || fields["result_table_id"] != "system.cpu" || fields["unit"] != "percent" || fields["bk_tenant_id"] != "tenant" {
				t.Error("retained metric facts or tenant scope changed")
			}
			if err := decoder.Decode(&head); err != io.EOF {
				t.Error("unexpected extra bulk document", err)
			}
			documents++
			_, _ = w.Write([]byte(`{"items":[{"update":{"status":201,"_index":"alarm-000001"}}]}`))
		default:
			t.Error("unexpected request", r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()
	client, err := Open(config.KACPluginConfig{AlarmEventIndex: "alarm", Elasticsearch: config.KACElasticsearchConfig{Addresses: []string{server.URL}}}, func(s string) (string, error) { return s, nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err = client.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	alert := storetest.Alert("tenant", "alert", "event", "fp", "warning")
	alert.EnrichStatus = domain.EnrichStatusSucceeded
	if err = json.Unmarshal([]byte(`{"processors":[{"metric":{"status":"succeeded","value":{"display_name":"CPU","metric_name":"usage","result_table_id":"system.cpu","unit":"percent"}}}]}`), &alert.Enrich); err != nil {
		t.Fatal(err)
	}
	results := client.WriteBatch(t.Context(), []domain.Alert{alert}, false)
	if len(results) != 1 || results[0].Err != nil {
		t.Fatal(results)
	}
	if templates != 1 || mappings != 1 || documents != 1 {
		t.Fatal("initialization or actual bulk path was not covered", templates, mappings, documents)
	}
}
