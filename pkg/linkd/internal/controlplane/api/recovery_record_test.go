package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

func TestRecoveryFieldsUsesFrozenEventAndCommittedPatches(t *testing.T) {
	a, e, r := enrichRecordFixture(t)
	e.Evaluations[0].Action = domain.EventActionResolved
	e.Content = "当次恢复正文"
	if err := json.Unmarshal([]byte(`{"source_id":"kac-source","source_name":"当时告警源"}`), &e.Labels); err != nil {
		t.Fatal(err)
	}
	e.SourceRawData = domain.JSONObject{"private": json.RawMessage(`"raw secret"`)}
	a.Title, a.Content = "当前告警标题", "当前告警正文"
	patches := []domain.EnrichPatch{}
	for field, value := range map[string]any{"$.title": "丰富后的恢复标题", "$.extra_data.zero": 0, "$.extra_data.enabled": false, "$.extra_data.__kac_custom_fields": []string{"zero", "enabled"}} {
		patch, err := domain.NewEnrichPatch(field, value)
		if err != nil {
			t.Fatal(err)
		}
		patch.RuleID = "title"
		patches = append(patches, patch)
	}
	payload, err := (enrich.Payload{Processors: []enrich.ProcessorEntry{{"fields": {Status: domain.EnrichStatusSucceeded, Patches: patches, Trace: []enrich.Trace{{RuleID: "title", Status: domain.EnrichStatusSucceeded}}}}}}).JSONObject()
	if err != nil {
		t.Fatal(err)
	}
	e.Enrich.Evaluations[0].Data = payload
	fields, err := recoveryFields(a, e, r)
	if err != nil {
		t.Fatal(err)
	}
	if fields["name"] != "丰富后的恢复标题" || fields["content"] != "当次恢复正文" || fields["enabled"] != false || fields["action"] != "resolved" || fields["source_name"] != "当时告警源" || fields["source_id"] != "kac-source" || e.Title != "before" {
		t.Fatalf("wrong fields: %+v", fields)
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "raw secret") || strings.Contains(string(encoded), "source_raw_data") {
		t.Fatal("raw event leaked")
	}
}

func TestRecoveryQueryRejectsForeignEventWrongSeverityAndTrigger(t *testing.T) {
	for _, kind := range []string{"ok", "tenant", "unrelated", "severity", "trigger", "version", "busy"} {
		t.Run(kind, func(t *testing.T) {
			a, e, r := enrichRecordFixture(t)
			e.Evaluations[0].Action = domain.EventActionResolved
			stub := &enrichRecordStub{alert: a, event: e, release: r}
			api := API{EnrichRecords: NewEnrichRecords(stub, stub)}
			switch kind {
			case "tenant":
				stub.event.BKTenantID = "foreign"
			case "unrelated":
				stub.event.RelatedAlertIDs = []string{"other"}
			case "severity":
				stub.alert.Severity = "warning"
			case "trigger":
				stub.event.Evaluations[0].Action = domain.EventActionTriggered
			case "version":
				stub.release.Version++
			case "busy":
				api.EnrichRecords.slots <- struct{}{}
				api.EnrichRecords.slots <- struct{}{}
			}
			request := httptest.NewRequestWithContext(context.Background(), "POST", "/api/v1/alerts/alert-1/recovery-record", strings.NewReader(`{"bk_tenant_id":"tenant-a","event_id":"event-1","severity":"fatal"}`))
			request.SetPathValue("id", "alert-1")
			response := httptest.NewRecorder()
			api.queryRecoveryRecord(response, request)
			if kind == "ok" && response.Code != 200 {
				t.Fatal(response.Body.String())
			}
			if kind != "ok" && response.Code == 200 {
				t.Fatal("unrelated recovery accepted")
			}
		})
	}
}
