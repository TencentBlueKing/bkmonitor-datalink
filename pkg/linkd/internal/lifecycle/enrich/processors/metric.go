// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

// Metric 丰富告警关联的指标信息。
type Metric struct{}

// Name 返回稳定的 Processor 名称。
func (Metric) Name() string { return rules.MetricProcessor }

// Match 判断当前告警是否适用 Metric。
func (Metric) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 查询平台历史并生成指标信息。
func (Metric) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	alert := scope.Alert()
	values := models.MetricValues{}
	status := domain.EnrichStatusSucceeded
	diagnostics := make([]enrich.Diagnostic, 0, 1)
	if anomalyBeginTime, exists, valid := parseAnomalyBeginTime(alert.ExtraData); exists {
		if valid {
			values.AnomalyBeginTime = &anomalyBeginTime
		} else {
			status = domain.EnrichStatusPartial
			diagnostics = append(diagnostics, enrich.Diagnostic{
				Code: enrich.DiagnosticCodeInvalidField, Fields: []string{"extra_data." + rules.FieldAnomalyBeginTime},
			})
		}
	}

	ids, idDiagnostics := enrich.ValidateRequiredIDs(alert)
	if len(idDiagnostics) != 0 {
		value, err := metricResultValue(scope, values)
		if err != nil {
			return enrich.ProcessorResult{}, err
		}
		return enrich.ProcessorResult{
			Status: domain.EnrichStatusFailed, Value: value,
			Diagnostics: append(diagnostics, idDiagnostics...),
		}, nil
	}
	strategy, strategyFound, strategyErr := scope.CWStrategyByBKStrategyID(ctx, ids.StrategyID)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if strategyErr != nil || !strategyFound {
		return metricDependencyFailure(scope, values, diagnostics, rules.DependencyKingeyeStrategy)
	}
	history, found, err := scope.BKStrategyHistory(ctx, ids.StrategyID, ids.HistoryID)
	if ctx.Err() != nil {
		return enrich.ProcessorResult{}, ctx.Err()
	}
	if err != nil || !found {
		return metricDependencyFailure(scope, values, diagnostics, rules.DependencyPlatformStrategyHistory)
	}
	snapshot, err := history.Snapshot()
	if err != nil || snapshot.BKBizID != ids.BizID || len(snapshot.Items) == 0 || len(snapshot.Items[0].QueryConfigs) == 0 {
		return metricDependencyFailure(scope, values, diagnostics, rules.DependencyPlatformStrategyHistory)
	}
	item := snapshot.Items[0]
	query := item.QueryConfigs[0]
	metricName := cleanMetricName(strategy, snapshot, item, query)
	unit, err := cleanUnit(ctx, scope, strategy, query)
	if err != nil {
		return metricDependencyFailure(scope, values, diagnostics, rules.DependencyMetricLibrary)
	}
	aggregateFunc := cleanAggregateFunc(strategy)
	timeInterval := cleanTimeInterval(strategy)
	metricQueryParams := buildMetricQueryParams(snapshot, strategy, scope.Alert().Dimensions, ids.BizID)
	displayName, err := cleanItem(ctx, scope, strategy, item, query)
	if err != nil {
		return metricDependencyFailure(scope, values, diagnostics, rules.DependencyMetricLibrary)
	}
	whereCondition := cleanWhereCondition(scope.Alert().Dimensions)
	values.DisplayName = displayName
	values.MetricName = metricName
	values.Unit = unit
	values.ResultTableID = query.ResultTableID
	values.MetricUniqueID = query.ResultTableID + "." + query.MetricField
	values.AggregateFunc = aggregateFunc
	values.TimeInterval = timeInterval
	values.WhereCondition = whereCondition
	values.MetricQueryParams = metricQueryParams
	value, encodeErr := metricResultValue(scope, values)
	if encodeErr != nil {
		return enrich.ProcessorResult{}, encodeErr
	}
	return enrich.ProcessorResult{Status: status, Value: value, Diagnostics: diagnostics}, nil
}

type metricQueryParams struct {
	Expression   string                    `json:"expression"`
	Functions    any                       `json:"functions"`
	QueryConfigs []metricQueryConfig       `json:"query_configs"`
	Function     metricQueryParamFunctions `json:"function"`
	BKBizID      int64                     `json:"bk_biz_id"`
	FieldTag     models.CWStrategyFieldTag `json:"field_tag,omitempty"`
}

type metricQueryParamFunctions struct {
	TimeCompare []string `json:"time_compare"`
}

