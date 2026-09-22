// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kachook

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/view"
	"linkd/internal/lifecycle"
)

const (
	identityPrefix = "linkd-"
	sourceName     = "鲸眼监控"
	kacTimeLayout  = "2006-01-02 15:04:05"
)

var kacTimeZone = time.FixedZone("Asia/Shanghai", 8*60*60)

type enrichValues struct {
	strategy models.StrategyValues
	resource models.ResourceValues
	display  models.DisplayValues
	metric   models.MetricValues
	log      models.LogValues
	apm      models.APMValues
	source   models.SourceValues
}

func convertMessage(input lifecycle.FinalHookInput) (kingeye.AlarmMessage, error) {
	return convertMessageWithLevel(input, kacLevel)
}

func convertMessageWithLevel(input lifecycle.FinalHookInput, resolve func(string) (string, error)) (kingeye.AlarmMessage, error) {
	if err := input.Cause.Validate(); err != nil {
		return kingeye.AlarmMessage{}, fmt.Errorf("KAC alarm cause: %w", err)
	}
	if err := input.Alert.Validate(); err != nil {
		return kingeye.AlarmMessage{}, fmt.Errorf("KAC alarm alert: %w", err)
	}
	effective, err := view.EnrichedAlert(input.Alert)
	if err != nil {
		return kingeye.AlarmMessage{}, err
	}
	input.Alert = effective
	values, err := decodeEffectiveEnrich(effective)
	if err != nil {
		return kingeye.AlarmMessage{}, err
	}
	action, err := kacAction(input.Alert.Status, input.Outcome)
	if err != nil {
		return kingeye.AlarmMessage{}, err
	}
	level, err := resolve(input.Alert.Severity)
	if err != nil && input.Alert.Status == domain.AlertStatusClosed && input.Alert.EndType == domain.AlertEndTypeSystem && input.Alert.EndReason == "unknown_severity" {
		level = input.Alert.Severity
		err = nil
	}
	if err != nil {
		return kingeye.AlarmMessage{}, err
	}
	query, err := encodeMetricQueryParams(values.metric.MetricQueryParams)
	if err != nil {
		return kingeye.AlarmMessage{}, err
	}
	eventIdentity := identityPrefix + input.Alert.AlertID
	message := kingeye.AlarmMessage{
		AlarmID: kacAlarmID(input), EventID: eventIdentity,
		SourceID: input.Alert.EventSourceID, SourceName: sourceName,
		Item: firstNonEmpty(values.metric.DisplayName, values.strategy.StrategyName), MetricName: values.metric.MetricName,
		Name: input.Alert.Title, Content: input.Alert.Content,
		AlarmTime: input.Alert.BeginAt.In(kacTimeZone).Format(kacTimeLayout), Action: action, Level: level,
		Object: firstNonEmpty(input.Alert.SubjectName, input.Alert.SubjectID), BKTenantID: input.Alert.BKTenantID,
		BKBizID: scalarText(values.resource.BKBizID), BKBizName: values.resource.BKBizName,
		BKSetID: scalarText(values.resource.BKSetID), BKSetName: values.resource.BKSetName,
		BKModuleID: scalarText(values.resource.BKModuleID), BKModuleName: values.resource.BKModuleName,
		BKCloudID: scalarText(values.resource.BKCloudID), BKCloudName: values.resource.BKCloudName,
		BKObjID: scalarText(values.resource.BKObjID), BKInstID: scalarText(values.resource.BKInstID),
		MetaInfo:   firstNonEmpty(values.source.MetaInfo, input.Alert.SourceEventID),
		StrategyID: positiveIntText(values.strategy.MonitorTemplateID), StrategyName: values.strategy.StrategyName,
		DimensionInfo: values.display.DimensionText, ModelInstID: values.resource.ModelInstID,
		ModelID: values.resource.ModelID, ModelName: values.resource.ModelName,
		AnomalyBeginTime: values.metric.AnomalyBeginTime, MetricUniqueID: values.metric.MetricUniqueID,
		ResultTableID: values.metric.ResultTableID, TimeInterval: scalarText(values.metric.TimeInterval),
		AggregateFunc: values.metric.AggregateFunc, WhereCondition: values.metric.WhereCondition,
		Unit: values.metric.Unit, DataSource: values.strategy.DataSource,
		FieldExtraInfo:    kingeye.FieldExtraInfo{StrategyName: kingeye.StrategyExtraInfo{URL: values.strategy.URL}},
		MetricQueryParams: query, DynamicGroupID: nonNilStrings(values.resource.DynamicGroupID), CWLabels: preferredLabels(values.log.CWLabels, values.apm.CWLabels, values.resource.CWLabels),
		LogThemeID: scalarInt64(values.log.LogThemeID), LogThemeName: values.log.LogThemeName,
		LogQueryString: values.log.LogQueryString, LogRelateInfo: values.log.LogRelateInfo,
		APMAppID: scalarInt64(values.apm.APMAppID), APMAppName: values.apm.APMAppName,
		APMAppAlias: values.apm.APMAppAlias, APMServiceName: values.apm.APMServiceName,
		APMInstanceName: values.apm.APMInstanceName, APMInterfaceName: values.apm.APMInterfaceName,
		APMNetPeerName: values.apm.APMNetPeerName,
	}
	if input.Alert.Status.Terminal() {
		closeTime := input.Alert.EndAt.In(kacTimeZone).Format(kacTimeLayout)
		closeReason := input.Alert.EndReason
		message.CloseTime, message.CloseReason = &closeTime, &closeReason
	}
	if message.Name == "" {
		return kingeye.AlarmMessage{}, fmt.Errorf("KAC alarm name is required")
	}
	if message.Content == "" {
		return kingeye.AlarmMessage{}, fmt.Errorf("KAC alarm content is required")
	}
	return message, nil
}

