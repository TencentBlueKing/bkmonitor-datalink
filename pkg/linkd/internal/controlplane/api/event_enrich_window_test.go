// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/eventsource"
	"linkd/internal/store"
)

type eventWindowStub struct {
	enrichRecordStub
	page           store.EventPage
	query          store.EventBySourceRequest
	tenant, source string
}

func (s *eventWindowStub) ListEventsBySource(_ context.Context, tenant, source string, q store.EventBySourceRequest) (store.EventPage, error) {
	s.tenant, s.source, s.query = tenant, source, q
	return s.page, s.err
}

func TestEventEnrichWindowReadsFrozenResultWithoutAlert(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	event.RelatedAlertIDs = nil
	event.ReceivedAt = time.Date(2026, 10, 13, 0, 15, 0, 0, time.UTC)
	stored := store.StoredEvent{Event: event}
	item := projectEventWindowItem(stored, event.Evaluations[0], release, true)
	if item.Unavailable || item.Status != "succeeded" || item.Alarm["name"] != "<script>text only</script>" || item.Original["name"] != "before" || item.Enrichments[0].Name != "标题规范化" || item.FieldChanges[0].Measures[0] != "fields:title" {
		t.Fatalf("item=%+v", item)
	}
	if !matchEventWindowFilters(item, map[string]eventWindowFilter{"enrich_method_name": {Value: "标题", Match: "contains"}}) || matchEventWindowFilters(item, map[string]eventWindowFilter{"name": {Value: "before", Match: "equals"}}) {
		t.Fatal("filters must use effective fields and frozen rule names")
	}
}

func TestEventEnrichWindowPreservesPendingPartialAndUnavailable(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	event.EnrichStatus = domain.EnrichStatusPending
	item := projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], eventsource.Release{}, true)
	if item.Status != "pending" || item.Unavailable {
		t.Fatalf("pending=%+v", item)
	}
	event.EnrichStatus = domain.EnrichStatusPartial
	event.Enrich.Evaluations[0].Status = domain.EnrichStatusPartial
	payload, err := enrich.DecodePayload(event.Enrich.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	payload.Processors = append(payload.Processors, enrich.ProcessorEntry{"metric": {Status: domain.EnrichStatusFailed, Patches: []domain.EnrichPatch{}, Diagnostics: []enrich.Diagnostic{{Code: "dependency_invalid", Dependency: "metric_catalog"}}}})
	event.Enrich.Evaluations[0].Data, err = payload.JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	item = projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, true)
	if item.Status != "partial" || len(item.Diagnostics) != 1 || item.Diagnostics[0].Dependency != "metric_catalog" {
		t.Fatalf("partial=%+v", item)
	}
	event.EnrichStatus = domain.EnrichStatusFailed
	event.Enrich.Evaluations[0].Status = domain.EnrichStatusFailed
	for _, entry := range payload.Processors {
		for key, envelope := range entry {
			envelope.Status = domain.EnrichStatusFailed
			envelope.Patches = []domain.EnrichPatch{}
			entry[key] = envelope
		}
	}
	event.Enrich.Evaluations[0].Data, err = payload.JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	item = projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, true)
	if item.Status != "failed" || item.ChangedFieldCount != 0 || item.Unavailable {
		t.Fatalf("failed=%+v", item)
	}
	item = projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], eventsource.Release{}, true)
	if !item.Unavailable || item.Diagnostics[0].Code != "enrich_history_unavailable" {
		t.Fatalf("missing release=%+v", item)
	}
}

func TestEventEnrichWindowFlatFieldsPreserveValues(t *testing.T) {
	original := map[string]any{"title": "CPU", "bk_tenant_id": "tenant-a", "labels": map[string]any{"owner": "label", "model_name": "trusted"}, "extra_data": map[string]any{"item": "", "object": false, "strategy_id": 0, "owner": []any{"alice"}, "__kac_custom_fields": []any{"owner", "bk_tenant_id", "model_name"}, "bk_tenant_id": "foreign", "model_name": "untrusted"}}
	flat := eventWindowAlarm(original, "critical")
	if flat["item"] != "" || flat["object"] != false || flat["strategy_id"] != 0 || flat["bk_tenant_id"] != "tenant-a" || flat["level"] != "fatal" || flat["model_name"] != "trusted" || flat["owner"].([]any)[0] != "alice" {
		t.Fatalf("flat=%+v", flat)
	}
	if _, ok := flat["__kac_custom_fields"]; ok {
		t.Fatal("internal catalog leaked")
	}
}

