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
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// QueryOptimizer 查询优化器
type QueryOptimizer struct {
	knowledgeBase *KnowledgeBase
}

// NewQueryOptimizer 创建查询优化器
func NewQueryOptimizer(knowledgeBase *KnowledgeBase) *QueryOptimizer {
	return &QueryOptimizer{
		knowledgeBase: knowledgeBase,
	}
}

// Optimize 优化查询
func (qo *QueryOptimizer) Optimize(ctx context.Context, query *structured.QueryTs, context *QueryContext) (*structured.QueryTs, error) {
	optimizedQuery := query

	// 1. 时间范围优化
	optimizedQuery = qo.optimizeTimeRange(ctx, optimizedQuery, context)

	// 2. 聚合函数优化
	optimizedQuery = qo.optimizeAggregations(ctx, optimizedQuery, context)

	// 3. 过滤条件优化
	optimizedQuery = qo.optimizeFilters(ctx, optimizedQuery, context)

	// 4. 查询结构优化
	optimizedQuery = qo.optimizeQueryStructure(ctx, optimizedQuery, context)

	// 5. 性能优化
	optimizedQuery = qo.optimizePerformance(ctx, optimizedQuery, context)

	return optimizedQuery, nil
}

// optimizeTimeRange 优化时间范围
func (qo *QueryOptimizer) optimizeTimeRange(ctx context.Context, query *structured.QueryTs, context *QueryContext) *structured.QueryTs {
	// 检查时间范围是否合理
	if query.Start == "" || query.End == "" {
		return query
	}

	// 解析时间范围
	startTime, err := qo.parseTime(query.Start)
	if err != nil {
		return query
	}

	endTime, err := qo.parseTime(query.End)
	if err != nil {
		return query
	}

	// 检查时间范围是否过大
	timeRange := endTime.Sub(startTime)
	if timeRange > 30*24*time.Hour { // 超过30天
		// 建议使用更小的时间范围
		query.Start = "7d"
		query.End = "now"
	}

	// 检查时间范围是否过小
	if timeRange < time.Minute { // 小于1分钟
		// 建议使用更大的时间范围
		query.Start = "1h"
		query.End = "now"
	}

	return query
}

// optimizeAggregations 优化聚合函数
func (qo *QueryOptimizer) optimizeAggregations(ctx context.Context, query *structured.QueryTs, context *QueryContext) *structured.QueryTs {
	for _, q := range query.QueryList {
		// 检查是否有不必要的聚合函数
		if len(q.AggregateMethodList) > 0 {
			// 如果只有一个聚合函数且是avg，可以考虑移除
			if len(q.AggregateMethodList) == 1 && q.AggregateMethodList[0].Method == "avg" {
				// 检查是否有时间聚合
				if q.TimeAggregation.Function != "" {
					// 如果时间聚合已经提供了平均值，可以移除空间聚合
					q.AggregateMethodList = nil
				}
			}
		}

		// 优化时间聚合函数
		if q.TimeAggregation.Function != "" {
			// 根据时间范围选择合适的聚合函数
			timeRange := qo.getTimeRange(query)
			if timeRange > 24*time.Hour {
				// 长时间范围，使用更粗粒度的聚合
				if q.TimeAggregation.Function == "avg_over_time" {
					q.TimeAggregation.Function = "avg_over_time"
					q.TimeAggregation.Window = "1h"
				}
			} else if timeRange < time.Hour {
				// 短时间范围，使用更细粒度的聚合
				if q.TimeAggregation.Function == "avg_over_time" {
					q.TimeAggregation.Window = "1m"
				}
			}
		}
	}

	return query
}

// optimizeFilters 优化过滤条件
func (qo *QueryOptimizer) optimizeFilters(ctx context.Context, query *structured.QueryTs, context *QueryContext) *structured.QueryTs {
	for _, q := range query.QueryList {
		// 检查过滤条件是否合理
		if len(q.Conditions.FieldList) > 0 {
			// 移除空的过滤条件
			var filteredFields []structured.ConditionField
			for _, field := range q.Conditions.FieldList {
				if field.DimensionName != "" && len(field.Value) > 0 {
					filteredFields = append(filteredFields, field)
				}
			}
			q.Conditions.FieldList = filteredFields

			// 优化过滤条件的顺序
			q.Conditions.FieldList = qo.sortFilterConditions(q.Conditions.FieldList)
		}
	}

	return query
}

