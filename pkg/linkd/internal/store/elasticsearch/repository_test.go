// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

type transportFunc func(request *http.Request) (*http.Response, error)

func (f transportFunc) Perform(request *http.Request) (*http.Response, error) { return f(request) }

type eventRouteOverride struct {
	Router
	route Route
}

func (r eventRouteOverride) EventRoute(context.Context, string) (Route, error) {
	return r.route, nil
}

func TestDocumentCodecs(t *testing.T) {
	event := storetest.Event("tenant-1", "event-1", "fp", "warning")
	event.Content = "CPU has remained above threshold"
	event.ActionReason = "threshold_exceeded"
	event.ConditionName = "CPU usage"
	event.SubjectSystem = "cmdb"
	event.SubjectType = "host"
	event.SubjectID = "host-1"
	event.SubjectName = "server-1"
	event.Labels = domain.DimensionMap{"team": domain.NewStringScalar("ops")}
	event.SourceRawData = domain.JSONObject{"source": json.RawMessage(`{"status":"firing"}`)}
	event.ExtraData = domain.JSONObject{"ticket": json.RawMessage(`"INC-1"`)}
	processing := store.NewUnprocessedEventProcessing()
	data, err := encodeEventDocument(event, processing)
	if err != nil {
		t.Fatal(err)
	}
	var document eventDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Processing.State != domain.EventProcessStateUnprocessed || document.Fingerprint != "fp" || len(document.Dimensions) != 1 {
		t.Fatalf("event document=%#v", document)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["payload"]; exists {
		t.Fatal("event document still contains payload")
	}
	if _, exists := fields["processing_state"]; exists {
		t.Fatal("event document still contains processing_state")
	}
	stored, err := decodeEventHit(searchHit{Index: "linkd-test-events", ID: documentID(event.BKTenantID, event.EventID), SeqNo: 0, PrimaryTerm: 1, Source: data})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored.Event, event) || !reflect.DeepEqual(stored.Processing, processing) {
		t.Fatalf("stored event=%#v, want event=%#v processing=%#v", stored, event, processing)
	}

	processedAt := event.CreateAt.Add(time.Minute)
	for _, state := range []domain.EventProcessState{domain.EventProcessStateAccepted, domain.EventProcessStateSuppressed, domain.EventProcessStateOrphaned, domain.EventProcessStateRejected} {
		t.Run(string(state), func(t *testing.T) {
			terminalEvent := event.Clone()
			if state == domain.EventProcessStateAccepted || state == domain.EventProcessStateSuppressed {
				terminalEvent.RelatedAlertID = "alert-1"
			}
			terminalProcessing := store.EventProcessing{State: state, Outcome: "test_outcome", ReasonCode: "test_reason", ProcessedAt: &processedAt}
			encoded, encodeErr := encodeEventDocument(terminalEvent, terminalProcessing)
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			decoded, decodeErr := decodeEventHit(searchHit{Index: "linkd-test-events", ID: documentID(terminalEvent.BKTenantID, terminalEvent.EventID), SeqNo: 1, PrimaryTerm: 1, Source: encoded})
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if decoded.Processing.State != state || decoded.Processing.Outcome != terminalProcessing.Outcome || decoded.Event.RelatedAlertID != terminalEvent.RelatedAlertID {
				t.Fatalf("decoded terminal event=%#v", decoded)
			}
		})
	}

	alert := storetest.Alert("tenant-1", "alert-1", "event-1", "fp", "warning")
	alert.Content = event.Content
	alert.ConditionName = event.ConditionName
	alert.SubjectSystem = event.SubjectSystem
	alert.SubjectType = event.SubjectType
	alert.SubjectID = event.SubjectID
	alert.SubjectName = event.SubjectName
	alert.Enrich = domain.JSONObject{"owner": json.RawMessage(`"ops"`)}
	data, err = encodeAlertDocument(alert)
	if err != nil {
		t.Fatal(err)
	}
	var alertDoc alertDocument
	if err := json.Unmarshal(data, &alertDoc); err != nil {
		t.Fatal(err)
	}
	if alertDoc.UpdateAt != alert.UpdateAt || alertDoc.LatestEventID != "event-1" {
		t.Fatalf("alert document=%#v", alertDoc)
	}
	fields = nil
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["payload"]; exists {
		t.Fatal("alert document still contains payload")
	}
	storedAlert, err := decodeAlertHit(searchHit{Index: "linkd-test-alerts", ID: documentID(alert.BKTenantID, alert.AlertID), SeqNo: 0, PrimaryTerm: 1, Source: data})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedAlert.Alert, alert) {
		t.Fatalf("stored alert=%#v, want=%#v", storedAlert.Alert, alert)
	}

	log := testAlertLog("log-1")
	log.Params = domain.JSONObject{"channel": json.RawMessage(`"kafka"`)}
	data, err = encodeAlertLogDocument(log)
	if err != nil {
		t.Fatal(err)
	}
	fields = nil
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["payload"]; exists {
		t.Fatal("alert log document still contains payload")
	}
	storedLog, err := decodeAlertLogHit(searchHit{Index: "linkd-test-alert-logs", ID: documentID(log.BKTenantID, log.LogID), Source: data})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(storedLog, log) {
		t.Fatalf("stored log=%#v, want=%#v", storedLog, log)
	}
}