func TestEventEnrichWindowHTTPPagingScopeAndDetail(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	event.RelatedAlertIDs = nil
	event.ReceivedAt = time.Date(2026, 10, 13, 0, 15, 0, 0, time.UTC)
	// 同一 Event 的第二等级仍在同页，无需创建第二个 Alert。
	event.Evaluations = append(event.Evaluations, domain.EventEvaluation{Severity: "warning", Action: domain.EventActionTriggered})
	second := event.Enrich.Evaluations[0]
	second.Severity = "warning"
	event.Enrich.Evaluations = append(event.Enrich.Evaluations, second)
	stub := &eventWindowStub{enrichRecordStub: enrichRecordStub{event: event, release: release}, page: store.EventPage{Events: []store.StoredEvent{{Event: event}}, NextCursor: "next-event"}}
	api := API{EnrichRecords: NewEnrichRecords(stub, stub)}
	body := `{"bk_tenant_id":"tenant-a","event_source_id":"host","received_from":"2026-10-13T00:00:00Z","received_to":"2026-10-13T01:00:00Z","limit":1}`
	r := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(body))
	w := httptest.NewRecorder()
	api.queryEventEnrichWindow(w, r)
	var result struct {
		Items []eventWindowItem `json:"items"`
		Next  string            `json:"next"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || len(result.Items) != 2 || result.Next != "next-event" || stub.tenant != "tenant-a" || stub.source != "host" || stub.query.Page.Limit != 1 {
		t.Fatalf("response=%s", w.Body)
	}
	for _, test := range []struct {
		replacement string
		code        int
	}{{"host", 200}, {"foreign", 404}} {
		detail := strings.Replace(body, `"limit":1`, `"severity":"warning"`, 1)
		detail = strings.Replace(detail, `"event_source_id":"host"`, `"event_source_id":"`+test.replacement+`"`, 1)
		r = httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(detail))
		r.SetPathValue("id", event.EventID)
		w = httptest.NewRecorder()
		api.queryEventEnrichDetail(w, r)
		if w.Code != test.code {
			t.Fatalf("detail=%d %s", w.Code, w.Body)
		}
	}
	r = httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(strings.Replace(body, "01:00:00", "02:00:00", 1)))
	w = httptest.NewRecorder()
	api.queryEventEnrichWindow(w, r)
	if w.Code != 400 {
		t.Fatal("unbounded window accepted")
	}
	filtered := strings.Replace(body, `"limit":1`, `"limit":1,"filters":{"name":{"value":"no match","match":"equals"}}`, 1)
	r = httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(filtered))
	w = httptest.NewRecorder()
	api.queryEventEnrichWindow(w, r)
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 || result.Next != "next-event" {
		t.Fatal("empty filtered page lost cursor")
	}
}

func TestEventWindowRoutesRequireManagementJWT(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	event.ReceivedAt = time.Date(2026, 10, 13, 0, 15, 0, 0, time.UTC)
	stub := &eventWindowStub{enrichRecordStub: enrichRecordStub{event: event, release: release}, page: store.EventPage{Events: []store.StoredEvent{{Event: event}}}}
	handler := (&API{EnrichRecords: NewEnrichRecords(stub, stub), Config: config.DispatchConfig{JWT: config.JWTConfig{SecretKey: "window-test"}, WorkerToken: "worker"}}).Handler()
	for _, route := range []string{"/api/v1/events/enrich-records/query", "/api/v1/events/event-1/enrich-record"} {
		for _, token := range []string{"", testJWT(t, "window-test")} {
			body := `{"bk_tenant_id":"tenant-a","event_source_id":"host","received_from":"2026-10-13T00:00:00Z","received_to":"2026-10-13T01:00:00Z","severity":"fatal"}`
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, route, strings.NewReader(body))
			request.Header.Set("Internal-Token", token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			want := 200
			if token == "" {
				want = 401
			}
			if response.Code != want {
				t.Fatalf("route=%s code=%d body=%s", route, response.Code, response.Body)
			}
		}
	}
}

func TestEventWindowIncludesBuiltinChangesAndRuleProvenance(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	payload, err := enrich.DecodePayload(event.Enrich.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	patch, err := domain.NewEnrichPatch("$.labels.model_name", "host")
	if err != nil {
		t.Fatal(err)
	}
	payload.Processors = append([]enrich.ProcessorEntry{{"resource": {Status: domain.EnrichStatusSucceeded, Patches: []domain.EnrichPatch{patch}}}}, payload.Processors...)
	event.Enrich.Evaluations[0].Data, err = payload.JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	item := projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, true)
	if item.ChangedFieldCount != 2 {
		t.Fatalf("missing builtin diff: %+v", item)
	}
	found := false
	for _, change := range item.FieldChanges {
		if change.Field == "model_name" {
			found = true
			if change.BeforePresent || string(change.After) != `"host"` || len(change.Measures) != 1 || change.Measures[0] != "resource" {
				t.Fatalf("change=%+v", change)
			}
		}
	}
	if !found {
		t.Fatal("resource field change missing")
	}
}

func TestEventWindowStepsKeepEveryCommittedChangeInOrder(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	patch := func(path string, value any, rule, operation string) domain.EnrichPatch {
		t.Helper()
		p, err := domain.NewEnrichPatch(path, value)
		if err != nil {
			t.Fatal(err)
		}
		p.RuleID, p.OperationID = rule, operation
		return p
	}
	payload := enrich.Payload{Processors: []enrich.ProcessorEntry{
		{"resource": {Status: domain.EnrichStatusSucceeded, Patches: []domain.EnrichPatch{patch("$.title", "A", "", "")}}},
		{"fields": {Status: domain.EnrichStatusSucceeded, Trace: []enrich.Trace{{RuleID: "title", Status: domain.EnrichStatusSucceeded}}, Patches: []domain.EnrichPatch{
			patch("$.title", "B", "title", "first"),
			patch("$.extra_data.owner", "alice", "title", "owner"),
			patch("$.title", "C", "title", "second"),
			patch("$.title", "A", "title", "restore"),
			patch("$.title", "A", "title", "same"),
		}}},
		{"metric": {Status: domain.EnrichStatusFailed, Patches: []domain.EnrichPatch{patch("$.title", "uncommitted", "", "")}}},
		{"source": {Status: domain.EnrichStatusSkipped, Patches: []domain.EnrichPatch{}}},
	}}
	var err error
	event.Enrich.Evaluations[0].Data, err = payload.JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	item := projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, true)
	if item.Unavailable || item.Steps == nil || item.Original["name"] != "before" || item.Alarm["name"] != "A" {
		t.Fatalf("invalid facts: %+v", item)
	}
	steps := *item.Steps
	if len(steps) != 4 || steps[0].Processor != "resource" || len(steps[0].Changes) != 1 || steps[1].Rules["title"] != "标题规范化" {
		t.Fatalf("steps=%+v", steps)
	}
	changes := steps[1].Changes
	if len(changes) != 4 {
		t.Fatalf("lost intermediate changes: %+v", changes)
	}
	want := []struct{ field, before, after, operation string }{
		{"name", `"A"`, `"B"`, "first"},
		{"owner", "null", `"alice"`, "owner"},
		{"name", `"B"`, `"C"`, "second"},
		{"name", `"C"`, `"A"`, "restore"},
	}
	for i, expected := range want {
		got := changes[i]
		if got.Field != expected.field || string(got.Before) != expected.before || string(got.After) != expected.after || got.RuleID != "title" || got.OperationID != expected.operation {
			t.Fatalf("change %d=%+v", i, got)
		}
	}
	if changes[0].Display != "告警名称" || changes[1].BeforePresent || len(steps[2].Changes) != 0 || len(steps[3].Changes) != 0 {
		t.Fatalf("invalid labels/presence/failed stages: %+v", steps)
	}
	summary := projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, false)
	if summary.Steps != nil || summary.Alarm["name"] != item.Alarm["name"] || event.Title != "before" {
		t.Fatal("summary leaked steps or query changed facts")
	}
}

func TestEventWindowStepsKeepLabelsWhenFinalValueReturnsToOriginal(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	payload, err := enrich.DecodePayload(event.Enrich.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	envelope := payload.Processors[0]["fields"]
	envelope.Patches[1].Value = json.RawMessage(`"before"`)
	payload.Processors[0]["fields"] = envelope
	event.Enrich.Evaluations[0].Data, err = payload.JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	item := projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, true)
	if item.Steps == nil || len(item.FieldChanges) != 0 || len((*item.Steps)[0].Changes) != 2 || (*item.Steps)[0].Changes[1].Display != "告警名称" {
		t.Fatalf("round trip lost steps or frozen label: %+v", item)
	}
}

func TestEventWindowStepsKeepFalsyValuesAndInternalRules(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	release.Spec.Enrich.Processors[0].Config["rules"] = []any{map[string]any{"id": "kac_custom_catalog", "name": ""}}
	encoded, _ := json.Marshal(release.Spec.Enrich)
	digest := sha256.Sum256(encoded)
	event.EnrichConfigDigest = hex.EncodeToString(digest[:])
	patches := []domain.EnrichPatch{}
	for _, value := range []any{nil, "", 0, false} {
		patch, err := domain.NewEnrichPatch("$.extra_data.value", value)
		if err != nil {
			t.Fatal(err)
		}
		patch.RuleID = "kac_custom_catalog"
		patches = append(patches, patch)
	}
	var err error
	event.Enrich.Evaluations[0].Data, err = (enrich.Payload{Processors: []enrich.ProcessorEntry{{"fields": {Status: domain.EnrichStatusSucceeded, Patches: patches}}}}).JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	item := projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, true)
	if item.Steps == nil || len((*item.Steps)[0].Changes) != 4 {
		t.Fatalf("complete facts missing: %+v", item)
	}
	changes := (*item.Steps)[0].Changes
	for i, want := range []string{"null", `""`, "0", "false"} {
		if string(changes[i].After) != want || !changes[i].AfterPresent || changes[i].RuleID != "kac_custom_catalog" {
			t.Fatalf("change %d=%+v", i, changes[i])
		}
	}
	if changes[0].BeforePresent || !changes[1].BeforePresent {
		t.Fatal("missing and null were conflated")
	}
}

func TestEventWindowStepsUnavailableWithoutCommittedBuiltinEvidence(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	payload, err := enrich.DecodePayload(event.Enrich.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	payload.Processors = append([]enrich.ProcessorEntry{{"resource": {Status: domain.EnrichStatusSucceeded, Value: domain.JSONObject{"model_name": json.RawMessage(`"host"`)}}}}, payload.Processors...)
	event.Enrich.Evaluations[0].Data, err = payload.JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	item := projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, true)
	if !item.Unavailable || item.Steps != nil {
		t.Fatal("missing steps were presented as complete history")
	}
}

func TestEventWindowStepsRetainSuccessfulStageWithoutChanges(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	payload, err := enrich.DecodePayload(event.Enrich.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	payload.Processors = append([]enrich.ProcessorEntry{{"resource": {Status: domain.EnrichStatusSucceeded, Value: domain.JSONObject{}, Patches: []domain.EnrichPatch{}}}}, payload.Processors...)
	event.Enrich.Evaluations[0].Data, err = payload.JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	item := projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, true)
	if item.Unavailable || item.Steps == nil || len(*item.Steps) != 2 || len((*item.Steps)[0].Changes) != 0 {
		t.Fatalf("empty successful stage was lost: %+v", item)
	}
}

func TestEventWindowStepsRetainCustomAssignmentBeforeCatalogFreeze(t *testing.T) {
	_, event, release := enrichRecordFixture(t)
	event.Labels = domain.DimensionMap{"owner": domain.NewStringScalar("label-owner")}
	event.ExtraData = domain.JSONObject{"owner": json.RawMessage(`"original-owner"`)}
	p1, err := domain.NewEnrichPatch("$.extra_data.owner", "custom-owner")
	if err != nil {
		t.Fatal(err)
	}
	p1.RuleID = "title"
	p2, err := domain.NewEnrichPatch("$.extra_data.__kac_custom_fields", []string{"owner"})
	if err != nil {
		t.Fatal(err)
	}
	p2.RuleID = "kac_custom_catalog"
	event.Enrich.Evaluations[0].Data, err = (enrich.Payload{Processors: []enrich.ProcessorEntry{{"fields": {Status: domain.EnrichStatusSucceeded, Patches: []domain.EnrichPatch{p1, p2}, Trace: []enrich.Trace{{RuleID: "title", Status: domain.EnrichStatusSucceeded}}}}}}).JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	item := projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, true)
	if item.Unavailable || item.Steps == nil || item.Alarm["owner"] != "custom-owner" {
		t.Fatalf("item=%+v", item)
	}
	changes := (*item.Steps)[0].Changes
	if len(changes) != 2 || changes[0].RuleID != "title" || changes[0].Field != "owner" || string(changes[0].Before) != `"original-owner"` || string(changes[0].After) != `"custom-owner"` {
		t.Fatalf("catalog hid a committed user assignment: %+v", changes)
	}
}
