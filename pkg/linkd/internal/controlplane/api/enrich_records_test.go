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
	"errors"
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

func enrichRecordFixture(t *testing.T) (domain.Alert, domain.Event, eventsource.Release) {
	t.Helper()
	alert := domain.Alert{AlertID: "alert-1", BKTenantID: "tenant-a", EventSourceID: "host", Severity: "fatal"}
	release := eventsource.Release{ID: "host", Version: 2, Spec: config.EventSource{EventSourceID: "host", RelatedTenantID: "tenant-a", Version: 2, Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "fields", Config: map[string]any{"rules": []any{map[string]any{"id": "title", "name": "标题规范化"}}, "field_labels": map[string]string{"$.title": "告警名称"}}}}}}}
	p1, err := domain.NewEnrichPatch("$.title", "intermediate")
	if err != nil {
		t.Fatal(err)
	}
	p1.RuleID = "title"
	p2 := p1
	p2.Value = json.RawMessage(`"<script>text only</script>"`)
	payload, err := (enrich.Payload{Processors: []enrich.ProcessorEntry{{"fields": {Status: domain.EnrichStatusSucceeded, Patches: []domain.EnrichPatch{p1, p2}, Trace: []enrich.Trace{{RuleID: "title", Status: domain.EnrichStatusSucceeded}}}}}}).JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 13, 0, 15, 20, 0, time.UTC)
	encoded, _ := json.Marshal(release.Spec.Enrich)
	digest := sha256.Sum256(encoded)
	event := domain.Event{EventID: "event-1", BKTenantID: "tenant-a", EventSourceID: "host", EventSourceVersion: 2, Title: "before", RelatedAlertIDs: []string{"alert-1"}, Evaluations: []domain.EventEvaluation{{Severity: "fatal", Action: domain.EventActionTriggered}}, EventEnrichment: domain.EventEnrichment{EnrichStatus: domain.EnrichStatusSucceeded, EnrichedAt: &at, EnrichConfigDigest: hex.EncodeToString(digest[:]), Enrich: domain.EventEnrichData{Evaluations: []domain.EvaluationEnrich{{Severity: "fatal", Status: domain.EnrichStatusSucceeded, Data: payload}}}}}
	return alert, event, release
}

