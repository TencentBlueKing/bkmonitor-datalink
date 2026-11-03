// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"strconv"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
)

func rawPlanTraceExporter(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		require.NoError(t, provider.Shutdown(context.Background()))
	})
	return exporter
}

func rawPlanningSpan(t *testing.T, exporter *tracetest.InMemoryExporter) tracetest.SpanStub {
	t.Helper()
	var matches tracetest.SpanStubs
	for _, span := range exporter.GetSpans() {
		if span.Name == "query-raw-plan" {
			matches = append(matches, span)
		}
	}
	require.Len(t, matches, 1)
	return matches[0]
}

func rawPlanningCounts(span tracetest.SpanStub) map[string]int64 {
	counts := make(map[string]int64, len(span.Attributes))
	for _, attr := range span.Attributes {
		counts[string(attr.Key)] = attr.Value.AsInt64()
	}
	return counts
}

func rawPlanDecisionCounts(t *testing.T) map[string]float64 {
	t.Helper()
	counts := map[string]float64{
		metric.RawQueryDecisionHistoryDuplicate: 0,
		metric.RawQueryDecisionMergeDB:          0,
	}
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() != "unify_query_raw_query_decisions_total" {
			continue
		}
		for _, item := range family.GetMetric() {
			for _, label := range item.GetLabel() {
				if label.GetName() == "reason" {
					if _, exists := counts[label.GetValue()]; exists {
						counts[label.GetValue()] += item.GetCounter().GetValue()
					}
				}
			}
		}
	}
	return counts
}

func TestRawQueryPlanObservabilityExecutionModes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		batch  bool
		scroll string
	}{
		{name: "ordinary raw"},
		{name: "batch", batch: true},
		{name: "scroll", scroll: "1m"},
		{name: "batch with scroll", batch: true, scroll: "1m"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, query, _ := rawPlanFixture()
			query.IsESBatch, query.Scroll = tc.batch, tc.scroll
			exporter := rawPlanTraceExporter(t)
			before := rawPlanDecisionCounts(t)
			plan, err := query.ToRawQueryPlan(ctx)
			require.NoError(t, err)
			require.Equal(t, QueryPlanStats{ReusedTargets: 1}, plan.Stats)
			span := rawPlanningSpan(t, exporter)
			require.Equal(t, codes.Unset, span.Status.Code)
			require.True(t, span.EndTime.After(span.StartTime))
			require.Equal(t, map[string]int64{
				"raw_plan_route_count":         3,
				"raw_plan_query_count":         2,
				"raw_plan_reused_target_count": 1,
				"raw_plan_merge_db_count":      0,
			}, rawPlanningCounts(span))
			for _, child := range exporter.GetSpans() {
				if child.Name == "query-ts-to-query-metric" {
					require.Equal(t, span.SpanContext.SpanID(), child.Parent.SpanID())
				}
			}
			after := rawPlanDecisionCounts(t)
			require.Equal(t, before[metric.RawQueryDecisionHistoryDuplicate]+1, after[metric.RawQueryDecisionHistoryDuplicate])
			require.Equal(t, before[metric.RawQueryDecisionMergeDB], after[metric.RawQueryDecisionMergeDB])
		})
	}
}

func TestRawQueryPlanObservabilityMergeDBAndLogicalQueries(t *testing.T) {
	for _, logicalQueries := range []int{1, 2} {
		t.Run(strconv.Itoa(logicalQueries), func(t *testing.T) {
			ctx, query, table := rawPlanFixture()
			query.IsMergeDB = true
			table.StorageType = metadata.BkSqlStorageType
			table.Measurement = metadata.DorisStorageType
			table.StorageClusterRecords = rawPlanRecords(table, "16", "16", "16")
			table.StorageClusterRecords[1].DB = "other-database"
			if logicalQueries == 2 {
				second := *query.QueryList[0]
				query.QueryList = append(query.QueryList, &second)
			}
			exporter := rawPlanTraceExporter(t)
			before := rawPlanDecisionCounts(t)
			plan, err := query.ToRawQueryPlan(ctx)
			require.NoError(t, err)
			require.Equal(t, QueryPlanStats{ReusedTargets: logicalQueries, MergedQueries: logicalQueries}, plan.Stats)
			require.Equal(t, map[string]int64{
				"raw_plan_route_count":         int64(3 * logicalQueries),
				"raw_plan_query_count":         int64(logicalQueries),
				"raw_plan_reused_target_count": int64(logicalQueries),
				"raw_plan_merge_db_count":      int64(logicalQueries),
			}, rawPlanningCounts(rawPlanningSpan(t, exporter)))
			after := rawPlanDecisionCounts(t)
			for _, reason := range []string{metric.RawQueryDecisionHistoryDuplicate, metric.RawQueryDecisionMergeDB} {
				require.Equal(t, before[reason]+float64(logicalQueries), after[reason])
			}
		})
	}
}

func TestRawQueryPlanObservabilityFailures(t *testing.T) {
	for _, tc := range []struct {
		name              string
		invalidTime       bool
		invalidConditions bool
		missingStorage    bool
		partialPlan       bool
	}{
		{name: "invalid time", invalidTime: true},
		{name: "invalid conditions", invalidConditions: true},
		{name: "unresolved storage type", missingStorage: true},
		{name: "later logical query fails", partialPlan: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, query, table := rawPlanFixture()
			if tc.invalidTime {
				query.Start = "invalid-time"
			}
			invalidConditions := Conditions{FieldList: []ConditionField{{DimensionName: "first"}, {DimensionName: "second"}}}
			if tc.invalidConditions {
				query.QueryList[0].Conditions = invalidConditions
			}
			if tc.missingStorage {
				table.StorageType = ""
				table.StorageID = "raw-plan-observability-unknown-storage"
				table.StorageClusterRecords = nil
			}
			if tc.partialPlan {
				second := *query.QueryList[0]
				second.Conditions = invalidConditions
				query.QueryList = append(query.QueryList, &second)
			}
			exporter := rawPlanTraceExporter(t)
			before := rawPlanDecisionCounts(t)
			plan, err := query.ToRawQueryPlan(ctx)
			require.Error(t, err)
			require.Empty(t, plan)
			span := rawPlanningSpan(t, exporter)
			require.Equal(t, codes.Error, span.Status.Code)
			require.Equal(t, err.Error(), span.Status.Description)
			require.NotEmpty(t, span.Events)
			require.Equal(t, map[string]int64{
				"raw_plan_route_count":         0,
				"raw_plan_query_count":         0,
				"raw_plan_reused_target_count": 0,
				"raw_plan_merge_db_count":      0,
			}, rawPlanningCounts(span))
			if !tc.invalidTime {
				failedChildren := 0
				for _, child := range exporter.GetSpans() {
					if child.Name == "query-ts-to-query-metric" && child.Status.Code == codes.Error {
						failedChildren++
						require.Equal(t, span.SpanContext.SpanID(), child.Parent.SpanID())
					}
				}
				require.Equal(t, 1, failedChildren)
			}
			require.Equal(t, before, rawPlanDecisionCounts(t))
		})
	}
}

func TestRawQueryPlanObservabilityDoesNotCountTimeSeries(t *testing.T) {
	ctx, query, _ := rawPlanFixture()
	exporter := rawPlanTraceExporter(t)
	before := rawPlanDecisionCounts(t)
	queries, err := query.ToQueryReference(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, queries.Count())
	for _, span := range exporter.GetSpans() {
		require.NotEqual(t, "query-raw-plan", span.Name)
	}
	require.Equal(t, before, rawPlanDecisionCounts(t))
}
