// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// 请求结果，封闭集合；判定顺序见 RequestOutcomes。
const (
	OutcomeCanceled      = "canceled"
	OutcomeUnauthorized  = "unauthorized"
	OutcomeInternal      = "internal"
	OutcomeTimeout       = "timeout"
	OutcomeUpstreamError = "upstream_error"
	OutcomeClientError   = "client_error"
	OutcomePartial       = "partial"
	OutcomeSuccess       = "success"

	// HandlerUnmatched 是没有匹配到任何注册路由的请求的 handler 标签。
	HandlerUnmatched = "unmatched"
)

// RequestOutcomes 按判定顺序列出全部请求结果，先命中的算。
var RequestOutcomes = []string{
	OutcomeCanceled,
	OutcomeUnauthorized,
	OutcomeInternal,
	OutcomeTimeout,
	OutcomeUpstreamError,
	OutcomeClientError,
	OutcomePartial,
	OutcomeSuccess,
}

var requestOutcomesTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "unify_query",
		Name:      "request_outcomes_total",
		Help: "HTTP requests by route template and how each ended, counted once per request; first match wins: " +
			"canceled (the caller disconnected, or the response could not be written back), " +
			"unauthorized (rejected by this service's JWT check), " +
			"internal (a recovered panic, or a 5xx not built from a typed failure), " +
			"timeout (504), upstream_error (502: the backend or a dependency failed), " +
			"client_error (other 4xx), partial (some routes failed and the rest was returned), success.",
	},
	[]string{"handler", "outcome"},
)

// RequestOutcomeInc 记一次请求的结果；handler 是路由模板，没有匹配到路由时用 HandlerUnmatched。
func RequestOutcomeInc(ctx context.Context, handler, outcome string) {
	metric, _ := requestOutcomesTotal.GetMetricWithLabelValues(handler, outcome)
	counterInc(ctx, metric)
}

// PrebuildRequestOutcomes 在路由注册完成后，为每个路由模板和 HandlerUnmatched 建出全部结果格，
// 值为 0：Go 客户端不导出没有调用过的格，首次出现就是 1 的序列 increase() 读不到。
func PrebuildRequestOutcomes(handlers []string) {
	for _, handler := range append(append([]string{}, handlers...), HandlerUnmatched) {
		for _, outcome := range RequestOutcomes {
			requestOutcomesTotal.WithLabelValues(handler, outcome)
		}
	}
}
