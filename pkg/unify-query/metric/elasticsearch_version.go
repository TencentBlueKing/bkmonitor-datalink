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
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const elasticsearchVersionOther = "other"

var (
	elasticsearchVersionRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "elasticsearch_version_requests_total",
		Help: "Elasticsearch root version requests by major version and outcome",
	}, []string{"es_major", "result"})
	elasticsearchVersionSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "unify_query", Name: "elasticsearch_version_request_seconds",
		Help: "Elasticsearch root version request duration including response validation", Buckets: secondsBuckets,
	}, []string{"result"})
	elasticsearchDateHistogramResolutions = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "elasticsearch_date_histogram_resolution_total",
		Help: "Elasticsearch date histogram syntax decisions by source, mode and outcome",
	}, []string{"source", "mode", "result"})
)

// ElasticsearchVersionObserve 的完整版本写入 trace，指标仅使用有限的大版本标签。
func ElasticsearchVersionObserve(ctx context.Context, major uint64, result string, duration time.Duration) {
	label := "unknown"
	if result == "success" {
		switch major {
		case 5, 6, 7, 8, 9:
			label = strconv.FormatUint(major, 10)
		default:
			label = elasticsearchVersionOther
		}
	}
	counterInc(ctx, elasticsearchVersionRequests.WithLabelValues(label, result))
	observe(ctx, elasticsearchVersionSeconds.WithLabelValues(result), duration.Seconds())
}

func ElasticsearchDateHistogramResolutionInc(ctx context.Context, source, mode, result string) {
	counterInc(ctx, elasticsearchDateHistogramResolutions.WithLabelValues(source, mode, result))
}