// optimizeQueryStructure 优化查询结构
func (qo *QueryOptimizer) optimizeQueryStructure(ctx context.Context, query *structured.QueryTs, context *QueryContext) *structured.QueryTs {
	// 检查是否有重复的查询
	uniqueQueries := make(map[string]*structured.Query)
	for _, q := range query.QueryList {
		key := fmt.Sprintf("%s_%s", q.TableID, q.FieldName)
		if _, exists := uniqueQueries[key]; !exists {
			uniqueQueries[key] = q
		}
	}

	// 重建查询列表
	query.QueryList = make([]*structured.Query, 0, len(uniqueQueries))
	for _, q := range uniqueQueries {
		query.QueryList = append(query.QueryList, q)
	}

	return query
}

// optimizePerformance 优化性能
func (qo *QueryOptimizer) optimizePerformance(ctx context.Context, query *structured.QueryTs, context *QueryContext) *structured.QueryTs {
	// 检查是否为慢查询
	if qo.knowledgeBase.IsSlowQuery(query) {
		// 添加限制条件
		for _, q := range query.QueryList {
			if q.Limit == 0 {
				q.Limit = 1000 // 默认限制1000条记录
			}
		}
	}

	// 检查是否有高基数标签
	if qo.knowledgeBase.HasHighCardinality(query) {
		// 添加过滤条件减少基数
		for _, q := range query.QueryList {
			if len(q.Conditions.FieldList) == 0 {
				// 添加默认过滤条件
				q.Conditions.FieldList = append(q.Conditions.FieldList, structured.ConditionField{
					DimensionName: "bk_biz_id",
					Value:         []string{"2"}, // 默认业务ID
					Operator:      structured.ConditionEqual,
				})
				q.Conditions.ConditionList = append(q.Conditions.ConditionList, "and")
			}
		}
	}

	return query
}

// parseTime 解析时间字符串
func (qo *QueryOptimizer) parseTime(timeStr string) (time.Time, error) {
	// 处理相对时间
	if strings.HasSuffix(timeStr, "h") {
		hours := strings.TrimSuffix(timeStr, "h")
		if hours == "now" {
			return time.Now(), nil
		}
		// 这里可以添加更复杂的相对时间解析
		return time.Now().Add(-time.Hour), nil
	}

	if strings.HasSuffix(timeStr, "d") {
		days := strings.TrimSuffix(timeStr, "d")
		if days == "now" {
			return time.Now(), nil
		}
		// 这里可以添加更复杂的相对时间解析
		return time.Now().Add(-24 * time.Hour), nil
	}

	// 处理绝对时间戳
	if len(timeStr) == 10 {
		// 秒级时间戳
		return time.Unix(0, 0), nil
	}

	return time.Now(), errors.New("unsupported time format")
}

// getTimeRange 获取时间范围
func (qo *QueryOptimizer) getTimeRange(query *structured.QueryTs) time.Duration {
	if query.Start == "" || query.End == "" {
		return 0
	}

	startTime, err := qo.parseTime(query.Start)
	if err != nil {
		return 0
	}

	endTime, err := qo.parseTime(query.End)
	if err != nil {
		return 0
	}

	return endTime.Sub(startTime)
}

// sortFilterConditions 排序过滤条件
func (qo *QueryOptimizer) sortFilterConditions(conditions []structured.ConditionField) []structured.ConditionField {
	// 按照条件的重要性排序
	priority := map[string]int{
		"bk_biz_id":          1,
		"bk_target_ip":       2,
		"bk_target_cloud_id": 3,
		"app_name":           4,
		"service_name":       5,
	}

	// 简单的冒泡排序
	for i := 0; i < len(conditions)-1; i++ {
		for j := 0; j < len(conditions)-i-1; j++ {
			priority1 := priority[conditions[j].DimensionName]
			priority2 := priority[conditions[j+1].DimensionName]

			if priority1 > priority2 {
				conditions[j], conditions[j+1] = conditions[j+1], conditions[j]
			}
		}
	}

	return conditions
}