func TestAlertDocumentIDUsesAlertIdentity(t *testing.T) {
	first := storetest.Alert("tenant-1", "alert-1", "event-1", "shared-fingerprint", "warning")
	second := first.Clone()
	second.AlertID = "alert-2"
	if alertDocumentID(first) == alertDocumentID(second) {
		t.Fatal("different alert IDs must use different document IDs")
	}

	sameIdentity := first.Clone()
	sameIdentity.Fingerprint = "another-fingerprint"
	if alertDocumentID(first) != alertDocumentID(sameIdentity) {
		t.Fatal("alert document ID must not depend on fingerprint")
	}
}

func TestMappings(t *testing.T) {
	events := eventProperties()
	assertMappingFields(t, events, reflect.TypeFor[domain.Event](), "processing")
	processing := processingProperties(t, events)
	alerts := alertProperties()
	logs := alertLogProperties()
	assertMappingFields(t, processing, reflect.TypeFor[store.EventProcessing]())
	assertMappingFields(t, alerts, reflect.TypeFor[domain.Alert]())
	assertMappingFields(t, logs, reflect.TypeFor[domain.AlertLog]())

	assertPropertyTypes(t, events, "keyword", "bk_tenant_id", "event_source_id", "related_alert_id", "event_id", "fingerprint", "title", "content", "severity", "action", "action_reason", "condition_key", "condition_name", "subject_system", "subject_type", "subject_id", "subject_name", "source_event_id", "source_alert_id")
	assertPropertyTypes(t, events, "date_nanos", "occurred_at", "produced_at", "received_at", "create_at")
	assertPropertyTypes(t, events, "flattened", "dimensions", "labels")
	assertPropertyTypes(t, events, "object", "source_raw_data", "extra_data", "processing")
	assertPropertyTypes(t, processing, "keyword", "state", "outcome", "reason_code")
	assertPropertyTypes(t, processing, "date_nanos", "processed_at")

	assertPropertyTypes(t, alerts, "keyword", "alert_id", "bk_tenant_id", "event_source_id", "fingerprint", "title", "content", "severity", "condition_key", "condition_name", "subject_system", "subject_type", "subject_id", "subject_name", "source_event_id", "source_alert_id", "status", "latest_event_id", "trigger_event_id", "end_type", "end_reason", "enrich_status")
	assertPropertyTypes(t, alerts, "date_nanos", "last_occurred_at", "update_at", "begin_at", "create_at", "end_at")
	assertPropertyTypes(t, alerts, "flattened", "dimensions", "labels")
	assertPropertyTypes(t, alerts, "object", "extra_data", "enrich")

	assertPropertyTypes(t, logs, "keyword", "log_id", "bk_tenant_id", "alert_id", "operator_kind", "operation_kind")
	assertPropertyTypes(t, logs, "object", "params")
	assertPropertyTypes(t, logs, "date_nanos", "created_time")

	for _, property := range []map[string]any{events["dimensions"].(map[string]any), events["labels"].(map[string]any), alerts["dimensions"].(map[string]any), alerts["labels"].(map[string]any)} {
		if property["type"] != "flattened" || property["ignore_above"] != flattenedIgnoreAbove {
			t.Fatalf("flattened property=%#v", property)
		}
	}
	for _, property := range []map[string]any{events["source_raw_data"].(map[string]any), events["extra_data"].(map[string]any), alerts["extra_data"].(map[string]any), alerts["enrich"].(map[string]any), logs["params"].(map[string]any)} {
		if property["type"] != "object" || property["enabled"] != false {
			t.Fatalf("opaque property=%#v", property)
		}
	}
	for _, field := range []string{"title", "content", "action_reason", "condition_name", "subject_name"} {
		property := events[field].(map[string]any)
		if property["type"] != "keyword" || property["index"] != false || property["doc_values"] != false {
			t.Fatalf("stored-only event field %q=%#v", field, property)
		}
	}
	if _, exists := events["payload"]; exists {
		t.Fatal("event payload mapping still exists")
	}
	if _, exists := events["processing_state"]; exists {
		t.Fatal("event processing_state mapping still exists")
	}
}

