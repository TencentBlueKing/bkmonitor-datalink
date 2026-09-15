// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kachook

import (
	"encoding/json"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
)

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
	if message.AlarmID != "linkd-alert-1" || message.EventID != "linkd-alert-1" || message.SourceName != "鲸眼监控" {
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
	var query map[string]any
	if err := json.Unmarshal([]byte(message.MetricQueryParams), &query); err != nil || query["expression"] != "A" {
		t.Fatalf("metric_query_params=%q err=%v", message.MetricQueryParams, err)
	}
	if message.CloseTime != nil || message.CloseReason != nil {
		t.Fatalf("active close fields=%+v", message)
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
	if message.Name != alert.Title || message.Content != alert.Content || message.StrategyID != "" || message.MetricQueryParams != "{}" {
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

func testAlert() domain.Alert {
	begin := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Alert{
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
			{"metric":{"status":"succeeded","value":{"display_name":"CPU 使用率","metric_name":"usage","unit":"percent","result_table_id":"system.cpu","metric_unique_id":"system.cpu.usage","aggregate_func":"avg","time_interval":60,"where_condition":"host='101'","metric_query_params":{"expression":"A"},"anomaly_begin_time":"2026-09-01T00:00:00Z"}}},
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
