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
	"regexp"
	"strings"
)

// EntityExtractor 实体提取器
type EntityExtractor struct {
	patterns map[string][]*regexp.Regexp
}

// NewEntityExtractor 创建实体提取器
func NewEntityExtractor() *EntityExtractor {
	return &EntityExtractor{
		patterns: initializeEntityPatterns(),
	}
}

// Extract 提取实体
func (ee *EntityExtractor) Extract(ctx context.Context, query string, context *QueryContext) (map[string]any, error) {
	entities := make(map[string]any)
	query = strings.ToLower(query)

	// 提取各种类型的实体
	entities["metrics"] = ee.extractMetrics(query, context)
	entities["servers"] = ee.extractServers(query, context)
	entities["businesses"] = ee.extractBusinesses(query, context)
	entities["applications"] = ee.extractApplications(query, context)
	entities["time_periods"] = ee.extractTimePeriods(query)
	entities["numbers"] = ee.extractNumbers(query)
	entities["operators"] = ee.extractOperators(query)

	return entities, nil
}

// extractMetrics 提取指标实体
func (ee *EntityExtractor) extractMetrics(query string, context *QueryContext) []string {
	var metrics []string

	// 从上下文中获取可用指标
	availableMetrics := make(map[string]bool)
	for _, metric := range context.AvailableMetrics {
		availableMetrics[strings.ToLower(metric.Name)] = true
	}

	// 指标关键词映射
	metricKeywords := map[string][]string{
		"cpu":     {"cpu", "处理器", "计算", "load", "负载"},
		"memory":  {"内存", "memory", "ram", "存储"},
		"disk":    {"磁盘", "disk", "存储", "空间", "容量"},
		"network": {"网络", "network", "流量", "带宽", "网速"},
		"error":   {"错误", "error", "失败", "异常", "故障"},
		"time":    {"时间", "time", "响应", "延迟", "latency"},
		"count":   {"数量", "count", "次数", "计数", "qps", "tps"},
	}

	// 查找匹配的指标
	for metricType, keywords := range metricKeywords {
		for _, keyword := range keywords {
			if strings.Contains(query, keyword) {
				// 检查是否有具体的指标名称匹配
				for metricName := range availableMetrics {
					if strings.Contains(metricName, keyword) || strings.Contains(metricName, metricType) {
						metrics = append(metrics, metricName)
					}
				}
				// 如果没有找到具体匹配，添加通用类型
				if len(metrics) == 0 {
					metrics = append(metrics, metricType)
				}
			}
		}
	}

	return metrics
}

// extractServers 提取服务器实体
func (ee *EntityExtractor) extractServers(query string, context *QueryContext) []string {
	var servers []string

	// 服务器相关的关键词
	serverKeywords := []string{
		"服务器", "server", "主机", "host", "机器", "machine",
		"节点", "node", "实例", "instance",
	}

	// 检查是否包含服务器相关关键词
	for _, keyword := range serverKeywords {
		if strings.Contains(query, keyword) {
			servers = append(servers, keyword)
		}
	}

	// 提取服务器数量
	numberPattern := regexp.MustCompile(`(\d+)\s*台`)
	matches := numberPattern.FindStringSubmatch(query)
	if len(matches) > 1 {
		servers = append(servers, "count:"+matches[1])
	}

	return servers
}

// extractBusinesses 提取业务实体
func (ee *EntityExtractor) extractBusinesses(query string, context *QueryContext) []string {
	var businesses []string

	// 业务相关的关键词
	businessKeywords := []string{
		"业务", "business", "项目", "project", "产品", "product",
		"系统", "system", "服务", "service",
	}

	// 检查是否包含业务相关关键词
	for _, keyword := range businessKeywords {
		if strings.Contains(query, keyword) {
			businesses = append(businesses, keyword)
		}
	}

	// 提取具体的业务ID
	bizIDPattern := regexp.MustCompile(`业务\s*(\d+)`)
	matches := bizIDPattern.FindStringSubmatch(query)
	if len(matches) > 1 {
		businesses = append(businesses, "biz_id:"+matches[1])
	}

	return businesses
}