func assertPropertyTypes(t *testing.T, properties map[string]any, wantType string, fields ...string) {
	t.Helper()
	for _, field := range fields {
		property, ok := properties[field].(map[string]any)
		if !ok || property["type"] != wantType {
			t.Fatalf("property %q=%#v, want type %q", field, properties[field], wantType)
		}
	}
}

func assertMappingFields(t *testing.T, properties map[string]any, model reflect.Type, extras ...string) {
	t.Helper()
	want := make(map[string]struct{}, model.NumField()+len(extras))
	for index := 0; index < model.NumField(); index++ {
		name := strings.Split(model.Field(index).Tag.Get("json"), ",")[0]
		if name != "" && name != "-" {
			want[name] = struct{}{}
		}
	}
	for _, name := range extras {
		want[name] = struct{}{}
	}
	if len(properties) != len(want) {
		t.Fatalf("mapping fields=%v, want model fields=%v", mapKeys(properties), mapKeys(want))
	}
	for name := range want {
		if _, exists := properties[name]; !exists {
			t.Fatalf("mapping lacks model field %q; fields=%v", name, mapKeys(properties))
		}
	}
}

func processingProperties(t *testing.T, events map[string]any) map[string]any {
	t.Helper()
	processing := events["processing"].(map[string]any)
	if processing["type"] != "object" || processing["dynamic"] != "strict" {
		t.Fatalf("processing mapping=%#v", processing)
	}
	return processing["properties"].(map[string]any)
}

func mapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func TestEnsureIndexRejectsIncompatibleSchema(t *testing.T) {
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPut && request.URL.Path == "/linkd-test-events":
			return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{"error":{"type":"resource_already_exists_exception","reason":"exists"}}`))}, nil
		case request.Method == http.MethodGet && request.URL.Path == "/linkd-test-events/_mapping":
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"linkd-test-events":{"mappings":{"_meta":{"managed_by":"linkd","entity":"event","schema_version":1}}}}`))}, nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	repository, err := New(transport, mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	err = repository.EnsureIndex(context.Background(), "linkd-test-events", entityEvent)
	if err == nil || !strings.Contains(err.Error(), "delete the index") {
		t.Fatalf("EnsureIndex() error=%v", err)
	}
}

