// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ai_agent

import (
	"context"
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// QueryBuilder 查询构建器
type QueryBuilder struct {
	knowledgeBase *KnowledgeBase
}

// NewQueryBuilder 创建查询构建器
func NewQueryBuilder(knowledgeBase *KnowledgeBase) *QueryBuilder {
	return &QueryBuilder{
		knowledgeBase: knowledgeBase,
	}
}

// BuildQuery 构建结构化查询
func (qb *QueryBuilder) BuildQuery(ctx context.Context, parsedQuery *ParsedQuery, req *NaturalLanguageQueryRequest) (*structured.QueryTs, error) {
	// 1. 确定表ID
	tableID, err := qb.determineTableID(ctx, parsedQuery, req.Context)
	if err != nil {
		return nil, errors.Wrap(err, "failed to determine table ID")
	}

	// 2. 确定字段名
	fieldName, err := qb.determineFieldName(ctx, parsedQuery, req.Context)
	if err != nil {
		return nil, errors.Wrap(err, "failed to determine field name")
	}

	// 3. 构建查询条件
	conditions, err := qb.buildConditions(ctx, parsedQuery, req.Context)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build conditions")
	}

	// 4. 构建聚合函数
	aggregateMethods, err := qb.buildAggregateMethods(ctx, parsedQuery)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build aggregate methods")
	}

	// 5. 构建时间聚合
	timeAggregation, err := qb.buildTimeAggregation(ctx, parsedQuery)
	if err != nil {
		return nil, errors.Wrap(err, "failed to build time aggregation")
	}

	// 6. 构建查询结构
	query := &structured.QueryTs{
		SpaceUid: req.SpaceUID,
		QueryList: []*structured.Query{
			{
				TableID:             structured.TableID(tableID),
				FieldName:           fieldName,
				IsRegexp:            false,
				AggregateMethodList: aggregateMethods,
				TimeAggregation:     timeAggregation,
				ReferenceName:       "a",
				Conditions:          conditions,
			},
		},
		MetricMerge: "a",
		Start:       req.TimeRange.Start,
		End:         req.TimeRange.End,
		Step:        req.TimeRange.Step,
	}

	// 7. 设置默认值
	if query.Start == "" {
		query.Start = "1h"
	}
	if query.End == "" {
		query.End = "now"
	}
	if query.Step == "" {
		query.Step = "1m"
	}

	return query, nil
}

// determineTableID 确定表ID
func (qb *QueryBuilder) determineTableID(ctx context.Context, parsedQuery *ParsedQuery, context *QueryContext) (string, error) {
	// 基于意图和指标确定表ID
	intent := parsedQuery.Intent
	metrics := parsedQuery.Metrics

	// 意图到表ID的映射
	intentTableMap := map[string]string{
		"cpu_usage":         "system.cpu_summary",
		"memory_usage":      "system.memory_summary",
		"disk_usage":        "system.disk_summary",
		"network_traffic":   "system.network_summary",
		"error_rate":        "application.error_summary",
		"response_time":     "application.response_time_summary",
		"throughput":        "application.throughput_summary",
		"top_servers":       "system.cpu_summary",
		"trend_analysis":    "system.cpu_summary",
		"comparison":        "system.cpu_summary",
		"alert_analysis":    "system.alert_summary",
		"capacity_planning": "system.cpu_summary",
	}

	// 首先尝试基于意图确定表ID
	if tableID, exists := intentTableMap[intent]; exists {
		return tableID, nil
	}

	// 如果基于意图无法确定，尝试基于指标确定
	if len(metrics) > 0 {
		metric := metrics[0]
		metricTableMap := map[string]string{
			"cpu":     "system.cpu_summary",
			"memory":  "system.memory_summary",
			"disk":    "system.disk_summary",
			"network": "system.network_summary",
			"error":   "application.error_summary",
			"time":    "application.response_time_summary",
			"count":   "application.throughput_summary",
		}

		for metricType, tableID := range metricTableMap {
			if strings.Contains(strings.ToLower(metric), metricType) {
				return tableID, nil
			}
		}
	}

	// 如果仍然无法确定，使用默认表
	return "system.cpu_summary", nil
}