// extractApplications 提取应用实体
func (ee *EntityExtractor) extractApplications(query string, context *QueryContext) []string {
	var applications []string

	// 应用相关的关键词
	appKeywords := []string{
		"应用", "application", "app", "程序", "program",
		"服务", "service", "接口", "api", "api",
	}

	// 检查是否包含应用相关关键词
	for _, keyword := range appKeywords {
		if strings.Contains(query, keyword) {
			applications = append(applications, keyword)
		}
	}

	// 提取具体的应用名称
	appNamePattern := regexp.MustCompile(`应用\s*(\w+)`)
	matches := appNamePattern.FindStringSubmatch(query)
	if len(matches) > 1 {
		applications = append(applications, "app_name:"+matches[1])
	}

	return applications
}

// extractTimePeriods 提取时间周期实体
func (ee *EntityExtractor) extractTimePeriods(query string) []string {
	var periods []string

	// 时间周期关键词映射
	timeKeywords := map[string]string{
		"最近1小时": "1h",
		"最近1天":  "1d",
		"最近1周":  "7d",
		"最近1个月": "30d",
		"最近1年":  "365d",
		"今天":    "today",
		"昨天":    "yesterday",
		"本周":    "this_week",
		"上周":    "last_week",
		"本月":    "this_month",
		"上月":    "last_month",
		"每分钟":   "1m",
		"每5分钟":  "5m",
		"每10分钟": "10m",
		"每小时":   "1h",
		"每2小时":  "2h",
		"每6小时":  "6h",
		"每天":    "1d",
		"每2天":   "2d",
		"每周":    "1w",
	}

	// 查找时间周期关键词
	for keyword, period := range timeKeywords {
		if strings.Contains(query, keyword) {
			periods = append(periods, period)
		}
	}

	return periods
}

// extractNumbers 提取数字实体
func (ee *EntityExtractor) extractNumbers(query string) []string {
	var numbers []string

	// 提取所有数字
	numberPattern := regexp.MustCompile(`\d+`)
	matches := numberPattern.FindAllString(query, -1)

	for _, match := range matches {
		numbers = append(numbers, match)
	}

	return numbers
}

// extractOperators 提取操作符实体
func (ee *EntityExtractor) extractOperators(query string) []string {
	var operators []string

	// 操作符关键词映射
	operatorKeywords := map[string]string{
		"最高":  "max",
		"最大":  "max",
		"最低":  "min",
		"最小":  "min",
		"平均":  "avg",
		"平均值": "avg",
		"总和":  "sum",
		"总计":  "sum",
		"计数":  "count",
		"数量":  "count",
		"中位数": "median",
		"中值":  "median",
		"标准差": "stddev",
		"方差":  "variance",
		"前":   "top",
		"后":   "bottom",
		"排序":  "sort",
		"分组":  "group",
		"过滤":  "filter",
		"筛选":  "filter",
	}

	// 查找操作符关键词
	for keyword, operator := range operatorKeywords {
		if strings.Contains(query, keyword) {
			operators = append(operators, operator)
		}
	}

	return operators
}

// initializeEntityPatterns 初始化实体模式
func initializeEntityPatterns() map[string][]*regexp.Regexp {
	return map[string][]*regexp.Regexp{
		"ip_address": {
			regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`),
		},
		"port": {
			regexp.MustCompile(`端口\s*(\d+)`),
			regexp.MustCompile(`:(\d+)\b`),
		},
		"percentage": {
			regexp.MustCompile(`(\d+(?:\.\d+)?)\s*%`),
			regexp.MustCompile(`(\d+(?:\.\d+)?)\s*percent`),
		},
		"file_size": {
			regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(GB|MB|KB|TB)`),
		},
		"time_duration": {
			regexp.MustCompile(`(\d+)\s*(秒|分钟|小时|天|周|月|年)`),
			regexp.MustCompile(`(\d+)\s*(s|m|h|d|w|M|y)`),
		},
	}
}
