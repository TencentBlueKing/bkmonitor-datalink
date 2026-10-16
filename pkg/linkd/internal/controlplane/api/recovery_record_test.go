package api

import (
	"bytes"
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
	metricQuery, err := domain.NewEnrichPatch("$.extra_data.metric_query_params", map[string]any{"expression": "A", "query_configs": []any{map[string]any{"bk_biz_id": 10}}})
	if err != nil {
		t.Fatal(err)
	}
	patches = append(patches, metricQuery)
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
	if fields["metric_query_params"] != `{"expression":"A","query_configs":[{"bk_biz_id":10}]}` {
		t.Fatalf("metric_query_params = %#v", fields["metric_query_params"])
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "raw secret") || strings.Contains(string(encoded), "source_raw_data") {
		t.Fatal("raw event leaked")
	}
}

func TestRecoveryFieldsProjectFrozenStrategyWithoutLegacyQuery(t *testing.T) {
	for _, action := range []domain.EventAction{domain.EventActionResolved, domain.EventActionClosed} {
		t.Run(string(action), func(t *testing.T) {
			a, e, r := enrichRecordFixture(t)
			a.EventSourceID, e.EventSourceID, r.ID, r.Spec.EventSourceID = "built_in_bk", "built_in_bk", "built_in_bk", "built_in_bk"
			e.Evaluations[0].Action = action
			if err := json.Unmarshal([]byte(`{"strategy_id":123,"strategy_version":7,"bk_biz_id":2}`), &e.Labels); err != nil {
				t.Fatal(err)
			}
			// 当前 Alert 的 opening 事实不能代替这次恢复 Event 的执行版本。
			a.Labels = domain.DimensionMap{"strategy_version": domain.NewStringScalar("opening-version")}
			patches := []domain.EnrichPatch{}
			for path, value := range map[string]any{
				"$.labels.strategy_id": 123, "$.labels.strategy_version": 7,
				"$.labels.monitor_template_id": 42, "$.labels.strategy_config_id": "config1",
				"$.labels.strategy_kind": "Strategy", "$.labels.is_default": false,
				"$.extra_data.strategy_instance": map[string]any{
					"object_model_code": "cw-Host", "bk_obj_id": "host", "bk_inst_id": "host|101", "object_model_group_id": "7",
				},
				"$.extra_data.field_extra_info": map[string]any{"object": map[string]any{"url": "https://example.test/host"}},
			} {
				patch, err := domain.NewEnrichPatch(path, value)
				if err != nil {
					t.Fatal(err)
				}
				patches = append(patches, patch)
			}
			payload, err := (enrich.Payload{Processors: []enrich.ProcessorEntry{{"strategy": {Status: domain.EnrichStatusSucceeded, Patches: patches}}}}).JSONObject()
			if err != nil {
				t.Fatal(err)
			}
			e.Enrich.Evaluations[0].Data = payload
			before, _ := json.Marshal(e)
			fields, err := recoveryFields(a, e, r)
			if err != nil {
				t.Fatal(err)
			}
			extra, _ := fields["field_extra_info"].(map[string]any)
			strategy, _ := extra["strategy_name"].(map[string]any)
			want := map[string]any{
				"bk_tenant_id": "tenant-a", "strategy_set_split_record_id": int64(123), "strategy_set_split_record_version": int64(7),
				"monitor_template_id": int64(42), "strategy_config_id": "config1", "strategy_kind": "Strategy", "is_default": false,
				"strategy_instance": map[string]any{"object_model_code": "cw-Host", "bk_obj_id": "host", "bk_inst_id": "host|101", "object_model_group_id": "7"},
			}
			gotJSON, _ := json.Marshal(strategy["strategy_context"])
			wantJSON, _ := json.Marshal(want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("recovery strategy_context=%s want=%s", gotJSON, wantJSON)
			}
			if _, exists := strategy["url"]; exists {
				t.Fatal("new recovery generated a frontend URL")
			}
			if _, exists := fields["metric_query_params"]; exists {
				t.Fatal("new recovery generated legacy query parameters")
			}
			if extra["object"].(map[string]any)["url"] != "https://example.test/host" {
				t.Fatal("source field extra info lost")
			}
			// 同一恢复 Event 的只读事实必须与投影引用一致，才能进入 KAC 新查询路径。
			stub := &enrichRecordStub{alert: a, event: e, release: r}
			api := API{EnrichRecords: NewEnrichRecords(stub, stub)}
			request := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(`{"bk_tenant_id":"tenant-a","event_id":"event-1","severity":"fatal"}`))
			request.SetPathValue("id", a.AlertID)
			response := httptest.NewRecorder()
			api.queryMetricFacts(response, request)
			if response.Code != 200 {
				t.Fatalf("metric facts status=%d: %s", response.Code, response.Body.String())
			}
			var facts struct {
				EventID         string `json:"event_id"`
				StrategyID      int64  `json:"strategy_id"`
				StrategyVersion int64  `json:"strategy_version"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &facts); err != nil {
				t.Fatal(err)
			}
			if facts.EventID != e.EventID || facts.StrategyID != want["strategy_set_split_record_id"] || facts.StrategyVersion != want["strategy_set_split_record_version"] {
				t.Fatalf("recovery projection and metric facts differ: %+v", facts)
			}
			after, _ := json.Marshal(e)
			if !bytes.Equal(before, after) {
				t.Fatal("recovery projection modified the frozen event")
			}
		})
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
