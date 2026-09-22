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
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
)

const (
	identityPrefix = "linkd-"
	sourceName     = "鲸眼监控"
	kacTimeLayout  = "2006-01-02 15:04:05"
)

var kacTimeZone = time.FixedZone("Asia/Shanghai", 8*60*60)

// Message 是发送给 KAC alarm_collect_topic 的扁平 Alarm JSON。
type Message struct {
	AlarmID           string         `json:"alarm_id"`
	SourceID          string         `json:"source_id"`
	SourceName        string         `json:"source_name"`
	Item              string         `json:"item"`
	MetricName        string         `json:"metric_name"`
	Name              string         `json:"name"`
	EventID           string         `json:"event_id"`
	AlarmTime         string         `json:"alarm_time"`
	Content           string         `json:"content"`
	Action            string         `json:"action"`
	Level             string         `json:"level"`
	Object            string         `json:"object"`
	BKTenantID        string         `json:"bk_tenant_id"`
	BKBizID           string         `json:"bk_biz_id"`
	BKBizName         string         `json:"bk_biz_name"`
	BKSetID           string         `json:"bk_set_id"`
	BKSetName         string         `json:"bk_set_name"`
	BKModuleID        string         `json:"bk_module_id"`
	BKModuleName      string         `json:"bk_module_name"`
	BKCloudID         string         `json:"bk_cloud_id"`
	BKCloudName       string         `json:"bk_cloud_name"`
	BKObjID           string         `json:"bk_obj_id"`
	BKInstID          string         `json:"bk_inst_id"`
	BKServiceID       string         `json:"bk_service_id"`
	MetaInfo          string         `json:"meta_info"`
	StrategyID        string         `json:"strategy_id"`
	StrategyName      string         `json:"strategy_name"`
	DimensionInfo     string         `json:"dimension_info"`
	ModelInstID       string         `json:"model_inst_id"`
	ModelID           string         `json:"model_id"`
	ModelName         string         `json:"model_name"`
	AnomalyBeginTime  *string        `json:"anomaly_begin_time"`
	CloseTime         *string        `json:"close_time,omitempty"`
	CloseReason       *string        `json:"close_reason,omitempty"`
	MetricUniqueID    string         `json:"metric_unique_id"`
	ResultTableID     string         `json:"result_table_id"`
	Namespace         string         `json:"Namespace"`
	TimeInterval      string         `json:"time_interval"`
	AggregateFunc     string         `json:"aggregate_func"`
	WhereCondition    string         `json:"where_condition"`
	Unit              string         `json:"unit"`
	DataSource        string         `json:"data_source"`
	FieldExtraInfo    fieldExtraInfo `json:"field_extra_info"`
	MetricQueryParams string         `json:"metric_query_params"`
	DynamicGroupID    []string       `json:"dynamic_group_id"`
	CWLabels          []string       `json:"cw_labels"`
	LogThemeID        int64          `json:"log_theme_id"`
	LogThemeName      string         `json:"log_theme_name"`
	LogQueryString    string         `json:"log_query_string"`
	LogRelateInfo     string         `json:"log_relate_info"`
	APMAppID          int64          `json:"apm_app_id"`
	APMAppName        string         `json:"apm_app_name"`
	APMAppAlias       string         `json:"apm_app_alias"`
	APMServiceName    string         `json:"apm_service_name"`
	APMInstanceName   string         `json:"apm_instance_name"`
	APMInterfaceName  string         `json:"apm_interface_name"`
	APMNetPeerName    string         `json:"apm_net_peer_name"`
	BCSClusterID      string         `json:"bcs_cluster_id"`
	ClusterName       string         `json:"cluster_name"`
	K8sNamespace      string         `json:"namespace"`
	Service           string         `json:"service"`
	WorkloadKind      string         `json:"workload_kind"`
	WorkloadName      string         `json:"workload_name"`
	PodName           string         `json:"pod_name"`
	ContainerName     string         `json:"container_name"`
	CloudPlatformID   string         `json:"cloud_plat_id"`
}

type fieldExtraInfo struct {
	StrategyName strategyExtraInfo `json:"strategy_name"`
}

