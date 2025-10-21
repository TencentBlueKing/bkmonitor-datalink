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
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/pkg/errors"
)

// NLPProcessor 自然语言处理器
type NLPProcessor struct {
	llmClient        LLMClient
	intentClassifier *IntentClassifier
	entityExtractor  *EntityExtractor
}

// ParsedQuery 解析后的查询
type ParsedQuery struct {
	OriginalQuery string         `json:"original_query"`
	Intent        string         `json:"intent"`
	Entities      map[string]any `json:"entities"`
	Confidence    float64        `json:"confidence"`
	TimeRange     *TimeRange     `json:"time_range,omitempty"`
	Metrics       []string       `json:"metrics,omitempty"`
	Filters       []Filter       `json:"filters,omitempty"`
	Aggregations  []Aggregation  `json:"aggregations,omitempty"`
}

// Filter 过滤条件
type Filter struct {
	Field    string   `json:"field"`
	Operator string   `json:"operator"`
	Values   []string `json:"values"`
}

// Aggregation 聚合操作
type Aggregation struct {
	Function string   `json:"function"`
	Field    string   `json:"field"`
	GroupBy  []string `json:"group_by,omitempty"`
	Window   string   `json:"window,omitempty"`
}

// NewNLPProcessor 创建自然语言处理器
func NewNLPProcessor(llmClient LLMClient) *NLPProcessor {
	return &NLPProcessor{
		llmClient:        llmClient,
		intentClassifier: NewIntentClassifier(),
		entityExtractor:  NewEntityExtractor(),
	}
}

// ParseQuery 解析自然语言查询
func (p *NLPProcessor) ParseQuery(ctx context.Context, query string, context *QueryContext) (*ParsedQuery, error) {
	// 1. 意图识别
	intent, confidence, err := p.intentClassifier.Classify(ctx, query, context)
	if err != nil {
		return nil, errors.Wrap(err, "failed to classify intent")
	}

	// 2. 实体提取
	entities, err := p.entityExtractor.Extract(ctx, query, context)
	if err != nil {
		return nil, errors.Wrap(err, "failed to extract entities")
	}

	// 3. 时间范围解析
	timeRange, err := p.extractTimeRange(query)
	if err != nil {
		// 时间解析失败不是致命错误，使用默认值
		timeRange = &TimeRange{
			Start: "1h", // 默认最近1小时
			End:   "now",
			Step:  "1m",
		}
	}

	// 4. 指标提取
	metrics := p.extractMetrics(query, context)

	// 5. 过滤条件提取
	filters := p.extractFilters(query, context)

	// 6. 聚合操作提取
	aggregations := p.extractAggregations(query, context)

	// 7. 使用LLM进行深度解析（如果可用）
	if p.llmClient != nil {
		llmParsed, err := p.parseWithLLM(ctx, query, intent, entities)
		if err == nil {
			// 合并LLM解析结果
			if llmParsed.TimeRange != nil {
				timeRange = llmParsed.TimeRange
			}
			if len(llmParsed.Metrics) > 0 {
				metrics = llmParsed.Metrics
			}
			if len(llmParsed.Filters) > 0 {
				filters = llmParsed.Filters
			}
			if len(llmParsed.Aggregations) > 0 {
				aggregations = llmParsed.Aggregations
			}
		}
	}

	return &ParsedQuery{
		OriginalQuery: query,
		Intent:        intent,
		Entities:      entities,
		Confidence:    confidence,
		TimeRange:     timeRange,
		Metrics:       metrics,
		Filters:       filters,
		Aggregations:  aggregations,
	}, nil
}

