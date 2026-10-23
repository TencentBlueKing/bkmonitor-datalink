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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	sharedSchemaRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "shared_schema_response_total",
		Help: "Shared schema v1 responses by fixed outcome and fallback/failure reason.",
	}, []string{"codec", "result", "reason"})
	sharedSchemaDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "unify_query", Name: "shared_schema_response_seconds",
		Help: "Shared schema response duration, including bounded preflight.", Buckets: secondsBuckets,
	}, []string{"codec", "stage"})
	sharedSchemaBytes = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "shared_schema_response_bytes_total", Help: "Actual shared schema body bytes written, including partial writes.",
	}, []string{"codec"})
	sharedSchemaFrames = promauto.NewCounter(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "shared_schema_response_frames_total", Help: "Complete shared schema frames written and flushed.",
	})
	sharedSchemaSchemas = promauto.NewHistogram(prometheus.HistogramOpts{
		Namespace: "unify_query", Name: "shared_schema_response_schemas", Help: "Schemas in preflight-approved shared schema responses.",
		Buckets: []float64{0, 1, 4, 16, 64, 256, 1024},
	})
)

// All result/reason/stage arguments originate from fixed codec enums. Do not
// add dimensions, query groups, result payloads or error text as labels.
func SharedSchemaResponseObserve(codec, result, reason string, schemas, frames int, bytes int64, preflight, encode, write, total time.Duration) {
	sharedSchemaRequests.WithLabelValues(codec, result, reason).Inc()
	sharedSchemaDuration.WithLabelValues(codec, "preflight").Observe(preflight.Seconds())
	sharedSchemaDuration.WithLabelValues(codec, "encode").Observe(encode.Seconds())
	sharedSchemaDuration.WithLabelValues(codec, "write").Observe(write.Seconds())
	sharedSchemaDuration.WithLabelValues(codec, "total").Observe(total.Seconds())
	sharedSchemaBytes.WithLabelValues(codec).Add(float64(bytes))
	sharedSchemaFrames.Add(float64(frames))
	if result != "fallback" {
		sharedSchemaSchemas.Observe(float64(schemas))
	}
}
