// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kachook

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/custom"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/processors"
	"linkd/internal/enrich/view"
	"linkd/internal/lifecycle"
)

func TestMonitorItemAliasFromEnrichToKAC(t *testing.T) {
	alert := testAlert()
	legacy, err := CompatibilityMessage(alert, kacLevel)
	if err != nil || legacy.Item != "CPU 使用率" || legacy.MetricName != "usage" {
		t.Fatalf("legacy item=%q metric=%q err=%v", legacy.Item, legacy.MetricName, err)
	}

	reader := &monitorItemAliasReader{alias: "应用 CPU"}
	chain, err := enrich.NewChain([]enrich.Processor{processors.Strategy{}, processors.Metric{}},
		enrich.Sources{CWStrategy: reader, Metric: reader}, enrich.WithReuseOnActive("strategy", "metric"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"strategy_id":123,"strategy_version":2,"bk_biz_id":2}`), &alert.Labels); err != nil {
		t.Fatal(err)
	}
	event := domain.Event{
		EventID: alert.TriggerEventID, BKTenantID: alert.BKTenantID, EventSourceID: alert.EventSourceID,
		EventSourceVersion: alert.EventSourceVersion, Fingerprint: alert.Fingerprint, SourceEventID: alert.SourceEventID,
		Labels: alert.Labels, Dimensions: alert.Dimensions,
		Evaluations: []domain.EventEvaluation{{Severity: alert.Severity, Action: domain.EventActionTriggered}},
	}
	first, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil || first.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("opening enrich status=%s err=%v", first.Status, err)
	}
	alert.Enrich = first.Data.Evaluations[0].Data
	alert.EnrichSeverity = alert.Severity
	wantQuery := models.StrategyRevisionQuery{TenantID: alert.BKTenantID, ID: 123, Version: 2}
	if !reflect.DeepEqual(reader.strategyQueries, []models.StrategyRevisionQuery{wantQuery}) || reader.metricCalls != 1 {
		t.Fatalf("strategy queries=%+v metric calls=%d", reader.strategyQueries, reader.metricCalls)
	}

	effective, err := view.EnrichedAlert(alert)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := effective.Labels["monitor_item_alias"]; exists {
		t.Fatal("display alias leaked into the effective labels")
	}
	if _, exists := effective.ExtraData["monitor_item_alias"]; exists {
		t.Fatal("display alias leaked into the effective extra_data")
	}

	for _, tc := range []struct{ name, override, want string }{
		{name: "alias", want: "应用 CPU"},
		{name: "custom override", override: `"覆盖"`, want: "覆盖"},
		{name: "explicit empty", override: `""`},
		{name: "explicit null", override: `null`},
		{name: "explicit zero", override: `0`, want: "0"},
		{name: "explicit false", override: `false`, want: "false"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := alert.Clone()
			if tc.override != "" {
				snapshot.ExtraData["item"] = json.RawMessage(tc.override)
			}
			message, err := CompatibilityMessage(snapshot, kacLevel)
			if err != nil || message.Item != tc.want || message.MetricName != "usage" || message.StrategyName != "CPU template" {
				t.Fatalf("item=%q metric=%q strategy=%q err=%v", message.Item, message.MetricName, message.StrategyName, err)
			}
		})
	}

	reader.alias = ""
	event = event.Clone()
	event.EventID = "next-event"
	if err := json.Unmarshal([]byte(`{"strategy_id":123,"strategy_version":3,"bk_biz_id":2}`), &event.Labels); err != nil {
		t.Fatal(err)
	}
	reused, err := chain.Enrich(t.Context(), enrich.Input{Event: event, ActiveAlert: &alert})
	if err != nil || reused.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("reused enrich status=%s err=%v", reused.Status, err)
	}
	snapshot := alert.Clone()
	snapshot.Enrich = reused.Data.Evaluations[0].Data
	reusedMessage, err := CompatibilityMessage(snapshot, kacLevel)
	if err != nil || reusedMessage.Item != "应用 CPU" || reusedMessage.MetricName != "usage" ||
		len(reader.strategyQueries) != 1 || reader.metricCalls != 1 {
		t.Fatalf("reused item=%q metric=%q strategy queries=%d metric calls=%d err=%v",
			reusedMessage.Item, reusedMessage.MetricName, len(reader.strategyQueries), reader.metricCalls, err)
	}

	cleared, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil || cleared.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("fresh enrich status=%s err=%v", cleared.Status, err)
	}
	snapshot.Enrich = cleared.Data.Evaluations[0].Data
	fallback, err := CompatibilityMessage(snapshot, kacLevel)
	nextQuery := wantQuery
	nextQuery.Version = 3
	if !reflect.DeepEqual(reader.strategyQueries, []models.StrategyRevisionQuery{wantQuery, nextQuery}) || reader.metricCalls != 2 {
		t.Fatalf("strategy queries=%+v metric calls=%d", reader.strategyQueries, reader.metricCalls)
	}
	if err != nil || fallback.Item != "CPU 使用率" || fallback.MetricName != "usage" {
		t.Fatalf("cleared item=%q metric=%q err=%v", fallback.Item, fallback.MetricName, err)
	}
}

type monitorItemAliasReader struct {
	alias           string
	strategyQueries []models.StrategyRevisionQuery
	metricCalls     int
}

func (reader *monitorItemAliasReader) GetByStrategyID(_ context.Context, query models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
	reader.strategyQueries = append(reader.strategyQueries, query)
	return models.CWStrategy{MonitorTemplateName: "CPU template", Spec: models.CWStrategySpec{
		Name: "CPU 使用率", MonitorItemAlias: reader.alias,
		StrategyItem: &models.CWStrategyItem{QueryConfigs: []models.StrategyQueryConfig{
			{ResultTableID: "system.cpu", MetricField: "usage"},
		}},
	}}, true, nil
}

func (reader *monitorItemAliasReader) FindMetric(context.Context, models.MetricQuery) (models.MetricMetadata, bool, error) {
	reader.metricCalls++
	return models.MetricMetadata{}, false, nil
}

func TestKACCustomCatalogRuleExecutesAndOverridesSourceDirectory(t *testing.T) {
	program, err := custom.Compile("fields", map[string]any{"rules": []any{map[string]any{
		"id": "kac_custom_catalog", "operations": []any{map[string]any{
			"id": "freeze_catalog", "type": "assign", "assignments": []any{map[string]any{
				"target": "$.extra_data.__kac_custom_fields", "value": map[string]any{"literal": []any{"owner"}},
			}},
		}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	alert := testAlert()
	alert.ExtraData = jsonObject(`{"owner":{"names":["alice"],"active":false},"__kac_custom_fields":["status"]}`)
	document, err := domain.AlertDocument(alert)
	if err != nil {
		t.Fatal(err)
	}
	result, err := program.Execute(t.Context(), document, document, alert.BKTenantID, custom.Sources{})
	if err != nil || result.Status != domain.EnrichStatusSucceeded {
		t.Fatal("catalog rule failed", result, err)
	}
	patches, _ := json.Marshal(result.Patches)
	alert.Enrich = domain.JSONObject{"processors": json.RawMessage(`[{"fields":{"status":"succeeded","patches":` + string(patches) + `}}]`)}
	payload, err := CompatibilityPayload(alert, kacLevel)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if json.Unmarshal(payload, &fields) != nil || fields["owner"].(map[string]any)["active"] != false {
		t.Fatal("catalog or custom JSON output lost", string(payload))
	}
	if _, found := fields["status"]; found {
		t.Fatal("source directory overrode published catalog")
	}
}

func TestConvertMessageMapsAlertAndEnrichToKACAlarm(t *testing.T) {
	input := lifecycle.FinalHookInput{
		Cause:   lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-1"},
		Alert:   testAlert(),
		Outcome: lifecycle.OutcomeAlertCreated,
	}
	message, err := convertMessage(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(message.AlarmID, identityPrefix) {
		t.Fatalf("alarm_id=%q", message.AlarmID)
	}
	if _, err := uuid.Parse(strings.TrimPrefix(message.AlarmID, identityPrefix)); err != nil {
		t.Fatalf("alarm_id UUID=%q err=%v", message.AlarmID, err)
	}
	if message.EventID != "linkd-alert-1" || message.SourceName != "鲸眼监控" {
		t.Fatalf("identity/source=%+v", message)
	}
	if message.Action != "firing" || message.Level != "warning" || message.AlarmTime != "2026-09-01 08:00:00" {
		t.Fatalf("lifecycle fields=%+v", message)
	}
	if message.Name != "CPU 使用率过高" || message.Content != "CPU reached 92.5%" || message.Object != "host-101" {
		t.Fatalf("display fields=%+v", message)
	}
	if message.StrategyID != "7" || message.BKBizID != "2" || message.BKCloudID != "0" || message.MetricName != "usage" {
		t.Fatalf("enrich fields=%+v", message)
	}
	if message.LogThemeID != 0 || message.APMAppID != 0 || message.LogThemeName != "" || message.BCSClusterID != "" {
		t.Fatalf("extension defaults=%+v", message)
	}
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, exists := fields["metric_query_params"]; exists {
		t.Fatal("historical query parameters leaked into KAC output")
	}
	if message.CloseTime != nil || message.CloseReason != nil {
		t.Fatalf("active close fields=%+v", message)
	}
}

func TestCompatibilityMessageOmitsHistoricalMetricQueryParams(t *testing.T) {
	for _, query := range []string{`{"expression":"A"}`, `"invalid"`, `[]`, `true`, `null`} {
		t.Run(query, func(t *testing.T) {
			alert := testAlert()
			alert.Enrich["processors"] = json.RawMessage(strings.ReplaceAll(
				string(alert.Enrich["processors"]), `"metric_query_params":{"expression":"A"}`, `"metric_query_params":`+query,
			))
			before := alert.Clone()
			message, err := CompatibilityMessage(alert, kacLevel)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := mappedPayload(message, alert, nil)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			if _, exists := fields["metric_query_params"]; exists {
				t.Fatal("historical query parameters leaked into native KAC output")
			}
			if !reflect.DeepEqual(alert, before) {
				t.Fatal("projection modified stored alert")
			}
		})
	}
}

func TestNewBuiltinWithoutQueryParametersRetainsExecutionReference(t *testing.T) {
	t.Parallel()
	alert := testAlert()
	alert.Enrich["processors"] = json.RawMessage(strings.ReplaceAll(
		string(alert.Enrich["processors"]), `"metric_query_params":{"expression":"A"},`, "",
	))
	alert.Enrich["processors"] = json.RawMessage(strings.ReplaceAll(
		string(alert.Enrich["processors"]), `"url":"/strategy/7",`, `"strategy_kind":"Strategy","is_default":true,`,
	))
	before := alert.Clone()
	message, err := CompatibilityMessage(alert, kacLevel)
	if err != nil {
		t.Fatal(err)
	}
	if message.AlarmID != alert.AlertID || message.EventID != alert.TriggerEventID || message.MetricName != "usage" {
		t.Fatalf("metric fields or alert identity changed: %+v", message)
	}
	encoded, err := json.Marshal(message.FieldExtraInfo)
	if err != nil {
		t.Fatal(err)
	}
	var extra struct {
		StrategyName struct {
			Context struct {
				Tenant   string `json:"bk_tenant_id"`
				ID       int64  `json:"strategy_set_split_record_id"`
				Revision int64  `json:"strategy_set_split_record_version"`
			} `json:"strategy_context"`
		} `json:"strategy_name"`
	}
	if err := json.Unmarshal(encoded, &extra); err != nil {
		t.Fatal(err)
	}
	context := extra.StrategyName.Context
	if context.Tenant != "tenant-1" || context.ID != 123 || context.Revision != 1 {
		t.Fatalf("execution reference lost: %s", encoded)
	}
	if !reflect.DeepEqual(alert, before) {
		t.Fatal("query migration rewrote the saved alert")
	}
}

func TestKACPayloadOmitsAbsentContextFields(t *testing.T) {
	alert := testAlert()
	message, err := convertMessage(lifecycle.FinalHookInput{
		Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-1"},
		Alert: alert, Outcome: lifecycle.OutcomeAlertCreated,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := mappedPayload(message, alert, nil)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"log_theme_id", "log_theme_name", "log_query_string", "log_relate_info",
		"apm_app_id", "apm_app_name", "apm_app_alias", "apm_service_name",
		"apm_instance_name", "apm_interface_name", "apm_net_peer_name",
		"bcs_cluster_id", "cluster_name", "namespace", "service", "workload_kind",
		"workload_name", "pod_name", "container_name", "cloud_plat_id",
	} {
		if _, exists := fields[name]; exists {
			t.Errorf("absent context field %q was sent to KAC: %s", name, fields[name])
		}
	}
	if string(fields["bk_cloud_id"]) != `"0"` {
		t.Errorf("meaningful bk_cloud_id must be preserved: %s", fields["bk_cloud_id"])
	}
	if string(fields["metric_name"]) != `"usage"` {
		t.Errorf("metric_name=%s", fields["metric_name"])
	}
}

func TestKACPayloadPreservesPresentContextFields(t *testing.T) {
	alert := testAlert()
	message, err := convertMessage(lifecycle.FinalHookInput{
		Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-1"},
		Alert: alert, Outcome: lifecycle.OutcomeAlertCreated,
	})
	if err != nil {
		t.Fatal(err)
	}
	message.LogThemeID = 36
	message.LogThemeName = "应用日志"
	message.APMAppID = 29
	message.APMAppName = "example-app"
	message.CloudPlatformID = "cloud-1"
	payload, err := mappedPayload(message, alert, nil)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]any{
		"log_theme_id": float64(36), "log_theme_name": "应用日志",
		"apm_app_id": float64(29), "apm_app_name": "example-app", "cloud_plat_id": "cloud-1",
	} {
		if fields[key] != want {
			t.Errorf("%s=%v, want %v", key, fields[key], want)
		}
	}
}

func TestConvertMessageMapsLogEnrichToKACAlarm(t *testing.T) {
	alert := testAlert()
	alert.Enrich = jsonObject(`{"processors":[
		{"display":{"status":"succeeded","value":{"title":"日志告警","content":"匹配到【error】关键字次数 2","object":"","dimensions":[],"dimension_text":""}}},
		{"log":{"status":"succeeded","value":{"log_theme_id":36,"log_theme_name":"应用日志","log_query_string":"error","log_relate_info":"{\"host\":\"web-1\"}","cw_labels":["bk_biz_id","bk_biz_id|2","log","log|36"]}}},
		{"resource":{"status":"succeeded","value":{"bk_biz_id":2,"dynamic_group_id":[],"cw_labels":[]}}}
	]}`)
	message, err := convertMessage(lifecycle.FinalHookInput{
		Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-1"},
		Alert: alert, Outcome: lifecycle.OutcomeAlertCreated,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.LogThemeID != 36 || message.LogThemeName != "应用日志" || message.LogQueryString != "error" || message.LogRelateInfo != `{"host":"web-1"}` {
		t.Fatalf("log fields=%+v", message)
	}
	if message.BKBizID != "2" {
		t.Fatalf("bk_biz_id=%q", message.BKBizID)
	}
	wantLabels := []string{"bk_biz_id", "bk_biz_id|2", "log", "log|36"}
	if len(message.CWLabels) != len(wantLabels) {
		t.Fatalf("cw_labels=%v", message.CWLabels)
	}
	for index := range wantLabels {
		if message.CWLabels[index] != wantLabels[index] {
			t.Fatalf("cw_labels=%v", message.CWLabels)
		}
	}
}

func TestConvertMessageMapsAPMEnrichToKACAlarm(t *testing.T) {
	alert := testAlert()
	alert.Enrich = jsonObject(`{"processors":[
		{"display":{"status":"succeeded","value":{"title":"APM告警","content":"请求量异常","object":"account","dimensions":[],"dimension_text":""}}},
		{"resource":{"status":"succeeded","value":{"model_id":"cw-service_instance","model_inst_id":"29|account|instance-a","bk_inst_id":29,"bk_biz_id":10,"dynamic_group_id":[],"cw_labels":["bk_biz_id","bk_biz_id|10","apm_app_id","apm_app_id|29"]}}},
		{"apm":{"status":"succeeded","value":{"apm_app_id":29,"apm_app_name":"example-app","apm_app_alias":"Example App","apm_service_name":"account","apm_instance_name":"instance-a","apm_interface_name":"GET /account","apm_net_peer_name":"192.0.2.21:3306","model_id":"cw-service_instance","model_inst_id":"29|account|instance-a","bk_biz_id":10,"cw_labels":["bk_biz_id","bk_biz_id|10","apm_app_id","apm_app_id|29"]}}}
	]}`)
	message, err := convertMessage(lifecycle.FinalHookInput{
		Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-1"},
		Alert: alert, Outcome: lifecycle.OutcomeAlertCreated,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.APMAppID != 29 || message.APMAppName != "example-app" || message.APMAppAlias != "Example App" || message.APMServiceName != "account" || message.APMInstanceName != "instance-a" || message.APMInterfaceName != "GET /account" || message.APMNetPeerName != "192.0.2.21:3306" {
		t.Fatalf("apm fields=%+v", message)
	}
	if message.ModelID != "cw-service_instance" || message.ModelInstID != "29|account|instance-a" || message.BKBizID != "10" {
		t.Fatalf("resource fields=%+v", message)
	}
}

func TestKACAlarmIDIsStableAndRouteSafe(t *testing.T) {
	input := lifecycle.FinalHookInput{
		Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-1"},
		Alert: testAlert(), Outcome: lifecycle.OutcomeAlertCreated,
	}
	input.Alert.AlertID = "20250101000000.tenant-example.source-example.0123456789abcdef"

	first, err := convertMessage(input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := convertMessage(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.AlarmID != second.AlarmID {
		t.Fatalf("alarm_id changed across retry: first=%q second=%q", first.AlarmID, second.AlarmID)
	}
	if strings.Contains(first.AlarmID, ".") {
		t.Fatalf("alarm_id contains route-unsafe dot: %q", first.AlarmID)
	}
	changed := input
	changed.Alert.UpdateAt = input.Alert.UpdateAt.Add(time.Nanosecond)
	third, err := convertMessage(changed)
	if err != nil {
		t.Fatal(err)
	}
	if third.AlarmID == first.AlarmID {
		t.Fatalf("different snapshots share alarm_id: %q", first.AlarmID)
	}
	if first.EventID != identityPrefix+input.Alert.AlertID || third.EventID != first.EventID {
		t.Fatalf("event identity changed: first=%q third=%q", first.EventID, third.EventID)
	}
}

func TestConvertMessageMapsTerminalStatusAndFixedSeverity(t *testing.T) {
	alert := testAlert()
	alert.Status = domain.AlertStatusRecovered
	alert.Severity = "critical"
	alert.UpdateAt = time.Date(2026, 9, 1, 1, 2, 4, 0, time.UTC)
	end := time.Date(2026, 9, 1, 1, 2, 3, 0, time.UTC)
	alert.EndAt = &end
	alert.EndType = domain.AlertEndTypeSource
	alert.EndReason = "source recovered"
	message, err := convertMessage(lifecycle.FinalHookInput{
		Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-2"}, Alert: alert,
		Outcome: lifecycle.OutcomeAlertRecovered,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.Action != "resolved" || message.Level != "fatal" || message.CloseTime == nil || *message.CloseTime != "2026-09-01 09:02:03" || message.CloseReason == nil || *message.CloseReason != "source recovered" {
		t.Fatalf("terminal message=%+v", message)
	}

	alert.Status = domain.AlertStatusClosed
	alert.EndType = domain.AlertEndTypeSystem
	message, err = convertMessage(lifecycle.FinalHookInput{
		Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSystemOperation, ID: "op-1"}, Alert: alert,
		Outcome: lifecycle.OutcomeAlertClosed,
	})
	if err != nil || message.Action != "close" {
		t.Fatalf("closed message=%+v err=%v", message, err)
	}
}

func TestConvertMessageSupportsNoopAndForwardCompatibleEnrich(t *testing.T) {
	alert := testAlert()
	alert.Enrich = jsonObject(`{"processors":[{"future":{"status":"succeeded","value":{"new_field":"value"}}}]}`)
	message, err := convertMessage(lifecycle.FinalHookInput{
		Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event"}, Alert: alert,
		Outcome: lifecycle.OutcomeAlertCreated,
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.Name != alert.Title || message.Content != alert.Content || message.StrategyID != "" {
		t.Fatalf("fallback message=%+v", message)
	}
}

func TestConvertMessageRejectsUnknownSeverityAndRequiredText(t *testing.T) {
	alert := testAlert()
	alert.Severity = "custom"
	if _, err := convertMessage(lifecycle.FinalHookInput{Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event"}, Alert: alert, Outcome: lifecycle.OutcomeAlertCreated}); err == nil {
		t.Fatal("unknown severity accepted")
	}
	alert = testAlert()
	alert.Content = ""
	alert.Enrich = jsonObject(`{"processors":[]}`)
	if _, err := convertMessage(lifecycle.FinalHookInput{Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event"}, Alert: alert, Outcome: lifecycle.OutcomeAlertCreated}); err == nil {
		t.Fatal("empty content accepted")
	}
}

func TestCompatibilityPayloadNormalizesNumericModelInstanceID(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
		want string
	}{
		{"source_numeric_host_id", `163`, "163"},
		{"large_integer_keeps_precision", `9007199254740993`, "9007199254740993"},
		{"numeric_zero", `0`, "0"},
		{"opaque_string", `"00163|host"`, "00163|host"},
		{"null", `null`, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			alert := testAlert()
			alert.Enrich = jsonObject(`{"processors":[]}`)
			alert.ExtraData = jsonObject(`{"model_id":"cw-Host","model_inst_id":` + tt.raw + `}`)
			payload, err := CompatibilityPayload(alert, kacLevel)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := json.Unmarshal(payload, &fields); err != nil {
				t.Fatal(err)
			}
			if fields["model_inst_id"] != tt.want {
				t.Fatalf("model_inst_id=%v, want %q", fields["model_inst_id"], tt.want)
			}
			if string(alert.ExtraData["model_inst_id"]) != tt.raw {
				t.Fatal("projection changed frozen source fields")
			}
		})
	}
}

func TestCompatibilityPayloadRejectsNonScalarModelInstanceID(t *testing.T) {
	for _, raw := range []string{`true`, `[]`, `{}`} {
		t.Run(raw, func(t *testing.T) {
			alert := testAlert()
			alert.Enrich = jsonObject(`{"processors":[]}`)
			alert.ExtraData = jsonObject(`{"model_inst_id":` + raw + `}`)
			if _, err := CompatibilityPayload(alert, kacLevel); err == nil {
				t.Fatal("invalid model_inst_id accepted")
			}
		})
	}
}

func TestCompatibilityPayloadPrefersNumericLabelModelInstanceID(t *testing.T) {
	alert := testAlert()
	alert.Enrich = jsonObject(`{"processors":[]}`)
	alert.ExtraData = jsonObject(`{"model_inst_id":163}`)
	if err := json.Unmarshal([]byte(`{"model_inst_id":164}`), &alert.Labels); err != nil {
		t.Fatal(err)
	}
	message, err := CompatibilityMessage(alert, kacLevel)
	if err != nil {
		t.Fatal(err)
	}
	if message.ModelInstID != "164" {
		t.Fatalf("model_inst_id=%q, want label identity 164", message.ModelInstID)
	}
}

func TestCompatibilityMessageKeepsPromQLDisplayDimensions(t *testing.T) {
	t.Parallel()
	alert := testAlert()
	alert.Enrich = jsonObject(`{"processors":[{"display":{"status":"succeeded","patches":[
		{"op":"set","path":"$[\"labels\"][\"dimension_text\"]","value":"hostname=host-1 | zone=a"},
		{"op":"set","path":"$[\"extra_data\"][\"dimensions\"]","value":[
			{"name":"hostname","value":"host-1","real_key":"hostname","real_value":"host-1"},
			{"name":"zone","value":"a","real_key":"zone","real_value":"a"}
		]}
	]}}]}`)
	message, err := CompatibilityMessage(alert, kacLevel)
	if err != nil {
		t.Fatal(err)
	}
	if message.DimensionInfo != "hostname=host-1 | zone=a" {
		t.Fatalf("dimension_info=%q", message.DimensionInfo)
	}
}

func testAlert() domain.Alert {
	begin := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Alert{Revision: 1,
		EventSourceVersion: 1, AlertID: "alert-1", BKTenantID: "tenant-1", EventSourceID: "built_in_bk",
		Fingerprint: "fp", Title: "source title", Content: "source content", Severity: "warning",
		Dimensions: domain.DimensionMap{}, SubjectSystem: "cmdb", SubjectType: "host", SubjectID: "101", SubjectName: "host-101",
		SourceEventID: "source-event-1", Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{},
		Status: domain.AlertStatusActive, LatestEventID: "event-1", LastOccurredAt: begin,
		UpdateAt: begin.Add(time.Second), TriggerEventID: "event-1", BeginAt: begin, CreateAt: begin,
		EnrichStatus: domain.EnrichStatusSucceeded,
		Enrich: jsonObject(`{"processors":[
			{"strategy":{"status":"succeeded","value":{"strategy_id":123,"strategy_version":1,"monitor_template_id":7,"strategy_config_id":"cfg","strategy_name":"CPU","url":"/strategy/7","data_source":"system"}}},
			{"resource":{"status":"succeeded","value":{"bk_obj_id":"host","bk_inst_id":101,"model_id":"cw-Host","model_inst_id":"101","model_name":"主机","bk_biz_id":2,"bk_biz_name":"业务2","bk_set_id":null,"bk_set_name":"","bk_module_id":null,"bk_module_name":"","bk_cloud_id":0,"bk_cloud_name":"默认区域","cloud_plat_id":null,"dynamic_group_id":[],"cw_labels":[]}}},
			{"display":{"status":"succeeded","value":{"title":"CPU 使用率过高","content":"CPU reached 92.5%","object":"host-101","dimensions":[],"dimension_text":"host=101"}}},
			{"metric":{"status":"succeeded","value":{"display_name":"CPU 使用率","metric_name":"usage","unit":"percent","result_table_id":"system.cpu","aggregate_func":"avg","time_interval":60,"where_condition":"host='101'","metric_query_params":{"expression":"A"},"anomaly_begin_time":"2026-09-01T00:00:00Z"}}},
			{"source":{"status":"succeeded","value":{"source_id":"built_in_bk","source_name":"ignored","meta_info":"source-event-1"}}}
		]}`),
	}
}

func jsonObject(value string) domain.JSONObject {
	var object domain.JSONObject
	if err := json.Unmarshal([]byte(value), &object); err != nil {
		panic(err)
	}
	return object
}

func TestCustomPatchesAndExplicitKACMappings(t *testing.T) {
	alert := testAlert()
	alert.Enrich = jsonObject(`{"processors":[{"fields":{"status":"succeeded","patches":[{"op":"set","path":"$.title","value":"custom title"},{"op":"set","path":"$.labels.strategy_id","value":9001},{"op":"set","path":"$.labels.owner","value":"alice"},{"op":"set","path":"$.labels.monitor_template_id","value":33}]}}]}`)
	alert.EnrichStatus = domain.EnrichStatusSucceeded
	message, err := convertMessage(lifecycle.FinalHookInput{Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event-1"}, Alert: alert, Outcome: lifecycle.OutcomeAlertCreated})
	if err != nil {
		t.Fatal(err)
	}
	if message.Name != "custom title" || message.StrategyID != "33" {
		t.Fatalf("message=%+v", message)
	}
	payload, err := mappedPayload(message, alert, map[string]string{"custom_owner": "$.labels.owner"})
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(payload, &object); err != nil {
		t.Fatal(err)
	}
	if object["custom_owner"] != "alice" {
		t.Fatalf("payload=%s", payload)
	}
	if err := kingeye.ValidateFieldMappings(map[string]string{"bk_tenant_id": "$.labels.owner"}); err == nil {
		t.Fatal("overrode protocol tenant")
	}
}

func TestCMDBIdentityAndTopologyCollectionsReachKACOutput(t *testing.T) {
	alert := testAlert()
	alert.Enrich = jsonObject(`{"processors":[{"cmdb":{"status":"succeeded","patches":[{"op":"set","path":"$.labels.model_id","value":"cw-Host"},{"op":"set","path":"$.labels.model_inst_id","value":"202"},{"op":"set","path":"$.labels.bk_obj_id","value":"host"},{"op":"set","path":"$.labels.bk_inst_id","value":202},{"op":"set","path":"$.extra_data.bk_set_id","value":[3,4]},{"op":"set","path":"$.extra_data.bk_set_name","value":["A","B"]},{"op":"set","path":"$.extra_data.bk_module_id","value":[7,8]},{"op":"set","path":"$.extra_data.bk_module_name","value":["C","D"]}]}}]}`)
	message, err := CompatibilityMessage(alert, kacLevel)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(message)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document["model_inst_id"] != "202" || document["bk_inst_id"] != "202" || len(document["bk_set_id"].([]any)) != 2 || len(document["bk_module_name"].([]any)) != 2 {
		t.Fatal(document)
	}
}

func TestStrategyNavigationFactsReachKACWithoutFrontendURL(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(fmt.Sprint(terminal), func(t *testing.T) {
			alert := testAlert()
			alert.Enrich = jsonObject(`{"processors":[{"strategy":{"status":"succeeded","value":{
				"strategy_id":123,"strategy_version":2,"monitor_template_id":42,"strategy_config_id":"config1",
				"strategy_name":"CPU","strategy_kind":"Strategy","is_default":false,
				"strategy_instance":{"object_model_code":"cw-Host","bk_obj_id":"host","bk_inst_id":"host|101","object_model_group_id":"7"}
			}}}]}`)
			if terminal {
				alert.Status = domain.AlertStatusRecovered
				alert.EndType = domain.AlertEndTypeSource
				alert.EndReason = "source recovered"
				end := alert.BeginAt.Add(time.Minute)
				alert.EndAt = &end
			}
			outcome := lifecycle.OutcomeAlertCreated
			if terminal {
				outcome = lifecycle.OutcomeAlertRecovered
			}
			message, err := convertMessage(lifecycle.FinalHookInput{
				Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event"}, Alert: alert, Outcome: outcome,
			})
			if err != nil {
				t.Fatal(err)
			}
			info := message.FieldExtraInfo["strategy_name"].(map[string]any)
			if _, exists := info["url"]; exists {
				t.Fatalf("generated URL: %#v", info)
			}
			want := map[string]any{"bk_tenant_id": "tenant-1", "strategy_set_split_record_id": int64(123), "strategy_set_split_record_version": int64(2),
				"monitor_template_id": int64(42), "strategy_config_id": "config1", "strategy_kind": models.CWStrategyKindStrategy,
				"is_default": false, "strategy_instance": map[string]any{"object_model_code": "cw-Host", "bk_obj_id": "host", "bk_inst_id": "host|101", "object_model_group_id": "7"}}
			if !reflect.DeepEqual(info["strategy_context"], want) {
				t.Fatalf("facts=%#v want=%#v", info["strategy_context"], want)
			}
			if message.StrategyID != "42" || message.Content != alert.Content {
				t.Fatalf("message=%#v", message)
			}
		})
	}
}

func TestHistoricalStrategyURLAndExternalCustomURLSurviveProjection(t *testing.T) {
	alert := testAlert()
	input := lifecycle.FinalHookInput{Cause: lifecycle.AlertChangeCause{Type: lifecycle.AlertChangeCauseSourceEvent, ID: "event"}, Alert: alert, Outcome: lifecycle.OutcomeAlertCreated}
	message, err := convertMessage(input)
	if err != nil {
		t.Fatal(err)
	}
	info := message.FieldExtraInfo["strategy_name"].(map[string]any)
	if info["url"] != "/strategy/7" || info["strategy_context"] != nil {
		t.Fatalf("legacy=%#v", info)
	}
	alert.ExtraData["field_extra_info"] = json.RawMessage(`{"strategy_name":{"url":"https://external.example.test/#/kmc/scene/monitorViewDetails?custom=1"}}`)
	input.Alert = alert
	message, err = convertMessage(input)
	if err != nil {
		t.Fatal(err)
	}
	if got := message.FieldExtraInfo["strategy_name"].(map[string]any)["url"]; got != "https://external.example.test/#/kmc/scene/monitorViewDetails?custom=1" {
		t.Fatalf("external URL=%v", got)
	}
}

func TestCloudStrategyFactsDoNotRequireDefaultAttribute(t *testing.T) {
	fields := strategyFieldExtraInfo("tenant-a", models.StrategyValues{StrategyKind: models.CWStrategyKindCloud, MonitorTemplateID: 42, StrategyConfigID: "cfg"})
	context := fields["strategy_name"].(map[string]any)["strategy_context"].(map[string]any)
	if _, exists := context["is_default"]; exists {
		t.Fatalf("invented default attribute: %#v", context)
	}
	if context["monitor_template_id"] != int64(42) || context["strategy_kind"] != models.CWStrategyKindCloud {
		t.Fatalf("cloud context=%#v", context)
	}
}