// extractTimeRange 提取时间范围
func (p *NLPProcessor) extractTimeRange(query string) (*TimeRange, error) {
	// 时间相关的关键词映射
	timePatterns := map[string]*TimeRange{
		"最近1小时": {Start: "1h", End: "now", Step: "1m"},
		"最近1天":  {Start: "1d", End: "now", Step: "5m"},
		"最近1周":  {Start: "7d", End: "now", Step: "1h"},
		"最近1个月": {Start: "30d", End: "now", Step: "1h"},
		"最近1年":  {Start: "365d", End: "now", Step: "1d"},
		"今天":    {Start: "today", End: "now", Step: "5m"},
		"昨天":    {Start: "yesterday", End: "today", Step: "5m"},
		"本周":    {Start: "this_week", End: "now", Step: "1h"},
		"上周":    {Start: "last_week", End: "this_week", Step: "1h"},
		"本月":    {Start: "this_month", End: "now", Step: "1h"},
		"上月":    {Start: "last_month", End: "this_month", Step: "1h"},
	}

	// 检查是否包含时间关键词
	for keyword, timeRange := range timePatterns {
		if strings.Contains(query, keyword) {
			return timeRange, nil
		}
	}

	// 尝试解析相对时间表达式
	relativeTimePatterns := []struct {
		pattern *regexp.Regexp
		hours   int
	}{
		{regexp.MustCompile(`(\d+)\s*小时前`), 0},
		{regexp.MustCompile(`(\d+)\s*天前`), 0},
		{regexp.MustCompile(`(\d+)\s*周前`), 0},
		{regexp.MustCompile(`(\d+)\s*月前`), 0},
	}

	for _, pattern := range relativeTimePatterns {
		matches := pattern.pattern.FindStringSubmatch(query)
		if len(matches) > 1 {
			// 这里简化处理，实际应该根据具体数值计算
			return &TimeRange{
				Start: "1h",
				End:   "now",
				Step:  "1m",
			}, nil
		}
	}

	// 尝试解析绝对时间
	absoluteTimePattern := regexp.MustCompile(`(\d{4}-\d{2}-\d{2})\s*到\s*(\d{4}-\d{2}-\d{2})`)
	matches := absoluteTimePattern.FindStringSubmatch(query)
	if len(matches) > 2 {
		startTime, err := time.Parse("2006-01-02", matches[1])
		if err != nil {
			return nil, err
		}
		endTime, err := time.Parse("2006-01-02", matches[2])
		if err != nil {
			return nil, err
		}

		return &TimeRange{
			Start: fmt.Sprintf("%d", startTime.Unix()),
			End:   fmt.Sprintf("%d", endTime.Unix()),
			Step:  "1h",
		}, nil
	}

	return nil, errors.New("no time range found")
}

// extractMetrics 提取指标
func (p *NLPProcessor) extractMetrics(query string, context *QueryContext) []string {
	var metrics []string

	// 从上下文中获取可用指标
	availableMetrics := make(map[string]bool)
	for _, metric := range context.AvailableMetrics {
		availableMetrics[metric.Name] = true
	}

	// 常见的指标关键词
	metricKeywords := []string{
		"CPU", "cpu", "内存", "memory", "磁盘", "disk", "网络", "network",
		"连接数", "connections", "请求数", "requests", "响应时间", "response_time",
		"错误率", "error_rate", "成功率", "success_rate", "吞吐量", "throughput",
		"延迟", "latency", "QPS", "TPS", "并发", "concurrency",
	}

	for _, keyword := range metricKeywords {
		if strings.Contains(query, keyword) {
			// 检查是否在可用指标中
			for metricName := range availableMetrics {
				if strings.Contains(strings.ToLower(metricName), strings.ToLower(keyword)) {
					metrics = append(metrics, metricName)
				}
			}
		}
	}

	// 如果没有找到匹配的指标，返回空列表
	return metrics
}

// extractFilters 提取过滤条件
func (p *NLPProcessor) extractFilters(query string, context *QueryContext) []Filter {
	var filters []Filter

	// 服务器相关的过滤条件
	serverPatterns := []struct {
		pattern  *regexp.Regexp
		operator string
	}{
		{regexp.MustCompile(`(\d+)\s*台服务器`), "limit"},
		{regexp.MustCompile(`前\s*(\d+)\s*台`), "limit"},
		{regexp.MustCompile(`最高的\s*(\d+)\s*台`), "limit"},
		{regexp.MustCompile(`最低的\s*(\d+)\s*台`), "limit"},
	}

	for _, pattern := range serverPatterns {
		matches := pattern.pattern.FindStringSubmatch(query)
		if len(matches) > 1 {
			filters = append(filters, Filter{
				Field:    "limit",
				Operator: pattern.operator,
				Values:   []string{matches[1]},
			})
		}
	}

	// 业务相关的过滤条件
	businessPatterns := []struct {
		pattern  *regexp.Regexp
		field    string
		operator string
	}{
		{regexp.MustCompile(`业务\s*(\d+)`), "bk_biz_id", "eq"},
		{regexp.MustCompile(`应用\s*(\w+)`), "app_name", "eq"},
		{regexp.MustCompile(`服务\s*(\w+)`), "service_name", "eq"},
	}

	for _, pattern := range businessPatterns {
		matches := pattern.pattern.FindStringSubmatch(query)
		if len(matches) > 1 {
			filters = append(filters, Filter{
				Field:    pattern.field,
				Operator: pattern.operator,
				Values:   []string{matches[1]},
			})
		}
	}

	return filters
}

