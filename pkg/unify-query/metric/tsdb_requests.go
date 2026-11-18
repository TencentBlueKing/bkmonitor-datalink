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
	"errors"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// 存储请求的结果，封闭集合，分法与 request_outcomes_total 对齐。
const (
	TsDBOutcomeSuccess  = "success"
	TsDBOutcomeError    = "error"
	TsDBOutcomeRejected = "rejected"
	TsDBOutcomeTimeout  = "timeout"
	TsDBOutcomeCanceled = "canceled"
	// TsDBOutcomeBodyError 是后端以 2xx 答复、在应答体里报的失败，业务码还没有分类。
	TsDBOutcomeBodyError = "body_error"
)

// TsDBOutcomes 列出全部存储请求结果。
var TsDBOutcomes = []string{TsDBOutcomeSuccess, TsDBOutcomeError, TsDBOutcomeRejected, TsDBOutcomeTimeout, TsDBOutcomeCanceled, TsDBOutcomeBodyError}

// TsDBTypes 是各后端打点时传给 TsDBRequestSecond 和 TsDBRequestInc 的 tsdb_type，封闭集合。
var TsDBTypes = []string{
	metadata.VictoriaMetricsStorageType,
	metadata.ElasticsearchStorageType,
	metadata.BkSqlStorageType,
	metadata.InfluxDBStorageType + "_http",
	metadata.InfluxDBStorageType + "_grpc",
	metadata.RedisStorageType,
}

var tsdbRequestsTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Namespace: "unify_query",
		Name:      "tsdb_requests_total",
		Help: "Requests to storage backends by outcome, counted for failures as well as successes: " +
			"success; error (the backend answered 5xx, 429, 401 or 403, or the transport failed); " +
			"rejected (any other failure, mostly the backend refusing the query with another 4xx); " +
			"timeout (the deadline passed); canceled (the request was canceled before the backend answered); " +
			"body_error (the backend answered 2xx and reported a failure in the response body, its code not yet classified).",
	},
	[]string{"tsdb_type", "outcome"},
)

func init() {
	// 6 种 tsdb_type 乘以 6 种结果，启动时全部建出，值为 0。
	for _, tsdbType := range TsDBTypes {
		for _, outcome := range TsDBOutcomes {
			tsdbRequestsTotal.WithLabelValues(tsdbType, outcome)
		}
	}
}

// TsDBRequestOutcome 按错误链里的类型给一次存储请求分类，与失败响应共用 FailureStatus：
// 504 为 timeout，502 为 error，其余失败为 rejected。一次存储请求只对应一个后端，取消可以按整条链判。
// 后端在应答体里报的失败（BackendBodyError）单列为 body_error：并进 rejected 会把后端的失败藏起来，
// 并进 error 又会让调用方写错的查询算成后端故障。
func TsDBRequestOutcome(err error) string {
	if err == nil {
		return TsDBOutcomeSuccess
	}
	if errors.Is(err, context.Canceled) {
		return TsDBOutcomeCanceled
	}
	var body *metadata.BackendBodyError
	if errors.As(err, &body) {
		return TsDBOutcomeBodyError
	}
	switch metadata.FailureStatus(err) {
	case http.StatusGatewayTimeout:
		return TsDBOutcomeTimeout
	case http.StatusBadGateway:
		return TsDBOutcomeError
	default:
		return TsDBOutcomeRejected
	}
}

// TsDBRequestInc 记一次存储请求，err 是这次请求本身的结果（后端吞掉、不交给调用方的错误也要计）。
func TsDBRequestInc(ctx context.Context, tsdbType string, err error) {
	TsDBRequestOutcomeInc(ctx, tsdbType, TsDBRequestOutcome(err))
}

// TsDBRequestOutcomeInc 记一次已经分好类的存储请求，用于错误不经 FailureStatus 分类的协议（如 gRPC）。
func TsDBRequestOutcomeInc(ctx context.Context, tsdbType, outcome string) {
	metric, _ := tsdbRequestsTotal.GetMetricWithLabelValues(tsdbType, outcome)
	counterInc(ctx, metric)
}