func TestEnsureIndexAcceptsCurrentSchema(t *testing.T) {
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPut && request.URL.Path == "/linkd-test-events":
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"acknowledged":true}`))}, nil
		case request.Method == http.MethodGet && request.URL.Path == "/linkd-test-events/_mapping":
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"linkd-test-events":{"mappings":{"_meta":{"managed_by":"linkd","entity":"event","schema_version":3}}}}`))}, nil
		default:
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
			return nil, nil
		}
	})
	repository, err := New(transport, mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.EnsureIndex(context.Background(), "linkd-test-events", entityEvent); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureSchemaWritesVersionedStrictTemplates(t *testing.T) {
	written := map[string]bool{}
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPut || !strings.HasPrefix(request.URL.Path, "/_index_template/linkd-test-") {
			t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		metadata := body["_meta"].(map[string]any)
		if body["version"] != float64(currentSchemaVersion) || metadata["managed_by"] != managedByLinkd || metadata["schema_version"] != float64(currentSchemaVersion) {
			t.Fatalf("template metadata=%#v", body)
		}
		mappings := body["template"].(map[string]any)["mappings"].(map[string]any)
		if mappings["dynamic"] != "strict" || !reflect.DeepEqual(mappings["_meta"], metadata) {
			t.Fatalf("template mappings=%#v, metadata=%#v", mappings, metadata)
		}
		written[request.URL.Path] = true
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"acknowledged":true}`))}, nil
	})
	repository, err := New(transport, mustStaticRouter(t), DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.EnsureSchema(context.Background(), mustStaticRouter(t).SchemaConfig()); err != nil {
		t.Fatal(err)
	}
	if len(written) != 3 {
		t.Fatalf("written templates=%v", mapKeys(written))
	}
}

func mustStaticRouter(t *testing.T) *StaticRouter {
	t.Helper()
	router, err := NewStaticRouter("linkd-test")
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func TestStaticRouterUsesSemanticNames(t *testing.T) {
	router, err := NewStaticRouter("linkd-test")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"linkd-test-events", "linkd-test-alerts", "linkd-test-alert-logs"}
	for index, target := range router.Targets() {
		if target != want[index] {
			t.Fatalf("target[%d]=%q, want %q", index, target, want[index])
		}
	}
}

func TestUnprocessedEventScansUseProcessingObjectState(t *testing.T) {
	for _, scan := range []struct {
		name string
		run  func(*Repository) error
	}{
		{name: "tenant", run: func(repository *Repository) error {
			_, err := repository.ScanUnprocessedEvents(context.Background(), "tenant-1", time.Now().UTC(), store.PageRequest{Limit: 10})
			return err
		}},
		{name: "all tenants", run: func(repository *Repository) error {
			_, err := repository.ScanAllUnprocessedEvents(context.Background(), time.Now().UTC(), store.PageRequest{Limit: 10})
			return err
		}},
	} {
		t.Run(scan.name, func(t *testing.T) {
			var searchBody string
			transport := transportFunc(func(request *http.Request) (*http.Response, error) {
				switch {
				case request.Method == http.MethodPost && request.URL.Path == "/linkd-test-events/_pit":
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"id":"pit-1"}`))}, nil
				case request.Method == http.MethodPost && request.URL.Path == "/_search":
					body, err := io.ReadAll(request.Body)
					if err != nil {
						t.Fatal(err)
					}
					searchBody = string(body)
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"pit_id":"pit-1","hits":{"hits":[]}}`))}, nil
				case request.Method == http.MethodDelete && request.URL.Path == "/_pit":
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"succeeded":true}`))}, nil
				default:
					t.Fatalf("unexpected request %s %s", request.Method, request.URL.Path)
					return nil, nil
				}
			})
			repository, err := New(transport, mustStaticRouter(t), DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			if err := scan.run(repository); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(searchBody, `"processing.state":"unprocessed"`) || strings.Contains(searchBody, "processing_state") {
				t.Fatalf("search body=%s", searchBody)
			}
		})
	}
}