// extractAggregations 提取聚合操作
func (p *NLPProcessor) extractAggregations(query string, context *QueryContext) []Aggregation {
	var aggregations []Aggregation

	// 聚合函数关键词映射
	aggKeywords := map[string]string{
		"平均": "avg", "平均值": "avg", "均值": "avg",
		"最大": "max", "最大值": "max", "最高": "max",
		"最小": "min", "最小值": "min", "最低": "min",
		"总和": "sum", "总计": "sum", "合计": "sum",
		"计数": "count", "数量": "count", "个数": "count",
		"中位数": "median", "中值": "median",
		"标准差": "stddev", "方差": "variance",
	}

	// 时间窗口关键词
	windowKeywords := map[string]string{
		"每分钟": "1m", "每5分钟": "5m", "每10分钟": "10m",
		"每小时": "1h", "每2小时": "2h", "每6小时": "6h",
		"每天": "1d", "每2天": "2d", "每周": "1w",
	}

	// 查找聚合函数
	for keyword, function := range aggKeywords {
		if strings.Contains(query, keyword) {
			agg := Aggregation{
				Function: function,
				Field:    "", // 将在后续处理中填充
			}

			// 查找时间窗口
			for windowKeyword, window := range windowKeywords {
				if strings.Contains(query, windowKeyword) {
					agg.Window = window
					break
				}
			}

			aggregations = append(aggregations, agg)
		}
	}

	return aggregations
}

// parseWithLLM 使用LLM进行深度解析
func (p *NLPProcessor) parseWithLLM(ctx context.Context, query, intent string, entities map[string]any) (*ParsedQuery, error) {
	prompt := fmt.Sprintf(`
请解析以下监控查询的自然语言描述，并提取关键信息：

查询: %s
意图: %s
实体: %v

请以JSON格式返回解析结果，包含以下字段：
- time_range: 时间范围 {start, end, step}
- metrics: 指标列表
- filters: 过滤条件 [{field, operator, values}]
- aggregations: 聚合操作 [{function, field, group_by, window}]

示例格式：
{
  "time_range": {"start": "1h", "end": "now", "step": "1m"},
  "metrics": ["cpu_usage", "memory_usage"],
  "filters": [{"field": "limit", "operator": "limit", "values": ["10"]}],
  "aggregations": [{"function": "avg", "field": "cpu_usage", "window": "5m"}]
}
`, query, intent, entities)

	response, err := p.llmClient.ChatCompletion(ctx, []Message{
		{
			Role:    "system",
			Content: "你是一个监控数据专家，请解析自然语言查询并提取关键信息。只返回JSON格式的结果，不要包含其他内容。",
		},
		{
			Role:    "user",
			Content: prompt,
		},
	})
	if err != nil {
		return nil, err
	}

	// 解析JSON响应
	var result struct {
		TimeRange    *TimeRange    `json:"time_range"`
		Metrics      []string      `json:"metrics"`
		Filters      []Filter      `json:"filters"`
		Aggregations []Aggregation `json:"aggregations"`
	}

	if err := json.Unmarshal([]byte(response.Content), &result); err != nil {
		return nil, errors.Wrap(err, "failed to parse LLM response")
	}

	return &ParsedQuery{
		TimeRange:    result.TimeRange,
		Metrics:      result.Metrics,
		Filters:      result.Filters,
		Aggregations: result.Aggregations,
	}, nil
}
