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
)

// IntentClassifier 意图分类器
type IntentClassifier struct {
	intents map[string]*QueryIntent
}

// QueryIntent 查询意图
type QueryIntent struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Examples    []string `json:"examples"`
	Keywords    []string `json:"keywords"`
	Confidence  float64  `json:"confidence"`
}

// NewIntentClassifier 创建意图分类器
func NewIntentClassifier() *IntentClassifier {
	return &IntentClassifier{
		intents: initializeIntents(),
	}
}

// Classify 分类查询意图
func (ic *IntentClassifier) Classify(ctx context.Context, query string, context *QueryContext) (string, float64, error) {
	query = strings.ToLower(query)

	// 计算每个意图的匹配分数
	scores := make(map[string]float64)

	for intentName, intent := range ic.intents {
		score := ic.calculateScore(query, intent)
		scores[intentName] = score
	}

	// 找到最高分的意图
	var bestIntent string
	var bestScore float64

	for intentName, score := range scores {
		if score > bestScore {
			bestIntent = intentName
			bestScore = score
		}
	}

	// 如果最高分太低，返回未知意图
	if bestScore < 0.3 {
		return "unknown", bestScore, nil
	}

	return bestIntent, bestScore, nil
}

// calculateScore 计算意图匹配分数
func (ic *IntentClassifier) calculateScore(query string, intent *QueryIntent) float64 {
	score := 0.0
	totalKeywords := len(intent.Keywords)

	if totalKeywords == 0 {
		return 0.0
	}

	// 计算关键词匹配
	matchedKeywords := 0
	for _, keyword := range intent.Keywords {
		if strings.Contains(query, strings.ToLower(keyword)) {
			matchedKeywords++
		}
	}

	// 基础分数：匹配的关键词数量 / 总关键词数量
	score = float64(matchedKeywords) / float64(totalKeywords)

	// 如果匹配了所有关键词，给予额外分数
	if matchedKeywords == totalKeywords {
		score += 0.2
	}

	// 如果匹配了大部分关键词，给予额外分数
	if float64(matchedKeywords)/float64(totalKeywords) >= 0.7 {
		score += 0.1
	}

	return score
}

// initializeIntents 初始化预定义意图
func initializeIntents() map[string]*QueryIntent {
	return map[string]*QueryIntent{
		"cpu_usage": {
			Name:        "CPU使用率查询",
			Description: "查询CPU使用率相关指标",
			Keywords:    []string{"cpu", "使用率", "利用率", "负载", "load"},
			Examples: []string{
				"显示CPU使用率",
				"CPU负载最高的服务器",
				"平均CPU使用率",
				"查询CPU利用率",
			},
			Confidence: 0.8,
		},
		"memory_usage": {
			Name:        "内存使用率查询",
			Description: "查询内存使用率相关指标",
			Keywords:    []string{"内存", "memory", "使用率", "占用", "ram"},
			Examples: []string{
				"显示内存使用率",
				"内存占用最高的服务器",
				"平均内存使用率",
				"查询内存利用率",
			},
			Confidence: 0.8,
		},
		"disk_usage": {
			Name:        "磁盘使用率查询",
			Description: "查询磁盘使用率相关指标",
			Keywords:    []string{"磁盘", "disk", "存储", "使用率", "空间"},
			Examples: []string{
				"显示磁盘使用率",
				"磁盘空间不足的服务器",
				"平均磁盘使用率",
				"查询存储使用情况",
			},
			Confidence: 0.8,
		},
		"network_traffic": {
			Name:        "网络流量查询",
			Description: "查询网络流量相关指标",
			Keywords:    []string{"网络", "network", "流量", "带宽", "网速"},
			Examples: []string{
				"显示网络流量",
				"网络带宽使用情况",
				"网络流量统计",
				"查询网速",
			},
			Confidence: 0.8,
		},
		"error_rate": {
			Name:        "错误率查询",
			Description: "查询错误率相关指标",
			Keywords:    []string{"错误", "error", "错误率", "失败", "异常"},
			Examples: []string{
				"显示错误率",
				"错误率最高的服务",
				"平均错误率",
				"查询失败率",
			},
			Confidence: 0.8,
		},
		"response_time": {
			Name:        "响应时间查询",
			Description: "查询响应时间相关指标",
			Keywords:    []string{"响应时间", "response", "延迟", "latency", "耗时"},
			Examples: []string{
				"显示响应时间",
				"响应时间最慢的接口",
				"平均响应时间",
				"查询接口延迟",
			},
			Confidence: 0.8,
		},
		"throughput": {
			Name:        "吞吐量查询",
			Description: "查询吞吐量相关指标",
			Keywords:    []string{"吞吐量", "throughput", "qps", "tps", "并发"},
			Examples: []string{
				"显示吞吐量",
				"QPS统计",
				"TPS分析",
				"查询并发量",
			},
			Confidence: 0.8,
		},
		"top_servers": {
			Name:        "Top服务器查询",
			Description: "查询性能最高的服务器",
			Keywords:    []string{"最高", "top", "前", "排名", "排序"},
			Examples: []string{
				"CPU使用率最高的10台服务器",
				"内存占用前5的服务器",
				"网络流量最大的服务器",
				"响应时间最慢的服务器",
			},
			Confidence: 0.8,
		},
		"trend_analysis": {
			Name:        "趋势分析查询",
			Description: "查询数据趋势分析",
			Keywords:    []string{"趋势", "trend", "变化", "增长", "下降", "分析"},
			Examples: []string{
				"CPU使用率趋势",
				"内存使用变化",
				"网络流量增长趋势",
				"错误率变化分析",
			},
			Confidence: 0.8,
		},
		"comparison": {
			Name:        "对比查询",
			Description: "查询不同维度的对比数据",
			Keywords:    []string{"对比", "比较", "vs", "差异", "对比分析"},
			Examples: []string{
				"不同业务线的CPU使用率对比",
				"各服务器性能比较",
				"时间段对比分析",
				"服务间性能差异",
			},
			Confidence: 0.8,
		},
		"alert_analysis": {
			Name:        "告警分析查询",
			Description: "查询告警相关数据",
			Keywords:    []string{"告警", "alert", "报警", "异常", "问题"},
			Examples: []string{
				"最近的告警信息",
				"告警频率统计",
				"异常服务器列表",
				"问题分析报告",
			},
			Confidence: 0.8,
		},
		"capacity_planning": {
			Name:        "容量规划查询",
			Description: "查询容量规划相关数据",
			Keywords:    []string{"容量", "capacity", "规划", "扩容", "缩容"},
			Examples: []string{
				"服务器容量分析",
				"扩容建议",
				"资源使用预测",
				"容量规划报告",
			},
			Confidence: 0.8,
		},
		"unknown": {
			Name:        "未知意图",
			Description: "无法识别的查询意图",
			Keywords:    []string{},
			Examples:    []string{},
			Confidence:  0.0,
		},
	}
}
