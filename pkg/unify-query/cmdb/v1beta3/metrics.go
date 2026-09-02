// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"errors"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// 可观测指标：binding 解析 + surrealdb 查询 + 错误分类
var (
	queryDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Subsystem: "cmdb_v1beta3",
			Name:      "surrealdb_query_duration_seconds",
			Help:      "Duration of SurrealDB graph queries routed via bkbase query_sync.",
			Buckets:   prometheus.ExponentialBuckets(0.01, 2, 12), // 10ms ~ 40s
		},
		[]string{"space_uid", "status"},
	)

	bindingLookupTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Subsystem: "cmdb_v1beta3",
			Name:      "binding_lookup_total",
			Help:      "Total SurrealDBBinding lookups by result (hit_cache / miss_cache / not_found / error).",
		},
		[]string{"space_uid", "result"},
	)

	bindingCacheSize = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Namespace: "unify_query",
			Subsystem: "cmdb_v1beta3",
			Name:      "binding_cache_size",
			Help:      "Number of bk_biz_id entries currently in the binding cache.",
		},
	)

	errorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Subsystem: "cmdb_v1beta3",
			Name:      "errors_total",
			Help:      "v1beta3 query errors classified by kind.",
		},
		[]string{"space_uid", "error_kind"},
	)
)

func init() {
	prometheus.MustRegister(
		queryDurationSeconds,
		bindingLookupTotal,
		bindingCacheSize,
		errorsTotal,
	)
}

// ObserveQueryDuration 记录一次 SurrealDB 查询耗时。
func ObserveQueryDuration(spaceUID, status string, seconds float64) {
	queryDurationSeconds.WithLabelValues(spaceUID, status).Observe(seconds)
}

// ObserveBindingLookup 记录一次 binding 查找结果。
// result 枚举：hit_cache / miss_cache / not_found / error。
func ObserveBindingLookup(spaceUID, result string) {
	bindingLookupTotal.WithLabelValues(spaceUID, result).Inc()
}

// ObserveBindingCacheSize 刷新 binding 缓存条数。
func ObserveBindingCacheSize(size int) {
	bindingCacheSize.Set(float64(size))
}

// ObserveError 记录一次分类后的错误。
func ObserveError(spaceUID, errorKind string) {
	errorsTotal.WithLabelValues(spaceUID, errorKind).Inc()
}

// CategorizeError 按错误类型分桶，便于告警和 metric label 归并。
//
// 枚举：
//
//	no_binding   —— space 没有找到可用的 SurrealDBBinding（见 BindingLookupError）
//	bkbase_4xx   —— bkbase 返回 4xx（鉴权/请求问题）
//	bkbase_5xx   —— bkbase 返回 5xx（上游不可达）
//	dsl_syntax   —— SurrealQL 语法问题（bkbase 能解析但 SurrealDB 拒绝）
//	parse        —— 响应解析失败（Graph 反序列化）
//	timeout      —— 请求超时
//	result_limit —— 查询结果超过安全上限
//	unknown      —— 未分类
func CategorizeError(err error) string {
	if err == nil {
		return ""
	}
	var bindingErr *BindingLookupError
	if errors.As(err, &bindingErr) {
		return "no_binding"
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return "timeout"
	case strings.Contains(msg, "result limit exceeded") || strings.Contains(msg, "response body exceeds maximum size"):
		return "result_limit"
	case strings.Contains(msg, "parse") || strings.Contains(msg, "unmarshal"):
		return "parse"
	case strings.Contains(msg, "bkbase response error"):
		// 响应 result=false 的 bkbase 错误；5xx / 4xx 无法从 message 精确区分，
		// 这里统一分到 bkbase_4xx（大部分是配置或 DSL 错）。
		if strings.Contains(msg, "syntax") || strings.Contains(msg, "parse") {
			return "dsl_syntax"
		}
		return "bkbase_4xx"
	case strings.Contains(msg, "bkbase request failed"):
		// HTTP 层错误：连接不上 / 5xx
		return "bkbase_5xx"
	default:
		return "unknown"
	}
}

// ObserveErrorFromErr 是 ObserveError 的便捷封装：按 CategorizeError 归类 +1。
func ObserveErrorFromErr(_ context.Context, spaceUID string, err error) {
	if err == nil {
		return
	}
	ObserveError(spaceUID, CategorizeError(err))
}