// kacAlarmID 为每个 Alert 快照生成 KAC 路由可接受的稳定 UUID。
// 受限于 KAC 页面 URL 对告警 ID 的严格限制（不允许出现 "."），因此首版复用 alarm_callback 的生成逻辑。
// event_id 继续承载 Linkd Alert 身份，用于关联 firing 与终态消息；alarm_id 仅作为本次 KAC 记录身份。
func kacAlarmID(input lifecycle.FinalHookInput) string {
	name := digestStrings(
		"linkd:kac-alarm", input.Alert.BKTenantID, input.Alert.AlertID,
		input.Alert.UpdateAt.UTC().Format(time.RFC3339Nano), string(input.Outcome),
	)
	return identityPrefix + uuid.NewSHA1(uuid.NameSpaceURL, []byte(name)).String()
}

func decodeProcessorValue(value domain.JSONObject, destination any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	return nil
}

func encodeMetricQueryParams(value any) (string, error) {
	if value == nil {
		return "{}", nil
	}
	if text, ok := value.(string); ok {
		decoder := json.NewDecoder(bytes.NewBufferString(text))
		decoder.UseNumber()
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			return "", fmt.Errorf("decode KAC metric_query_params: %w", err)
		}
		value = object
	}
	data, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("encode KAC metric_query_params: %w", err)
	}
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return "", fmt.Errorf("KAC metric_query_params must be an object: %w", err)
	}
	normalized, err := json.Marshal(object)
	if err != nil {
		return "", fmt.Errorf("normalize KAC metric_query_params: %w", err)
	}
	return string(normalized), nil
}

func kacAction(status domain.AlertStatus, outcome lifecycle.ProcessOutcome) (string, error) {
	switch status {
	case domain.AlertStatusActive:
		if outcome != lifecycle.OutcomeAlertCreated && outcome != lifecycle.OutcomeAlertUpdated && outcome != lifecycle.OutcomeAlertSeverityChanged {
			return "", fmt.Errorf("KAC active alert outcome is invalid: %q", outcome)
		}
		return "firing", nil
	case domain.AlertStatusRecovered:
		if outcome != lifecycle.OutcomeAlertRecovered {
			return "", fmt.Errorf("KAC recovered alert outcome is invalid: %q", outcome)
		}
		return "resolved", nil
	case domain.AlertStatusClosed:
		if outcome != lifecycle.OutcomeAlertClosed {
			return "", fmt.Errorf("KAC closed alert outcome is invalid: %q", outcome)
		}
		return "close", nil
	default:
		return "", fmt.Errorf("KAC alert status is invalid: %q", status)
	}
}

func kacLevel(severity string) (string, error) {
	switch severity {
	case "critical":
		return "fatal", nil
	case "warning":
		return "warning", nil
	case "info":
		return "remind", nil
	default:
		return "", fmt.Errorf("KAC severity is unsupported: %q", severity)
	}
}

func positiveIntText(value int64) string {
	if value <= 0 {
		return ""
	}
	return strconv.FormatInt(value, 10)
}

func scalarText(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32)
	default:
		return fmt.Sprint(value)
	}
}

func scalarInt64(value any) int64 {
	text := scalarText(value)
	if text == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

func preferredLabels(candidates ...[]string) []string {
	for _, candidate := range candidates {
		if len(candidate) != 0 {
			return nonNilStrings(candidate)
		}
	}
	return []string{}
}

func decodeEffectiveEnrich(alert domain.Alert) (enrichValues, error) {
	flat := alert.ExtraData.Clone()
	if flat == nil {
		flat = domain.JSONObject{}
	}
	for key, value := range alert.Labels {
		data, err := json.Marshal(value)
		if err != nil {
			return enrichValues{}, err
		}
		flat[key] = data
	}
	var values enrichValues
	for _, target := range []any{&values.strategy, &values.resource, &values.display, &values.metric, &values.log, &values.apm, &values.source} {
		if err := decodeProcessorValue(flat, target); err != nil {
			return values, err
		}
	}
	return values, nil
}