func TestCreateEventsUsesNDJSONBulkAndKeepsTemporaryItemFailure(t *testing.T) {
	router, err := NewStaticRouter("linkd-test")
	if err != nil {
		t.Fatal(err)
	}
	event1 := storetest.Event("tenant-1", "event-1", "fp-1", "warning")
	event2 := storetest.Event("tenant-1", "event-2", "fp-2", "warning")
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/_bulk" || request.URL.Query().Get("refresh") != "wait_for" {
			t.Fatalf("request=%s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Content-Type") != "application/x-ndjson" {
			t.Fatalf("content-type=%q", request.Header.Get("Content-Type"))
		}
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.HasSuffix(string(body), "\n") || strings.Count(string(body), `{"create":`) != 2 {
			t.Fatalf("bulk body=%q", body)
		}
		response := `{"items":[` +
			`{"create":{"_index":"linkd-test-events","_id":"` + documentID(event1.BKTenantID, event1.EventID) + `","_seq_no":1,"_primary_term":1,"status":201}},` +
			`{"create":{"_index":"linkd-test-events","_id":"` + documentID(event2.BKTenantID, event2.EventID) + `","status":429,"error":{"type":"es_rejected_execution_exception","reason":"busy"}}}` +
			`]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
	})
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	results, err := repository.CreateEvents(context.Background(), []domain.Event{event1, event2})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Err != nil || !results[0].Result.Created || results[1].Err == nil {
		t.Fatalf("results=%+v", results)
	}
	if errors.Is(results[1].Err, store.ErrInvalidArgument) {
		t.Fatalf("429 must remain retryable: %v", results[1].Err)
	}
}

func TestAppendAlertLogsUsesNDJSONBulkWithoutRefreshWait(t *testing.T) {
	router, err := NewStaticRouter("linkd-test")
	if err != nil {
		t.Fatal(err)
	}
	logs := []domain.AlertLog{testAlertLog("log-1"), testAlertLog("log-2")}
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodPost || request.URL.Path != "/_bulk" || request.URL.Query().Get("refresh") != "false" {
			t.Fatalf("request=%s %s", request.Method, request.URL.String())
		}
		if request.Header.Get("Content-Type") != "application/x-ndjson" {
			t.Fatalf("content-type=%q", request.Header.Get("Content-Type"))
		}
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.HasSuffix(string(body), "\n") || strings.Count(string(body), `{"create":`) != 2 {
			t.Fatalf("bulk body=%q", body)
		}
		response := `{"items":[` +
			`{"create":{"_index":"linkd-test-alert-logs","_id":"` + documentID(logs[0].BKTenantID, logs[0].LogID) + `","status":201}},` +
			`{"create":{"_index":"linkd-test-alert-logs","_id":"` + documentID(logs[1].BKTenantID, logs[1].LogID) + `","status":429,"error":{"type":"es_rejected_execution_exception","reason":"busy"}}}` +
			`]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
	})
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	results, err := repository.AppendAlertLogs(context.Background(), logs)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Err != nil || !results[0].Result.Created || results[1].Err == nil {
		t.Fatalf("results=%+v", results)
	}
	if errors.Is(results[1].Err, store.ErrInvalidArgument) {
		t.Fatalf("429 must remain retryable: %v", results[1].Err)
	}
}

func TestAppendAlertLogsConflictUsesRealtimeGET(t *testing.T) {
	tests := []struct {
		name         string
		mutate       func(*domain.AlertLog)
		wantConflict bool
	}{
		{name: "same content is idempotent"},
		{name: "different content conflicts", mutate: func(log *domain.AlertLog) {
			log.Params = domain.JSONObject{"changed": []byte(`true`)}
		}, wantConflict: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, err := NewStaticRouter("linkd-test")
			if err != nil {
				t.Fatal(err)
			}
			requested := testAlertLog("log-conflict")
			existing := requested.Clone()
			if tt.mutate != nil {
				tt.mutate(&existing)
			}
			document, err := encodeAlertLogDocument(existing)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			transport := transportFunc(func(request *http.Request) (*http.Response, error) {
				calls++
				documentID := documentID(requested.BKTenantID, requested.LogID)
				switch request.URL.Path {
				case "/_bulk":
					response := `{"items":[{"create":{"_index":"linkd-test-alert-logs","_id":"` + documentID + `","status":409,"error":{"type":"version_conflict_engine_exception","reason":"exists"}}}]}`
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
				case "/linkd-test-alert-logs/_doc/" + documentID:
					if request.URL.Query().Get("realtime") != "true" {
						t.Fatalf("GET query=%s", request.URL.RawQuery)
					}
					response := `{"_index":"linkd-test-alert-logs","_id":"` + documentID + `","_seq_no":1,"_primary_term":1,"found":true,"_source":` + string(document) + `}`
					return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
				default:
					t.Fatalf("unexpected request=%s %s", request.Method, request.URL.String())
					return nil, nil
				}
			})
			repository, err := New(transport, router, DefaultConfig())
			if err != nil {
				t.Fatal(err)
			}
			results, err := repository.AppendAlertLogs(context.Background(), []domain.AlertLog{requested})
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || calls != 2 || errors.Is(results[0].Err, store.ErrIdentityConflict) != tt.wantConflict {
				t.Fatalf("results=%+v calls=%d", results, calls)
			}
			if !tt.wantConflict && (results[0].Err != nil || results[0].Result.Created) {
				t.Fatalf("idempotent result=%+v", results[0])
			}
		})
	}
}

