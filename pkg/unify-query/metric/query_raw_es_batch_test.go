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
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
)

func TestQueryRawESBatchMetricsRecordValidEnums(t *testing.T) {
	ctx := context.Background()

	events := []string{
		QueryRawESBatchEventPlannerEnabled,
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
		QueryRawESBatchEventFallbackFailed,
	}
	for _, event := range events {
		before := queryRawESBatchCounterValue(t, event)
		QueryRawESBatchEventInc(ctx, event)
		require.Equal(t, before+1, queryRawESBatchCounterValue(t, event), event)
	}

	beforeCandidate := queryRawESBatchCounterValue(t, QueryRawESBatchEventPlannerCandidate)
	QueryRawESBatchEventAdd(ctx, QueryRawESBatchEventPlannerCandidate, 3)
	require.Equal(t, beforeCandidate+3, queryRawESBatchCounterValue(t, QueryRawESBatchEventPlannerCandidate))

	beforeMembers := queryRawESBatchHistogramValue(t, "unify_query_query_raw_es_batch_member_count", nil)
	QueryRawESBatchMemberCountObserve(ctx, 4)
	afterMembers := queryRawESBatchHistogramValue(t, "unify_query_query_raw_es_batch_member_count", nil)
	require.Equal(t, beforeMembers.count+1, afterMembers.count)
	require.InDelta(t, beforeMembers.sum+4, afterMembers.sum, 1e-12)

	beforeBody := queryRawESBatchHistogramValue(t, "unify_query_query_raw_es_batch_body_bytes", nil)
	QueryRawESBatchBodyBytesObserve(ctx, 4096)
	afterBody := queryRawESBatchHistogramValue(t, "unify_query_query_raw_es_batch_body_bytes", nil)
	require.Equal(t, beforeBody.count+1, afterBody.count)
	require.InDelta(t, beforeBody.sum+4096, afterBody.sum, 1e-12)

	durations := map[string]time.Duration{
		QueryRawESBatchDurationPrepare: 250 * time.Millisecond,
		QueryRawESBatchDurationExecute: 500 * time.Millisecond,
		QueryRawESBatchDurationTotal:   750 * time.Millisecond,
	}
	for stage, duration := range durations {
		before := queryRawESBatchHistogramValue(t, "unify_query_query_raw_es_batch_duration_seconds", map[string]string{"stage": stage})
		QueryRawESBatchDurationObserve(ctx, stage, duration)
		after := queryRawESBatchHistogramValue(t, "unify_query_query_raw_es_batch_duration_seconds", map[string]string{"stage": stage})
		require.Equal(t, before.count+1, after.count, stage)
		require.InDelta(t, before.sum+duration.Seconds(), after.sum, 1e-12, stage)
	}
}

func TestQueryRawESBatchMetricsRejectInvalidEnumsWithoutCreatingSeries(t *testing.T) {
	ctx := context.Background()
	const sensitiveSentinel = "https://user:password@example.invalid/index/_search?trace_id=secret"

	eventSeriesBefore := queryRawESBatchSeriesCount(t, "unify_query_query_raw_es_batch_events_total")
	QueryRawESBatchEventInc(ctx, sensitiveSentinel)
	QueryRawESBatchEventAdd(ctx, QueryRawESBatchEventPlannerCandidate, 0)
	QueryRawESBatchEventAdd(ctx, QueryRawESBatchEventPlannerCandidate, -1)
	require.Equal(t, eventSeriesBefore, queryRawESBatchSeriesCount(t, "unify_query_query_raw_es_batch_events_total"))

	durationSeriesBefore := queryRawESBatchSeriesCount(t, "unify_query_query_raw_es_batch_duration_seconds")
	QueryRawESBatchDurationObserve(ctx, sensitiveSentinel, time.Second)
	QueryRawESBatchDurationObserve(ctx, QueryRawESBatchDurationPrepare, -time.Second)
	require.Equal(t, durationSeriesBefore, queryRawESBatchSeriesCount(t, "unify_query_query_raw_es_batch_duration_seconds"))

	QueryRawESBatchMemberCountObserve(ctx, 0)
	QueryRawESBatchBodyBytesObserve(ctx, -1)

	for _, name := range []string{
		"unify_query_query_raw_es_batch_events_total",
		"unify_query_query_raw_es_batch_member_count",
		"unify_query_query_raw_es_batch_body_bytes",
		"unify_query_query_raw_es_batch_duration_seconds",
	} {
		family := queryRawESBatchMetricFamily(t, name)
		if family == nil {
			continue
		}
		for _, metric := range family.GetMetric() {
			for _, label := range metric.GetLabel() {
				require.NotContains(t, label.GetValue(), sensitiveSentinel)
				switch label.GetName() {
				case "storage_type", "event", "stage", "version", "commit_id":
				default:
					t.Fatalf("unexpected dynamic label %q in %s", label.GetName(), name)
				}
			}
		}
	}
}

type queryRawESBatchHistogramSnapshot struct {
	count uint64
	sum   float64
}

func queryRawESBatchCounterValue(t *testing.T, event string) float64 {
	t.Helper()

	family := queryRawESBatchMetricFamily(t, "unify_query_query_raw_es_batch_events_total")
	if family == nil {
		return 0
	}
	labels := queryRawESBatchBaseLabels()
	labels["event"] = event
	for _, metric := range family.GetMetric() {
		if queryRawESBatchLabelsMatch(metric, labels) {
			return metric.GetCounter().GetValue()
		}
	}
	return 0
}

func queryRawESBatchHistogramValue(t *testing.T, name string, extraLabels map[string]string) queryRawESBatchHistogramSnapshot {
	t.Helper()

	family := queryRawESBatchMetricFamily(t, name)
	if family == nil {
		return queryRawESBatchHistogramSnapshot{}
	}
	labels := queryRawESBatchBaseLabels()
	for name, value := range extraLabels {
		labels[name] = value
	}
	for _, metric := range family.GetMetric() {
		if queryRawESBatchLabelsMatch(metric, labels) {
			histogram := metric.GetHistogram()
			return queryRawESBatchHistogramSnapshot{
				count: histogram.GetSampleCount(),
				sum:   histogram.GetSampleSum(),
			}
		}
	}
	return queryRawESBatchHistogramSnapshot{}
}

func queryRawESBatchSeriesCount(t *testing.T, name string) int {
	t.Helper()

	family := queryRawESBatchMetricFamily(t, name)
	if family == nil {
		return 0
	}
	return len(family.GetMetric())
}

func queryRawESBatchMetricFamily(t *testing.T, name string) *dto.MetricFamily {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == name {
			return family
		}
	}
	return nil
}

func queryRawESBatchBaseLabels() map[string]string {
	return map[string]string{
		"storage_type": QueryRawESBatchStorageType,
		"version":      config.Version,
		"commit_id":    config.CommitHash,
	}
}

func queryRawESBatchLabelsMatch(metric *dto.Metric, expected map[string]string) bool {
	if len(metric.GetLabel()) != len(expected) {
		return false
	}
	for _, label := range metric.GetLabel() {
		if expected[label.GetName()] != label.GetValue() {
			return false
		}
	}
	return true
}