type metricQueryConfig struct {
	CustomEventName string                 `json:"custom_event_name"`
	QueryString     string                 `json:"query_string"`
	IndexSetID      int64                  `json:"index_set_id"`
	BKBizID         int64                  `json:"bk_biz_id"`
	DataSourceLabel string                 `json:"data_source_label"`
	DataTypeLabel   string                 `json:"data_type_label"`
	GroupBy         []string               `json:"group_by"`
	Table           string                 `json:"table"`
	PromQL          string                 `json:"promql"`
	Metrics         []metricQueryMetric    `json:"metrics"`
	Interval        int64                  `json:"interval"`
	Where           []metricQueryCondition `json:"where"`
	TimeField       any                    `json:"time_field"`
	ExtendFields    any                    `json:"extend_fields"`
	FilterDict      map[string]any         `json:"filter_dict"`
	Functions       any                    `json:"functions"`
}

type metricQueryMetric struct {
	Field  string `json:"field"`
	Method string `json:"method"`
	Alias  string `json:"alias"`
}

type metricQueryCondition struct {
	Condition string `json:"condition,omitempty"`
	Key       string `json:"key"`
	Method    string `json:"method"`
	Value     any    `json:"value"`
}

func buildMetricQueryParams(
	snapshot models.BkStrategySnapshot,
	strategy models.CWStrategy,
	dimensions domain.DimensionMap,
	bizID int64,
) metricQueryParams {
	item := snapshot.Items[0]
	params := metricQueryParams{
		Expression: item.Expression, Functions: rawJSONValue(item.Functions, []any{}),
		QueryConfigs: make([]metricQueryConfig, 0, len(item.QueryConfigs)),
		Function:     metricQueryParamFunctions{TimeCompare: []string{}}, BKBizID: bizID,
	}
	if strategy.Kind != models.CWStrategyKindCloud && strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric {
		params.FieldTag = strategy.Spec.FieldTag
	}
	for _, query := range item.QueryConfigs {
		selectedDimensions := selectQueryDimensions(query, dimensions)
		translated := translateMetricQuery(query, selectedDimensions)
		params.QueryConfigs = append(params.QueryConfigs, metricQueryConfig{
			CustomEventName: query.CustomEventName, QueryString: query.QueryString, IndexSetID: query.IndexSetID,
			BKBizID: bizID, DataSourceLabel: query.DataSourceLabel, DataTypeLabel: query.DataTypeLabel,
			GroupBy: intersectDimensions(query.AggregateBy, selectedDimensions), Table: translated.table,
			PromQL: query.PromQL, Metrics: []metricQueryMetric{{Field: translated.field, Method: query.AggregateMethod, Alias: query.Alias}},
			Interval: query.AggregatePeriod, Where: mergeQueryConditions(query.AggregateFilter, selectedDimensions),
			TimeField: rawJSONValue(query.TimeField, nil), ExtendFields: jsonObjectValue(query.ExtendFields),
			FilterDict: translated.filterDict, Functions: rawJSONValue(query.Functions, []any{}),
		})
	}
	return params
}

type translatedMetricQuery struct {
	field      string
	table      string
	filterDict map[string]any
}

func translateMetricQuery(query models.BkStrategyQueryConfig, dimensions map[string]any) translatedMetricQuery {
	translated := translatedMetricQuery{field: query.MetricField, table: query.ResultTableID, filterDict: map[string]any{}}
	switch {
	case query.DataSourceLabel == rules.DataSourceBKFTA:
		translated.field = query.AlertName
		translated.table = rules.ResultTableEvent
	case query.DataSourceLabel == rules.DataSourceBKMonitor && query.DataTypeLabel == rules.DataTypeLog:
		if objectID, objectFound := dimensions[rules.FieldBKObjID]; objectFound {
			if instanceID, instanceFound := dimensions[rules.FieldBKInstID]; instanceFound {
				delete(dimensions, rules.FieldBKObjID)
				delete(dimensions, rules.FieldBKInstID)
				dimensions[fmt.Sprintf("bk_%v_id", objectID)] = fmt.Sprint(instanceID)
			}
		}
		translated.field = rules.MetricFieldEventCount
	case query.DataSourceLabel == rules.DataSourceCustom && query.DataTypeLabel == rules.DataTypeEvent:
		translated.field = rules.MetricFieldIndex
		translated.filterDict[rules.FieldEventName] = query.CustomEventName
	case query.DataSourceLabel == rules.DataSourceBKMonitor && query.DataTypeLabel == rules.DataTypeAlert:
		translated.field = fmt.Sprint(query.BKStrategyID)
		translated.table = rules.ResultTableAlert
	case query.DataSourceLabel == rules.DataSourceBKLogSearch && query.DataTypeLabel == rules.DataTypeLog:
		translated.field = query.QueryString
	}
	return translated
}