func TestAppendAlertLogsReturnsRequestFailure(t *testing.T) {
	router, err := NewStaticRouter("linkd-test")
	if err != nil {
		t.Fatal(err)
	}
	transport := transportFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body: io.NopCloser(strings.NewReader(
				`{"error":{"type":"unavailable_shards_exception","reason":"unavailable"}}`,
			)),
		}, nil
	})
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	results, err := repository.AppendAlertLogs(context.Background(), []domain.AlertLog{testAlertLog("log-request-failure")})
	if err == nil || results != nil {
		t.Fatalf("AppendAlertLogs()=%#v,%v, want request failure", results, err)
	}
	if errors.Is(err, store.ErrInvalidArgument) {
		t.Fatalf("503 must remain retryable: %v", err)
	}
}

func TestCompareAndSetEventResultDoesNotWaitForRefresh(t *testing.T) {
	router, err := NewStaticRouter("linkd-test")
	if err != nil {
		t.Fatal(err)
	}
	event := storetest.Event("tenant-1", "event-cas", "fp", "warning")
	document, err := encodeEventDocument(event, store.NewUnprocessedEventProcessing())
	if err != nil {
		t.Fatal(err)
	}
	documentID := documentID(event.BKTenantID, event.EventID)
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/linkd-test-events/_doc/" + documentID:
			if request.Method == http.MethodGet {
				if request.URL.Query().Get("realtime") != "true" {
					t.Fatalf("GET query=%s", request.URL.RawQuery)
				}
				response := `{"_index":"linkd-test-events","_id":"` + documentID + `","_seq_no":1,"_primary_term":1,"found":true,"_source":` + string(document) + `}`
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
			}
			if request.URL.Query().Get("refresh") != "false" {
				t.Fatalf("CAS query=%s", request.URL.RawQuery)
			}
			response := `{"_index":"linkd-test-events","_id":"` + documentID + `","_seq_no":2,"_primary_term":1}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
		default:
			t.Fatalf("unexpected request=%s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	version, err := encodeVersion(versionPayload{Index: "linkd-test-events", DocumentID: documentID, SeqNo: 1, PrimaryTerm: 1})
	if err != nil {
		t.Fatal(err)
	}
	processedAt := event.CreateAt.Add(time.Minute)
	stored, err := repository.CompareAndSetEventResult(
		context.Background(),
		event.BKTenantID,
		event.EventID,
		version,
		store.EventResult{State: domain.EventProcessStateAccepted, RelatedAlertID: "alert-1", Outcome: "alert_created", ProcessedAt: processedAt},
	)
	if err != nil || stored.Processing.State != domain.EventProcessStateAccepted {
		t.Fatalf("CompareAndSetEventResult()=%#v,%v", stored, err)
	}
}

func TestGetEventUsesRealtimeWriteTarget(t *testing.T) {
	baseRouter, err := NewStaticRouter("linkd-test")
	if err != nil {
		t.Fatal(err)
	}
	router := eventRouteOverride{
		Router: baseRouter,
		route: Route{
			WriteTarget: "linkd-test-events-write",
			ReadTargets: []string{"linkd-test-events-read-*"},
		},
	}
	event := storetest.Event("tenant-1", "event-realtime-get", "fp", "warning")
	document, err := encodeEventDocument(event, store.NewUnprocessedEventProcessing())
	if err != nil {
		t.Fatal(err)
	}
	documentID := documentID(event.BKTenantID, event.EventID)
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/linkd-test-events-write/_doc/"+documentID {
			t.Fatalf("unexpected request=%s %s", request.Method, request.URL.String())
		}
		if request.URL.Query().Get("realtime") != "true" {
			t.Fatalf("GET query=%s", request.URL.RawQuery)
		}
		response := `{"_index":"linkd-test-events-20260901","_id":"` + documentID + `","_seq_no":1,"_primary_term":1,"found":true,"_source":` + string(document) + `}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
	})
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetEvent(context.Background(), event.BKTenantID, event.EventID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Event.EventID != event.EventID || stored.Processing.State != domain.EventProcessStateUnprocessed {
		t.Fatalf("GetEvent()=%#v", stored)
	}
}

