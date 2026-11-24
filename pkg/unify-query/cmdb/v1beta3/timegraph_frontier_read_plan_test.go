// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// 宽读取前的计数覆盖所有评估点各自的回看窗口：在最后一个网格点按关系维度去重后再计数。
func TestFrontierScopeProbeCountsTheWholeWindow(t *testing.T) {
	start := time.Unix(1700000040, 0)
	loader := &timeGraphMatrixLoader{model: &Model{schemaProvider: wideFrontierProvider(), timeGraphQueryReference: selectorQueryReference},
		start: start, end: start.Add(150 * time.Second), step: time.Minute, lookBack: time.Minute}
	relation := cmdb.Relation{V: []cmdb.Resource{"pod", "target"}, MetricName: "pod_to_target_flow", Category: string(RelationCategoryStatic)}
	model := &Model{schemaProvider: wideFrontierProvider()}
	q := newTimeGraphWithConfig(model.timeGraphConfig("space"))
	ts, err := q.MakeQueryTsWithWindow(context.Background(), "space", nil, loader.start, loader.end, loader.step, loader.lookBack, relation)
	require.NoError(t, err)
	probe := loader.frontierScopeProbe(ts)
	require.NotNil(t, probe)
	// 网格点 40、100、160；最后一个网格点是 160，窗口覆盖 (-20, 160]。
	require.Equal(t, "1700000160", probe.Start)
	require.Equal(t, probe.Start, probe.End)
	require.EqualValues(t, "3m0s", probe.QueryList[0].TimeAggregation.Window)
	require.Equal(t, ts.QueryList[0].AggregateMethodList[0], probe.QueryList[0].AggregateMethodList[0])
	require.Equal(t, structured.COUNT, probe.QueryList[0].AggregateMethodList[1].Method)
	require.Empty(t, probe.QueryList[0].AggregateMethodList[1].Dimensions)
	require.EqualValues(t, "1m0s", ts.QueryList[0].TimeAggregation.Window, "the read itself is unchanged")
	expr, _, err := loader.prepare(initTimeGraphQueryTestEnvironment(), probe)
	require.NoError(t, err)
	require.Equal(t, "count(count by (cluster, namespace, pod, target_id) (count_over_time(a[3m])))", expr)
}

// 没有出现过超限的请求不计数：宽读取照常发出，读取与此前相同；构图 span 汇总计数、跳过的宽读取、
// 超限次数、学到的最大批与逐条准备。
func TestFrontierRequestWithoutOverflowSendsNoCount(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { otel.SetTracerProvider(previous); require.NoError(t, provider.Shutdown(context.Background())) })
	b := wideFrontierScenario(300, 3, 2)
	model := legacyRegressionModel(wideFrontierProvider())
	model.timeGraphVMQuery = b.query(t)
	_, _, _, _, buckets, err := model.QueryResourceMatcherRange(initTimeGraphQueryTestEnvironment(), "", "space", "1m", "1700000040", "1700000100", "target", "root", cmdb.Matcher{"root_id": "r1"}, nil, false, nil)
	require.NoError(t, err)
	require.Equal(t, wideFrontierWant(300), wideFrontierBucketTargets(buckets))
	require.Zero(t, b.probes)
	require.Equal(t, 1, b.wideQueries)
	require.Len(t, b.hopQueries, 1)
	attrs := map[string]attribute.Value{}
	for _, span := range recorder.Ended() {
		if span.Name() == "build-time-graph-from-relations" {
			for _, kv := range span.Attributes() {
				attrs[string(kv.Key)] = kv.Value
			}
		}
	}
	for _, key := range []string{"frontier-probe-count", "frontier-wide-skip-count", "frontier-wide-overflow-count"} {
		require.Contains(t, attrs, key)
		require.Zero(t, attrs[key].AsInt64(), key)
	}
	require.EqualValues(t, timeGraphFrontierBatchSize, attrs["frontier-batch-max-identities"].AsInt64())
	require.Positive(t, attrs["matrix-prepare-count"].AsInt64())
	for _, key := range []string{"matrix-prepare-duration-seconds", "backend-read-count", "backend-read-bytes", "backend-first-byte-seconds"} {
		require.Contains(t, attrs, key)
	}
}

// 宽读取超过边界后退回分批；响应远小于边界时批大小逐批翻倍：1001 个身份读 5 次（64、128、256、512、41），
// 固定 64 个一批要读 16 次。
func TestFrontierBatchesGrowWhileResponsesStaySmall(t *testing.T) {
	b := wideFrontierScenario(1000, 3, 2)
	b.rejectWide = func(context.Context) error { return metadata.ErrBackendResponseTooLarge }
	model := legacyRegressionModel(wideFrontierProvider())
	model.timeGraphVMQuery = b.query(t)
	_, _, _, _, buckets, err := model.QueryResourceMatcherRange(initTimeGraphQueryTestEnvironment(), "", "space", "1m", "1700000040", "1700000100", "target", "root", cmdb.Matcher{"root_id": "r1"}, nil, false, nil)
	require.NoError(t, err)
	require.Equal(t, wideFrontierWant(1000), wideFrontierBucketTargets(buckets))
	require.Equal(t, 1, b.wideQueries, "the oversized wide read falls back to batches")
	sizes := make([]int, 0, len(b.hopQueries))
	for _, conditions := range b.hopQueries[1:] {
		groups, err := conditions.AnalysisConditions()
		require.NoError(t, err)
		sizes = append(sizes, len(groups))
	}
	require.Equal(t, []int{64, 128, 256, 512, 41}, sizes)
}

// 批大小每步最多翻倍或减半，不满一批不改，受条件字节与身份数上限约束。
func TestFrontierBatchSizeAdjustment(t *testing.T) {
	small := frontierBatchObservation{responseBytes: 1 << 20}
	for _, tc := range []struct {
		name       string
		batch      int
		identities int
		observed   frontierBatchObservation
		want       int
	}{
		{"a full batch with a small response doubles", 64, 64, small, 128},
		{"a full batch near the boundary keeps its size", 64, 64, frontierBatchObservation{responseBytes: timeGraphBackendResponseBytes / 2}, 64},
		{"a partial batch keeps the size", 256, 10, small, 256},
		{"an oversized batch halves", 256, 256, frontierBatchObservation{tooLarge: true}, 128},
		{"a single identity never goes below one", 1, 1, frontierBatchObservation{tooLarge: true}, 1},
		{"condition bytes cap the growth", 64, 64, frontierBatchObservation{responseBytes: 1 << 20, conditionBytes: 400 * 1024}, 81},
		{"condition bytes shrink an oversized condition", 256, 256, frontierBatchObservation{responseBytes: 1 << 20, conditionBytes: 1024 * 1024}, 128},
		{"the identity cap holds", 1024, 1024, small, timeGraphFrontierBatchMax},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &frontierReadState{batch: tc.batch}
			state.adjustBatch(tc.identities, tc.observed)
			require.Equal(t, tc.want, state.batch)
		})
	}
}

// wideFrontierBucketTargets 把兼容区间结果整理成每个时间点的目标列表，与 wideFrontierWant 对照。
func wideFrontierBucketTargets(buckets []cmdb.MatchersWithTimestamp) map[int64][]string {
	got := make(map[int64][]string, len(buckets))
	for _, bucket := range buckets {
		for _, target := range bucket.Matchers {
			got[bucket.Timestamp] = append(got[bucket.Timestamp], target["target_id"])
		}
		sort.Strings(got[bucket.Timestamp])
	}
	return got
}