func selectQueryDimensions(query models.BkStrategyQueryConfig, dimensions domain.DimensionMap) map[string]any {
	available := make(map[string]any, len(dimensions)+2)
	for key, value := range dimensions {
		if key == rules.FieldBKHostID {
			continue
		}
		available[strings.TrimPrefix(key, rules.DimensionTagPrefix)] = rules.ScalarValue(value)
	}
	if strings.HasPrefix(query.ResultTableID, rules.SystemTablePrefix) {
		if value, exists := available[rules.FieldIP]; exists {
			available[rules.FieldBKTargetIP] = value
		}
		if value, exists := available[rules.FieldBKCloudID]; exists {
			available[rules.FieldBKTargetCloudID] = value
		}
	}

	selected := make(map[string]any)
	for _, key := range query.AggregateBy {
		if strings.HasPrefix(key, rules.TaskIndexPrefix) {
			continue
		}
		value, exists := available[key]
		if exists {
			selected[key] = value
		}
	}
	return selected
}

func intersectDimensions(aggregateBy []string, dimensions map[string]any) []string {
	groupBy := make([]string, 0, len(aggregateBy))
	for _, key := range aggregateBy {
		if _, exists := dimensions[key]; exists {
			groupBy = append(groupBy, key)
		}
	}
	return groupBy
}

func mergeQueryConditions(raw json.RawMessage, dimensions map[string]any) []metricQueryCondition {
	var conditions []metricQueryCondition
	_ = json.Unmarshal(raw, &conditions)
	keys := make([]string, 0, len(dimensions))
	for key := range dimensions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := dimensions[key]
		conditions = append(conditions, metricQueryCondition{Condition: rules.ConditionAnd, Key: key, Method: rules.ConditionEqual, Value: []any{value}})
	}
	if len(conditions) != 0 {
		conditions[0].Condition = ""
	}
	return conditions
}

func rawJSONValue(raw json.RawMessage, fallback any) any {
	if len(bytes.TrimSpace(raw)) == 0 {
		return fallback
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return fallback
	}
	return value
}

