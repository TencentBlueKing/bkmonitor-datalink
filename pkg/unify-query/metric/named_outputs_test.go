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
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
)

func namedOutputsOutputCountSamples(t *testing.T) uint64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "unify_query_named_outputs_output_count" {
			continue
		}
		for _, familyMetric := range family.GetMetric() {
			labels := make(map[string]string, len(familyMetric.GetLabel()))
			for _, label := range familyMetric.GetLabel() {
				labels[label.GetName()] = label.GetValue()
			}
			if labels["version"] == config.Version && labels["commit_id"] == config.CommitHash {
				return familyMetric.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

func TestNamedOutputsOutputCountObservesConfiguredCountsAboveDefault(t *testing.T) {
	ctx := context.Background()
	before := namedOutputsOutputCountSamples(t)
	NamedOutputsOutputCountObserve(ctx, 5)
	require.Equal(t, before+1, namedOutputsOutputCountSamples(t))
}

func TestNamedOutputsMetricsUseFixedLowCardinalityLabels(t *testing.T) {
	ctx := context.Background()

	requestBefore := queryRawESBatchSeriesCount(t, "unify_query_named_outputs_requests_total")
	NamedOutputsRequestInc(ctx, NamedOutputsRequestReceived)
	require.Equal(t, requestBefore+1, queryRawESBatchSeriesCount(t, "unify_query_named_outputs_requests_total"))

	stateBefore := queryRawESBatchSeriesCount(t, "unify_query_named_outputs_output_states_total")
	NamedOutputsOutputStateInc(ctx, NamedOutputsOutputPartial)
	require.Equal(t, stateBefore+1, queryRawESBatchSeriesCount(t, "unify_query_named_outputs_output_states_total"))

	cacheBefore := queryRawESBatchSeriesCount(t, "unify_query_named_outputs_selector_cache_events_total")
	NamedOutputsSelectorCacheEventInc(ctx, NamedOutputsSelectorCacheHit)
	require.Equal(t, cacheBefore+1, queryRawESBatchSeriesCount(t, "unify_query_named_outputs_selector_cache_events_total"))

	entryBefore := queryRawESBatchSeriesCount(t, "unify_query_named_outputs_selector_cache_entries_total")
	NamedOutputsSelectorCacheStateInc(ctx, NamedOutputsSelectorCachePartial)
	require.Equal(t, entryBefore+1, queryRawESBatchSeriesCount(t, "unify_query_named_outputs_selector_cache_entries_total"))

	NamedOutputsOutputCountObserve(ctx, 3)
	NamedOutputsDownstreamCallsObserve(ctx, NamedOutputsModePromEngine, 2)
	NamedOutputsDownstreamAmplificationObserve(ctx, NamedOutputsModePromEngine, 2, 3)
	NamedOutputsResponseBytesObserve(ctx, 4096)
	NamedOutputsDurationObserve(ctx, NamedOutputsRequestSuccess, time.Second)
	NamedOutputsResultSizeObserve(ctx, 2, 10)
	NamedOutputsRejectInc(ctx, NamedOutputsRejectValidation)

	const sensitive = "user-output-A/secret-expression"
	seriesBefore := map[string]int{
		"request": queryRawESBatchSeriesCount(t, "unify_query_named_outputs_requests_total"),
		"state":   queryRawESBatchSeriesCount(t, "unify_query_named_outputs_output_states_total"),
		"cache":   queryRawESBatchSeriesCount(t, "unify_query_named_outputs_selector_cache_events_total"),
		"entry":   queryRawESBatchSeriesCount(t, "unify_query_named_outputs_selector_cache_entries_total"),
		"calls":   queryRawESBatchSeriesCount(t, "unify_query_named_outputs_downstream_calls"),
		"ratio":   queryRawESBatchSeriesCount(t, "unify_query_named_outputs_downstream_amplification_ratio"),
		"reject":  queryRawESBatchSeriesCount(t, "unify_query_named_outputs_rejections_total"),
	}
	NamedOutputsRequestInc(ctx, sensitive)
	NamedOutputsOutputStateInc(ctx, sensitive)
	NamedOutputsSelectorCacheEventInc(ctx, sensitive)
	NamedOutputsSelectorCacheStateInc(ctx, sensitive)
	NamedOutputsDownstreamCallsObserve(ctx, sensitive, 1)
	NamedOutputsDownstreamAmplificationObserve(ctx, sensitive, 1, 1)
	NamedOutputsDurationObserve(ctx, sensitive, time.Second)
	NamedOutputsRejectInc(ctx, sensitive)
	require.Equal(t, seriesBefore["request"], queryRawESBatchSeriesCount(t, "unify_query_named_outputs_requests_total"))
	require.Equal(t, seriesBefore["state"], queryRawESBatchSeriesCount(t, "unify_query_named_outputs_output_states_total"))
	require.Equal(t, seriesBefore["cache"], queryRawESBatchSeriesCount(t, "unify_query_named_outputs_selector_cache_events_total"))
	require.Equal(t, seriesBefore["entry"], queryRawESBatchSeriesCount(t, "unify_query_named_outputs_selector_cache_entries_total"))
	require.Equal(t, seriesBefore["calls"], queryRawESBatchSeriesCount(t, "unify_query_named_outputs_downstream_calls"))
	require.Equal(t, seriesBefore["ratio"], queryRawESBatchSeriesCount(t, "unify_query_named_outputs_downstream_amplification_ratio"))
	require.Equal(t, seriesBefore["reject"], queryRawESBatchSeriesCount(t, "unify_query_named_outputs_rejections_total"))
}