func TestEnrichRecordCommittedDiffAndFrozenNames(t *testing.T) {
	alert, event, release := enrichRecordFixture(t)
	result, err := projectEnrichRecord(alert, event, release, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.EnrichmentCount != 1 || result.ChangedFieldCount != 1 || result.Enrichments[0].Name != "标题规范化" {
		t.Fatalf("invalid facts: %+v", result)
	}
	change := result.FieldChanges[0]
	var after string
	if err := json.Unmarshal(change.After, &after); err != nil {
		t.Fatal(err)
	}
	if change.Display != "告警名称" || string(change.Before) != `"before"` || after != "<script>text only</script>" || !change.BeforePresent || len(change.Measures) != 1 {
		t.Fatalf("invalid change: %+v", change)
	}
	if event.Title != "before" {
		t.Fatal("query modified original event")
	}
	summary, err := projectEnrichRecord(alert, event, release, false)
	if err != nil || summary.ChangedFieldCount != 1 || len(summary.FieldChanges) != 0 {
		t.Fatal("summary leaked field values")
	}
}

func TestEnrichRecordUsesExecutedSourceVersion(t *testing.T) {
	alert, event, release := enrichRecordFixture(t)
	event.EventSourceVersion = 1
	event.EnrichSourceVersion = release.Version
	result, err := projectEnrichRecord(alert, event, release, true)
	if err != nil || result.EnrichmentCount != 1 || result.Enrichments[0].Name != "标题规范化" {
		t.Fatalf("executed source metadata unavailable: %+v %v", result, err)
	}
	release.Version = event.EventSourceVersion
	if _, err := projectEnrichRecord(alert, event, release, true); err == nil {
		t.Fatal("intake version was used for newer enrichment")
	}
}

func TestEnrichRecordPreservesReusedRuleAfterConfigurationChanges(t *testing.T) {
	alert, event, release := enrichRecordFixture(t)
	payload, err := enrich.DecodePayload(event.Enrich.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	step := payload.Processors[0]["fields"]
	step.ReusedFromAlertID = "opening-alert"
	step.Trace[0].RuleID = "previous-title-rule"
	for index := range step.Patches {
		step.Patches[index].RuleID = "previous-title-rule"
	}
	payload.Processors[0]["fields"] = step
	event.Enrich.Evaluations[0].Data, err = payload.JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	result, err := projectEnrichRecord(alert, event, release, true)
	if err != nil || len(result.Enrichments) != 1 || result.Enrichments[0].ReusedFromAlertID != "opening-alert" || result.Enrichments[0].Name != "previous-title-rule" {
		t.Fatalf("reused rule was lost or relabeled as current configuration: %+v %v", result, err)
	}
	item := projectEventWindowItem(store.StoredEvent{Event: event}, event.Evaluations[0], release, true)
	if item.Unavailable || item.Steps == nil || len(*item.Steps) != 1 {
		t.Fatalf("reused step unavailable: %+v", item)
	}
	savedStep := (*item.Steps)[0]
	if savedStep.ReusedFromAlertID != "opening-alert" || savedStep.Rules["previous-title-rule"] != "previous-title-rule" || len(savedStep.Changes) == 0 {
		t.Fatalf("reused provenance or changes missing: %+v", savedStep)
	}
}

func TestEnrichRecordRejectsWrongVersionDigestTenantAndSeverity(t *testing.T) {
	for _, kind := range []string{"version", "digest", "tenant", "severity", "time"} {
		t.Run(kind, func(t *testing.T) {
			a, e, r := enrichRecordFixture(t)
			switch kind {
			case "version":
				r.Version++
			case "digest":
				e.EnrichConfigDigest = "bad"
			case "tenant":
				r.Spec.RelatedTenantID = "tenant-b"
			case "severity":
				a.Severity = "warning"
			case "time":
				e.EnrichedAt = nil
			}
			if _, err := projectEnrichRecord(a, e, r, true); err == nil {
				t.Fatal("accepted mismatched frozen fact")
			}
		})
	}
}

func TestEnrichRecordDistinguishesUnsetNullEmptyZeroFalseAndNetNoChange(t *testing.T) {
	a, e, r := enrichRecordFixture(t)
	patches := []domain.EnrichPatch{}
	for key, value := range map[string]any{"zero": 0, "false": false, "empty": "", "null": nil} {
		p, err := domain.NewEnrichPatch("$.extra_data."+key, value)
		if err != nil {
			t.Fatal(err)
		}
		p.RuleID = "title"
		patches = append(patches, p)
	}
	// 两次改名最终还原，不应作为生效字段计数。
	for _, value := range []string{"temp", "before"} {
		p, _ := domain.NewEnrichPatch("$.title", value)
		p.RuleID = "title"
		patches = append(patches, p)
	}
	payload, err := (enrich.Payload{Processors: []enrich.ProcessorEntry{{"fields": {Status: domain.EnrichStatusSucceeded, Patches: patches, Trace: []enrich.Trace{{RuleID: "title", Status: domain.EnrichStatusSucceeded}}}}}}).JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	e.Enrich.Evaluations[0].Data = payload
	result, err := projectEnrichRecord(a, e, r, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.ChangedFieldCount != 4 {
		t.Fatalf("net changes=%d", result.ChangedFieldCount)
	}
	for _, change := range result.FieldChanges {
		if change.BeforePresent || !change.AfterPresent {
			t.Fatal("lost value presence")
		}
	}
}

func TestEnrichRecordFailedAndSkippedRulesHaveNoCommittedChanges(t *testing.T) {
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusFailed, domain.EnrichStatusSkipped} {
		a, e, r := enrichRecordFixture(t)
		payload, err := (enrich.Payload{Processors: []enrich.ProcessorEntry{{"fields": {Status: status, Patches: []domain.EnrichPatch{}, Trace: []enrich.Trace{{RuleID: "title", Status: status, Code: "condition_not_matched"}}}}}}).JSONObject()
		if err != nil {
			t.Fatal(err)
		}
		e.Enrich.Evaluations[0].Data = payload
		result, err := projectEnrichRecord(a, e, r, true)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != status || result.EnrichmentCount != 1 || result.ChangedFieldCount != 0 {
			t.Fatalf("invalid failed/skip facts: %+v", result)
		}
	}
}

func TestEnrichRecordRejectsLegacySuccessWithoutCommittedPatchEvidence(t *testing.T) {
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusSucceeded, domain.EnrichStatusPartial} {
		t.Run(string(status), func(t *testing.T) {
			a, e, r := enrichRecordFixture(t)
			payload, err := (enrich.Payload{Processors: []enrich.ProcessorEntry{{"fields": {Status: status, Value: domain.JSONObject{"title": json.RawMessage(`"historical"`)}, Trace: []enrich.Trace{{RuleID: "title", Status: status}}}}}}).JSONObject()
			if err != nil {
				t.Fatal(err)
			}
			e.Enrich.Evaluations[0].Data = payload
			if _, err := projectEnrichRecord(a, e, r, true); err == nil {
				t.Fatal("invented empty field comparison from historical success")
			}
		})
	}
}

type enrichRecordStub struct {
	alert   domain.Alert
	event   domain.Event
	release eventsource.Release
	reads   int
	err     error
}

func (s *enrichRecordStub) GetAlert(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	if err := ctx.Err(); err != nil {
		return store.StoredAlert{}, err
	}
	return store.StoredAlert{Alert: s.alert}, nil
}

func (s *enrichRecordStub) GetEvent(ctx context.Context, tenant, id string) (store.StoredEvent, error) {
	s.reads++
	return store.StoredEvent{Event: s.event}, s.err
}

func (s *enrichRecordStub) GetRelease(ctx context.Context, id string, version int64) (eventsource.Release, error) {
	return s.release, nil
}

func TestEnrichRecordHTTPBoundsIsolationAndMissing(t *testing.T) {
	for _, kind := range []string{"ok", "foreign-event", "unrelated", "missing", "oversize", "busy", "canceled"} {
		t.Run(kind, func(t *testing.T) {
			a, e, release := enrichRecordFixture(t)
			stub := &enrichRecordStub{alert: a, event: e, release: release}
			api := API{EnrichRecords: NewEnrichRecords(stub, stub)}
			body := `{"bk_tenant_id":"tenant-a","event_ids":["event-1","event-1"],"detail":true}`
			switch kind {
			case "foreign-event":
				stub.event.BKTenantID = "tenant-b"
			case "unrelated":
				stub.event.RelatedAlertIDs = []string{"other"}
			case "missing":
				stub.err = errors.New("unavailable")
			case "oversize":
				body = `{"bk_tenant_id":"tenant-a","event_ids":["` + strings.Repeat("x", 161) + `"]}`
			case "busy":
				api.EnrichRecords.slots <- struct{}{}
				api.EnrichRecords.slots <- struct{}{}
			}
			r := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/alerts/alert-1/enrich-records", strings.NewReader(body))
			r.SetPathValue("id", "alert-1")
			if kind == "canceled" {
				ctx, cancel := context.WithCancel(r.Context())
				cancel()
				r = r.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			api.queryEnrichRecords(w, r)
			if kind == "oversize" {
				if w.Code != 400 {
					t.Fatal(w.Code)
				}
				return
			}
			if kind == "busy" {
				if w.Code != 429 {
					t.Fatal(w.Code)
				}
				return
			}
			if kind == "canceled" {
				if w.Code != 502 {
					t.Fatal(w.Code)
				}
				return
			}
			if w.Code != 200 || stub.reads != 1 {
				t.Fatalf("code=%d reads=%d", w.Code, stub.reads)
			}
			var response struct {
				Items       []enrichRecord `json:"items"`
				Unavailable []string       `json:"unavailable"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if kind == "ok" {
				if len(response.Items) != 1 || len(response.Unavailable) != 0 {
					t.Fatal("valid facts missing")
				}
			} else if len(response.Items) != 0 || len(response.Unavailable) != 1 {
				t.Fatal("unrelated fact leaked")
			}
		})
	}
}