// determineFieldName 确定字段名
func (qb *QueryBuilder) determineFieldName(ctx context.Context, parsedQuery *ParsedQuery, context *QueryContext) (string, error) {
	// 基于意图和指标确定字段名
	intent := parsedQuery.Intent
	metrics := parsedQuery.Metrics

	// 意图到字段名的映射
	intentFieldMap := map[string]string{
		"cpu_usage":         "usage",
		"memory_usage":      "usage",
		"disk_usage":        "usage",
		"network_traffic":   "bytes_recv",
		"error_rate":        "error_count",
		"response_time":     "response_time",
		"throughput":        "request_count",
		"top_servers":       "usage",
		"trend_analysis":    "usage",
		"comparison":        "usage",
		"alert_analysis":    "alert_count",
		"capacity_planning": "usage",
	}

	// 首先尝试基于意图确定字段名
	if fieldName, exists := intentFieldMap[intent]; exists {
		return fieldName, nil
	}

	// 如果基于意图无法确定，尝试基于指标确定
	if len(metrics) > 0 {
		metric := metrics[0]
		metricFieldMap := map[string]string{
			"cpu":     "usage",
			"memory":  "usage",
			"disk":    "usage",
			"network": "bytes_recv",
			"error":   "error_count",
			"time":    "response_time",
			"count":   "request_count",
		}

		for metricType, fieldName := range metricFieldMap {
			if strings.Contains(strings.ToLower(metric), metricType) {
				return fieldName, nil
			}
		}
	}

	// 如果仍然无法确定，使用默认字段
	return "usage", nil
}

// buildConditions 构建查询条件
func (qb *QueryBuilder) buildConditions(ctx context.Context, parsedQuery *ParsedQuery, context *QueryContext) (structured.Conditions, error) {
	conditions := structured.Conditions{
		FieldList:     []structured.ConditionField{},
		ConditionList: []string{},
	}

	// 处理过滤条件
	for _, filter := range parsedQuery.Filters {
		conditionField := structured.ConditionField{
			DimensionName: filter.Field,
			Value:         filter.Values,
			Operator:      qb.mapOperator(filter.Operator),
		}
		conditions.FieldList = append(conditions.FieldList, conditionField)
		conditions.ConditionList = append(conditions.ConditionList, "and")
	}

	// 处理业务ID过滤
	if len(context.AvailableMetrics) > 0 {
		// 这里可以根据实际业务逻辑添加业务ID过滤
		// 例如：conditions.Append(structured.ConditionField{
		//     DimensionName: "bk_biz_id",
		//     Value:         []string{"2"},
		//     Operator:      "eq",
		// }, "and")
	}

	return conditions, nil
}

// buildAggregateMethods 构建聚合方法
func (qb *QueryBuilder) buildAggregateMethods(ctx context.Context, parsedQuery *ParsedQuery) (structured.AggregateMethodList, error) {
	var methods structured.AggregateMethodList

	// 处理聚合操作
	for _, agg := range parsedQuery.Aggregations {
		method := structured.AggregateMethod{
			Method:     agg.Function,
			Field:      agg.Field,
			Dimensions: agg.GroupBy,
			Without:    false,
		}
		methods = append(methods, method)
	}

	// 如果没有聚合操作，添加默认聚合
	if len(methods) == 0 {
		methods = append(methods, structured.AggregateMethod{
			Method:     "avg",
			Field:      "",
			Dimensions: []string{},
			Without:    false,
		})
	}

	return methods, nil
}

// buildTimeAggregation 构建时间聚合
func (qb *QueryBuilder) buildTimeAggregation(ctx context.Context, parsedQuery *ParsedQuery) (structured.TimeAggregation, error) {
	// 确定时间窗口
	window := "5m" // 默认5分钟窗口

	// 从时间范围中提取窗口
	if parsedQuery.TimeRange != nil && parsedQuery.TimeRange.Step != "" {
		window = parsedQuery.TimeRange.Step
	}

	// 从聚合操作中提取窗口
	for _, agg := range parsedQuery.Aggregations {
		if agg.Window != "" {
			window = agg.Window
			break
		}
	}

	// 确定时间聚合函数
	function := "avg_over_time" // 默认平均值

	// 基于意图确定时间聚合函数
	intentFunctionMap := map[string]string{
		"cpu_usage":         "avg_over_time",
		"memory_usage":      "avg_over_time",
		"disk_usage":        "avg_over_time",
		"network_traffic":   "rate",
		"error_rate":        "rate",
		"response_time":     "avg_over_time",
		"throughput":        "rate",
		"top_servers":       "avg_over_time",
		"trend_analysis":    "avg_over_time",
		"comparison":        "avg_over_time",
		"alert_analysis":    "count_over_time",
		"capacity_planning": "avg_over_time",
	}

	if funcName, exists := intentFunctionMap[parsedQuery.Intent]; exists {
		function = funcName
	}

	return structured.TimeAggregation{
		Function: function,
		Window:   structured.Window(window),
		Position: 0,
	}, nil
}

// mapOperator 映射操作符
func (qb *QueryBuilder) mapOperator(operator string) string {
	operatorMap := map[string]string{
		"eq":       "eq",
		"ne":       "ne",
		"gt":       "gt",
		"gte":      "gte",
		"lt":       "lt",
		"lte":      "lte",
		"in":       "in",
		"not_in":   "not_in",
		"contains": "contains",
		"regex":    "regex",
		"limit":    "eq", // 限制条件使用等于操作符
	}

	if op, exists := operatorMap[operator]; exists {
		return op
	}

	return "eq" // 默认使用等于操作符
}