func TestAlertCreateAndCASWaitForRefresh(t *testing.T) {
	router, err := NewStaticRouter("linkd-test")
	if err != nil {
		t.Fatal(err)
	}
	alert := storetest.Alert("tenant-1", "alert-refresh", "event-1", "fp", "warning")
	document, err := encodeAlertDocument(alert)
	if err != nil {
		t.Fatal(err)
	}
	documentID := alertDocumentID(alert)
	transport := transportFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/linkd-test-alerts/_search":
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"hits":{"hits":[]}}`))}, nil
		case "/linkd-test-alerts/_create/" + documentID:
			if request.URL.Query().Get("refresh") != "wait_for" {
				t.Fatalf("create query=%s", request.URL.RawQuery)
			}
			response := `{"_index":"linkd-test-alerts","_id":"` + documentID + `","_seq_no":1,"_primary_term":1}`
			return &http.Response{StatusCode: http.StatusCreated, Body: io.NopCloser(strings.NewReader(response))}, nil
		case "/_msearch":
			response := `{"responses":[{"status":200,"hits":{"hits":[{"_index":"linkd-test-alerts","_id":"` + documentID + `","_seq_no":1,"_primary_term":1,"_source":` + string(document) + `}]}}]}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
		case "/linkd-test-alerts/_doc/" + documentID:
			if request.URL.Query().Get("refresh") != "wait_for" {
				t.Fatalf("CAS query=%s", request.URL.RawQuery)
			}
			response := `{"_index":"linkd-test-alerts","_id":"` + documentID + `","_seq_no":2,"_primary_term":1}`
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(response))}, nil
		default:
			t.Fatalf("unexpected request=%s %s", request.Method, request.URL.String())
			return nil, nil
		}
	})
	repository, err := New(transport, router, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	created, err := repository.CreateAlert(context.Background(), alert)
	if err != nil {
		t.Fatal(err)
	}
	replacement := alert.Clone()
	replacement.Status = domain.AlertStatusRecovered
	replacement.LatestEventID = "event-2"
	replacement.LastOccurredAt = replacement.LastOccurredAt.Add(time.Minute)
	replacement.UpdateAt = replacement.UpdateAt.Add(time.Minute)
	endAt := replacement.LastOccurredAt
	replacement.EndAt = &endAt
	replacement.EndType = domain.AlertEndTypeSource
	if _, err := repository.CompareAndSetAlert(
		context.Background(),
		alert.BKTenantID,
		alert.AlertID,
		created.Version,
		replacement,
	); err != nil {
		t.Fatal(err)
	}
}

func testAlertLog(logID string) domain.AlertLog {
	return domain.AlertLog{
		LogID: logID, BKTenantID: "tenant-1", AlertID: "alert-1",
		OperatorKind: domain.OperatorKindSource, OperationKind: domain.OperationKindTrigger,
		Params: domain.JSONObject{}, CreatedTime: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}
}