type strategyExtraInfo struct {
	URL string `json:"url"`
}

type enrichValues struct {
	strategy models.StrategyValues
	resource models.ResourceValues
	display  models.DisplayValues
	metric   models.MetricValues
	log      models.LogValues
	apm      models.APMValues
	source   models.SourceValues
}

func convertMessage(input lifecycle.FinalHookInput) (Message, error) {
	return convertMessageWithLevel(input, kacLevel)
}

func convertMessageWithLevel(input lifecycle.FinalHookInput, resolve func(string) (string, error)) (Message, error) {
	if err := input.Cause.Validate(); err != nil {
		return Message{}, fmt.Errorf("KAC alarm cause: %w", err)
	}
	if err := input.Alert.Validate(); err != nil {
		return Message{}, fmt.Errorf("KAC alarm alert: %w", err)
	}
	values, err := decodeEnrich(input.Alert.Enrich)
	if err != nil {
		return Message{}, err
	}
	action, err := kacAction(input.Alert.Status, input.Outcome)
	if err != nil {
		return Message{}, err
	}
	level, err := resolve(input.Alert.Severity)
	if err != nil && input.Alert.Status == domain.AlertStatusClosed && input.Alert.EndType == domain.AlertEndTypeSystem && input.Alert.EndReason == "unknown_severity" {
		level = input.Alert.Severity
		err = nil
	}
	if err != nil {
		return Message{}, err
	}
	query, err := encodeMetricQueryParams(values.metric.MetricQueryParams)
	if err != nil {
		return Message{}, err
	}
	eventIdentity := identityPrefix + input.Alert.AlertID
	message := Message{
		AlarmID: kacAlarmID(input), EventID: eventIdentity,
		SourceID: input.Alert.EventSourceID, SourceName: sourceName,
		Item: firstNonEmpty(values.metric.DisplayName, values.strategy.StrategyName), MetricName: values.metric.MetricName,
		Name: firstNonEmpty(values.display.Title, input.Alert.Title), Content: firstNonEmpty(values.display.Content, input.Alert.Content),
		AlarmTime: input.Alert.BeginAt.In(kacTimeZone).Format(kacTimeLayout), Action: action, Level: level,
		Object: firstNonEmpty(values.display.Object, input.Alert.SubjectName, input.Alert.SubjectID), BKTenantID: input.Alert.BKTenantID,
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
		FieldExtraInfo:    fieldExtraInfo{StrategyName: strategyExtraInfo{URL: values.strategy.URL}},
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
		return Message{}, fmt.Errorf("KAC alarm name is required")
	}
	if message.Content == "" {
		return Message{}, fmt.Errorf("KAC alarm content is required")
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

func decodeEnrich(object domain.JSONObject) (enrichValues, error) {
	payload, err := enrich.DecodePayload(object)
	if err != nil {
		return enrichValues{}, fmt.Errorf("decode KAC alarm enrich payload: %w", err)
	}
	var values enrichValues
	seen := make(map[string]struct{}, len(payload.Processors))
	for _, entry := range payload.Processors {
		for name, envelope := range entry {
			if _, exists := seen[name]; exists {
				return enrichValues{}, fmt.Errorf("decode KAC alarm enrich payload: duplicate processor %q", name)
			}
			seen[name] = struct{}{}
			if envelope.Status == domain.EnrichStatusFailed || envelope.Status == domain.EnrichStatusSkipped {
				continue
			}
			switch name {
			case "strategy":
				err = decodeProcessorValue(envelope.Value, &values.strategy)
			case "resource":
				err = decodeProcessorValue(envelope.Value, &values.resource)
			case "display":
				err = decodeProcessorValue(envelope.Value, &values.display)
			case "metric":
				err = decodeProcessorValue(envelope.Value, &values.metric)
			case "log":
				err = decodeProcessorValue(envelope.Value, &values.log)
			case "apm":
				err = decodeProcessorValue(envelope.Value, &values.apm)
			case "source":
				err = decodeProcessorValue(envelope.Value, &values.source)
			}
			if err != nil {
				return enrichValues{}, fmt.Errorf("decode KAC alarm enrich processor %q: %w", name, err)
			}
		}
	}
	return values, nil
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
