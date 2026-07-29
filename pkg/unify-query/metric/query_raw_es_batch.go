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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
)

const (
	// QueryRawESBatchStorageType is fixed because this batch path only supports
	// direct Elasticsearch storage. Callers cannot provide a storage label.
	QueryRawESBatchStorageType = "elasticsearch"
)

// QueryRaw ES batch event labels are a fixed set. Do not add request-derived
// values such as storage IDs, endpoints, indexes, fingerprints or errors.
const (
	QueryRawESBatchEventPlannerEnabled             = "planner_enabled"
	QueryRawESBatchEventPlannerCandidate           = "planner_candidate"
	QueryRawESBatchEventPlannerPreGroup            = "planner_pre_group"
	QueryRawESBatchEventPlannerFinalGroup          = "planner_final_group"
	QueryRawESBatchEventPlannerBatch               = "planner_batch"
	QueryRawESBatchEventPlannerSingle              = "planner_single"
	QueryRawESBatchEventPlannerIneligible          = "planner_ineligible"
	QueryRawESBatchEventPlannerPreSingle           = "planner_pre_single"
	QueryRawESBatchEventPlannerFinalSplit          = "planner_final_body_split"
	QueryRawESBatchEventPlannerFingerprintFallback = "planner_fingerprint_fallback"
	QueryRawESBatchEventPlannerPackedSingle        = "planner_packed_single"
	QueryRawESBatchEventPlannerBodyOversized       = "planner_body_oversized"
	QueryRawESBatchEventPlannerPackError           = "planner_pack_error"

	QueryRawESBatchEventWireSuccess          = "wire_success"
	QueryRawESBatchEventWirePartial          = "wire_partial"
	QueryRawESBatchEventWireChildSuccess     = "wire_child_success"
	QueryRawESBatchEventWireChildFailure     = "wire_child_failure"
	QueryRawESBatchEventWireTransportFailure = "wire_transport_failure"

	QueryRawESBatchEventFallbackAttempted = "fallback_attempted"
	QueryRawESBatchEventFallbackRecovered = "fallback_recovered"
	QueryRawESBatchEventFallbackFailed    = "fallback_failed"
)

const (
	QueryRawESBatchDurationPrepare = "prepare"
	QueryRawESBatchDurationExecute = "execute"
	QueryRawESBatchDurationTotal   = "total"
)

var (
	queryRawESBatchEventsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "unify_query",
			Name:      "query_raw_es_batch_events_total",
			Help:      "query_raw Elasticsearch batch events from a fixed low-cardinality event set",
		},
		[]string{"storage_type", "event", "version", "commit_id"},
	)

	queryRawESBatchMemberCount = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_raw_es_batch_member_count",
			Help:      "number of child searches in one query_raw Elasticsearch batch request",
			Buckets:   []float64{1, 2, 4, 8, 16, 32, 64, 128, 256},
		},
		[]string{"storage_type", "version", "commit_id"},
	)

	queryRawESBatchBodyBytes = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_raw_es_batch_body_bytes",
			Help:      "NDJSON request body size of one query_raw Elasticsearch batch request",
			Buckets:   bytesBuckets,
		},
		[]string{"storage_type", "version", "commit_id"},
	)

	queryRawESBatchDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "unify_query",
			Name:      "query_raw_es_batch_duration_seconds",
			Help:      "query_raw Elasticsearch batch duration by a fixed stage",
			Buckets:   secondsBuckets,
		},
		[]string{"storage_type", "stage", "version", "commit_id"},
	)
)

// QueryRawESBatchEventInc records one fixed query_raw Elasticsearch batch event.
func QueryRawESBatchEventInc(ctx context.Context, event string) {
	QueryRawESBatchEventAdd(ctx, event, 1)
}

// QueryRawESBatchEventAdd records count fixed query_raw Elasticsearch batch
// events. Unknown events and non-positive counts are rejected before a
// Prometheus series is created.
func QueryRawESBatchEventAdd(ctx context.Context, event string, count int) {
	if count <= 0 || !isQueryRawESBatchEvent(event) {
		return
	}

	metric, _ := queryRawESBatchEventsTotal.GetMetricWithLabelValues(
		QueryRawESBatchStorageType,
		event,
		config.Version,
		config.CommitHash,
	)
	counterAdd(ctx, metric, float64(count))
}

// QueryRawESBatchMemberCountObserve records the number of child searches in an
// emitted _msearch request.
func QueryRawESBatchMemberCountObserve(ctx context.Context, members int) {
	if members <= 0 {
		return
	}

	metric, _ := queryRawESBatchMemberCount.GetMetricWithLabelValues(
		QueryRawESBatchStorageType,
		config.Version,
		config.CommitHash,
	)
	observe(ctx, metric, float64(members))
}

// QueryRawESBatchBodyBytesObserve records the NDJSON body size of an emitted
// _msearch request.
func QueryRawESBatchBodyBytesObserve(ctx context.Context, bodyBytes int) {
	if bodyBytes <= 0 {
		return
	}

	metric, _ := queryRawESBatchBodyBytes.GetMetricWithLabelValues(
		QueryRawESBatchStorageType,
		config.Version,
		config.CommitHash,
	)
	observe(ctx, metric, float64(bodyBytes))
}

// QueryRawESBatchDurationObserve records a fixed prepare, execute or total
// duration. Unknown stages and negative durations are rejected before a
// Prometheus series is created.
func QueryRawESBatchDurationObserve(ctx context.Context, stage string, duration time.Duration) {
	if duration < 0 || !isQueryRawESBatchDurationStage(stage) {
		return
	}

	metric, _ := queryRawESBatchDurationSeconds.GetMetricWithLabelValues(
		QueryRawESBatchStorageType,
		stage,
		config.Version,
		config.CommitHash,
	)
	observe(ctx, metric, duration.Seconds())
}

func isQueryRawESBatchEvent(event string) bool {
	switch event {
	case QueryRawESBatchEventPlannerEnabled,
		QueryRawESBatchEventPlannerCandidate,
		QueryRawESBatchEventPlannerPreGroup,
		QueryRawESBatchEventPlannerFinalGroup,
		QueryRawESBatchEventPlannerBatch,
		QueryRawESBatchEventPlannerSingle,
		QueryRawESBatchEventPlannerIneligible,
		QueryRawESBatchEventPlannerPreSingle,
		QueryRawESBatchEventPlannerFinalSplit,
		QueryRawESBatchEventPlannerFingerprintFallback,
		QueryRawESBatchEventPlannerPackedSingle,
		QueryRawESBatchEventPlannerBodyOversized,
		QueryRawESBatchEventPlannerPackError,
		QueryRawESBatchEventWireSuccess,
		QueryRawESBatchEventWirePartial,
		QueryRawESBatchEventWireChildSuccess,
		QueryRawESBatchEventWireChildFailure,
		QueryRawESBatchEventWireTransportFailure,
		QueryRawESBatchEventFallbackAttempted,
		QueryRawESBatchEventFallbackRecovered,
		QueryRawESBatchEventFallbackFailed:
		return true
	default:
		return false
	}
}

func isQueryRawESBatchDurationStage(stage string) bool {
	switch stage {
	case QueryRawESBatchDurationPrepare, QueryRawESBatchDurationExecute, QueryRawESBatchDurationTotal:
		return true
	default:
		return false
	}
}