func jsonObjectValue(value domain.JSONObject) any {
	if value == nil {
		return map[string]any{}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	return rawJSONValue(encoded, map[string]any{})
}

func metricResultValue(scope *enrich.Scope, values models.MetricValues) (domain.JSONObject, error) {
	scope.Context().Metric.Set(values)
	return scope.Context().Metric.JSONObject()
}

func metricDependencyFailure(
	scope *enrich.Scope,
	values models.MetricValues,
	diagnostics []enrich.Diagnostic,
	dependency string,
) (enrich.ProcessorResult, error) {
	value, err := metricResultValue(scope, values)
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	return enrich.ProcessorResult{
		Status: domain.EnrichStatusFailed, Value: value,
		Diagnostics: append(diagnostics, enrich.Diagnostic{
			Code: enrich.DiagnosticCodeDependencyInvalid, Dependency: dependency,
		}),
	}, nil
}

func parseAnomalyBeginTime(extraData domain.JSONObject) (string, bool, bool) {
	raw, exists := extraData[rules.FieldAnomalyBeginTime]
	if !exists {
		return "", false, true
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, false
	}
	return value, true, true
}

func cleanWhereCondition(dimensions domain.DimensionMap) string {
	keys := make([]string, 0, len(dimensions))
	for key := range dimensions {
		if key == rules.FieldBKTopoNode || key == rules.FieldBKHostID {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)

	conditions := make([]string, 0, len(keys))
	for _, key := range keys {
		conditions = append(conditions, fmt.Sprintf("%s='%v'", key, rules.ScalarValue(dimensions[key])))
	}
	return strings.Join(conditions, " "+rules.ConditionAnd+" ")
}

func cleanAggregateFunc(strategy models.CWStrategy) string {
	if strategy.Spec.StrategyItem == nil {
		return rules.DefaultAggregateFunc
	}
	return strategy.Spec.StrategyItem.AggregateMethod
}

func cleanTimeInterval(strategy models.CWStrategy) any {
	if strategy.Spec.StrategyItem == nil {
		return rules.DefaultTimeInterval
	}
	return strategy.Spec.StrategyItem.AggregatePeriod
}

func cleanUnit(
	ctx context.Context,
	scope *enrich.Scope,
	strategy models.CWStrategy,
	query models.BkStrategyQueryConfig,
) (string, error) {
	if strategy.Spec.StrategyItem == nil || strategy.Spec.MonitorItemType == models.CWMonitorItemTypeLogKeyword {
		// 日志关键字
		return "", nil
	}
	tableID := query.ResultTableID
	if tableID == "" {
		tableID = strategy.Spec.TableID
	}
	fieldName := query.MetricField
	isDerivedMetric := strategy.Kind != models.CWStrategyKindCloud &&
		strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric
	if isDerivedMetric {
		fieldName = strategy.Spec.FieldName
	}
	metricQuery := models.MetricLibraryQuery{TableID: tableID, FieldName: fieldName}
	if isDerivedMetric {
		// KAC 的衍生指标查询按 field_name 和 derived_metric 标签定位，不限定 table_id。
		metricQuery.TableID = ""
		metricQuery.FieldTag = models.CWStrategyFieldTagDerivedMetric
	}
	metadata, found, err := scope.MetricLibrary(ctx, metricQuery)
	if err != nil {
		return "", err
	}
	if found && metadata.Unit != "" {
		return metadata.Unit, nil
	}
	if isDerivedMetric {
		// 指标库未命中，回落到 query_config 中的 unit 字段
		return query.Unit, nil
	}
	if strategy.ObjectModelCode != nil && *strategy.ObjectModelCode == rules.UptimeModelCode {
		// 拨测指标，固定返回
		return rules.DefaultUptimeUnit, nil
	}
	return "", nil
}

func cleanMetricName(
	strategy models.CWStrategy,
	snapshot models.BkStrategySnapshot,
	item models.BkStrategyItem,
	query models.BkStrategyQueryConfig,
) string {
	if strategy.Spec.MonitorItemType == models.CWMonitorItemTypeLog ||
		strategy.Spec.MonitorItemType == models.CWMonitorItemTypeLogKeyword {
		if strategy.Spec.AliasName != "" {
			return strategy.Spec.AliasName
		}
		if queryString := sourceConfigString(strategy.Spec.SourceConfig, rules.FieldQueryString); queryString != "" {
			return queryString
		}
		// 日志指标和日志关键字
		return rules.LogMetricFallback
	}
	if strategy.Kind != models.CWStrategyKindCloud && strategy.Spec.FieldTag == models.CWStrategyFieldTagDerivedMetric {
		// 衍生指标
		return strategy.Spec.FieldName
	}
	if len(snapshot.Items) > 1 || strategy.Spec.StrategyItem == nil ||
		hasFunctions(strategy.Spec.StrategyItem.Functions) || hasFunctions(item.Functions) {
		return ""
	}
	// 普通单指标
	return query.MetricField
}

func sourceConfigString(sourceConfig domain.JSONObject, field string) string {
	raw, exists := sourceConfig[field]
	if !exists {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func hasFunctions(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) != 0 && !bytes.Equal(trimmed, []byte("null")) &&
		!bytes.Equal(trimmed, []byte("[]")) && !bytes.Equal(trimmed, []byte(`""`))
}

func cleanItem(
	ctx context.Context,
	scope *enrich.Scope,
	strategy models.CWStrategy,
	item models.BkStrategyItem,
	query models.BkStrategyQueryConfig,
) (string, error) {
	if strategy.Spec.AliasName != "" {
		// 别名优先
		return strategy.Spec.AliasName, nil
	}
	if strategy.Spec.StrategyItem != nil && len(strategy.Spec.StrategyItem.QueryConfigs) != 0 ||
		strategy.ObjectModelCode == nil || *strategy.ObjectModelCode == "" {
		// 基于数据或多指标
		return strategy.Spec.Name, nil
	}
	tableID := query.ResultTableID
	if tableID == "" {
		tableID = strategy.Spec.TableID
	}
	if tableID == rules.SystemEventTableID {
		// 系统事件
		return item.Name, nil
	}
	metadata, found, err := scope.MetricLibrary(ctx, models.MetricLibraryQuery{
		TableID: tableID, FieldName: query.MetricField, ObjectModelCode: *strategy.ObjectModelCode,
	})
	if err != nil {
		return "", err
	}
	if found && metadata.FieldCNName != "" {
		return metadata.FieldCNName, nil
	}
	// MonitorMetric 表已经废弃；未命中 MetricLibrary 时沿用最终 metric_field 回退。
	return query.MetricField, nil
}
