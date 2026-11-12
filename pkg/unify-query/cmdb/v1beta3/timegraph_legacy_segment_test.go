// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"testing"
	"time"

	kitlog "github.com/go-kit/log"
	"github.com/prometheus/prometheus/model/labels"
	pl "github.com/prometheus/prometheus/promql"
	promtsdb "github.com/prometheus/prometheus/tsdb"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	uqquery "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	localprom "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// localPrometheusLegacyModel 用真实 TSDB 和 Prometheus 引擎执行生产生成的表达式；路由只是配置输入。
func localPrometheusLegacyModel(t *testing.T, ctx context.Context, samples map[string][]int64, calls *[]legacyRangeSegment) *Model {
	t.Helper()
	provider := NewSchemaProviderFromRelation(relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
		ResourcePrimaryKeys: map[string][]string{"source": {"source_id"}, "target": {"target_id"}},
		ResourceDefinitions: []*relation.ResourceDefinition{
			{Name: "source", Fields: []relation.FieldDefinition{{Name: "source_id", Required: true}}},
			{Name: "target", Fields: []relation.FieldDefinition{{Name: "target_id", Required: true}}},
		},
		RelationSchemas: []relation.RelationSchema{
			{RelationName: "source_to_target", FromType: "source", ToType: "target", Category: relation.RelationCategoryStatic, IsDirectional: true, MetricName: "source_target_flow"},
		},
	}))
	db, err := promtsdb.Open(t.TempDir(), kitlog.NewNopLogger(), nil, promtsdb.DefaultOptions(), nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	// 按时间顺序写入：空 TSDB 以首个样本定下可写窗口，乱序写入早一小时以上的样本会越界。
	type sample struct {
		target string
		at     int64
	}
	var ordered []sample
	for target, times := range samples {
		for _, at := range times {
			ordered = append(ordered, sample{target, at})
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].at < ordered[j].at })
	appender := db.Appender(ctx)
	for _, item := range ordered {
		_, err = appender.Append(0, labels.FromStrings("__name__", "a", "source_id", "a", "target_id", item.target), item.at*1000, 1)
		require.NoError(t, err)
	}
	require.NoError(t, appender.Commit())
	instance := localprom.NewInstance(ctx, pl.NewEngine(pl.EngineOpts{Timeout: 5 * time.Second, MaxSamples: 100000}), db, time.Minute, 2)
	model := &Model{schemaProvider: provider}
	model.timeGraphQueryReference = func(ctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
		q.TsDBMap = make(map[string]structured.TsDBs)
		for _, item := range q.QueryList {
			q.TsDBMap[item.ReferenceName] = structured.TsDBs{&uqquery.TsDBV2{
				TableID: "grid." + item.FieldName, Field: []string{item.FieldName},
				MeasurementType: redis.BkSplitMeasurement, ExpandMetricNames: []string{"a"},
				StorageID: "1", StorageType: metadata.InfluxDBStorageType, DB: "grid", Measurement: item.FieldName,
			}}
		}
		return q.ToQueryReference(ctx)
	}
	model.timeGraphVMQuery = func(ctx context.Context, _ *structured.QueryTs, expr string, instant bool, queryStart, queryEnd time.Time, step time.Duration) (pl.Matrix, error) {
		require.False(t, instant)
		*calls = append(*calls, legacyRangeSegment{start: queryStart, end: queryEnd, points: len(legacyRangeGridPoints(queryStart, queryEnd, step))})
		matrix, _, err := instance.DirectQueryRange(ctx, expr, queryStart, queryEnd, step)
		return matrix, err
	}
	return model
}

// 本地 Prometheus 引擎执行生产表达式：样本落在段边界前 30 秒，只属于边界上的评估点。
// 首段为空时第二段保持 64 点，分段读取与一次读取整个网格得到相同的分桶答案。
func TestLegacyRangeSegmentBoundaryOnLocalPrometheus(t *testing.T) {
	const points = 150
	start := int64(1793259600)
	end := start + (points-1)*60
	boundaries := []int64{start + 64*60, start + 128*60}
	samples := map[string][]int64{"t0": {boundaries[0] - 30}, "t1": {boundaries[1] - 30}}
	query := func(segmentPoints int) ([]cmdb.MatchersWithTimestamp, []legacyRangeSegment) {
		old := legacyRangeSegmentPoints
		legacyRangeSegmentPoints = segmentPoints
		defer func() { legacyRangeSegmentPoints = old }()
		ctx := initTimeGraphQueryTestEnvironment()
		var calls []legacyRangeSegment
		model := localPrometheusLegacyModel(t, ctx, samples, &calls)
		_, _, path, _, buckets, err := model.QueryResourceMatcherRange(ctx, "", "space", "60s",
			strconv.FormatInt(start, 10), strconv.FormatInt(end, 10), "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
		require.NoError(t, err)
		require.Equal(t, []string{"source", "target"}, path)
		return buckets, calls
	}
	want, unsplit := query(points)
	require.Equal(t, legacyGridSegments(time.Unix(start, 0), time.Minute, points), unsplit, "the reference reads the whole grid at once")
	require.Equal(t, []cmdb.MatchersWithTimestamp{
		{Timestamp: boundaries[0] * 1000, Matchers: cmdb.Matchers{{"target_id": "t0"}}},
		{Timestamp: boundaries[1] * 1000, Matchers: cmdb.Matchers{{"target_id": "t1"}}},
	}, want)

	got, calls := query(64)
	require.Equal(t, legacyGridSegments(time.Unix(start, 0), time.Minute, 64, 64, 22), calls, "each sample sits 30s before a segment start")
	require.Equal(t, want, got)
}

// legacyCandidateVM 按指标返回两条候选路径的 fixture；failAt 非空时该指标在起点不早于 failAt 的读取失败。
func legacyCandidateVM(t *testing.T, directFrom time.Time, failMetric string, failAt time.Time) func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
	return func(_ context.Context, query *structured.QueryTs, _ string, instant bool, queryStart, queryEnd time.Time, step time.Duration) (pl.Matrix, error) {
		require.False(t, instant)
		name := query.QueryList[0].FieldName
		if name == failMetric && !queryStart.Before(failAt) {
			return nil, errors.New("backend unavailable for this segment")
		}
		var timestamps []int64
		for _, at := range legacyRangeGridPoints(queryStart, queryEnd, step) {
			if name == "source_target_flow" && at.Before(directFrom) {
				continue
			}
			timestamps = append(timestamps, at.UnixMilli())
		}
		if len(timestamps) == 0 {
			return nil, nil
		}
		return filterTopologyRegressionMatrix(query, legacyAlternativeMatrix(name, timestamps...)), nil
	}
}

func legacyCandidateBuckets(grid []time.Time, target string) []cmdb.MatchersWithTimestamp {
	buckets := make([]cmdb.MatchersWithTimestamp, 0, len(grid))
	for _, at := range grid {
		buckets = append(buckets, cmdb.MatchersWithTimestamp{Timestamp: at.UnixMilli(), Matchers: cmdb.Matchers{{"target_id": target}}})
	}
	return buckets
}

// 首选候选只在后面的段有结果时仍是首条命中的路径，与一次读取整个网格的旧引擎相同；
// 首选候选在后面的段真实失败时整条候选作废，换下一条候选，不合并它前面各段的结果。
func TestLegacyRangeCandidateSelectionAcrossSegments(t *testing.T) {
	start := time.Unix(1700000040, 0)
	const points = 130
	end := start.Add(time.Duration(points-1) * time.Minute)
	grid := legacyRangeGridPoints(start, end, time.Minute)
	query := func(model *Model) ([]string, []cmdb.MatchersWithTimestamp, error) {
		_, _, path, _, buckets, err := model.QueryResourceMatcherRange(initTimeGraphQueryTestEnvironment(), "", "space", "1m",
			strconv.FormatInt(start.Unix(), 10), strconv.FormatInt(end.Unix(), 10), "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
		return path, buckets, err
	}
	for _, segmentPoints := range []int{64, points} {
		t.Run(fmt.Sprintf("preferred_hit_only_in_later_segment/segment-%d", segmentPoints), func(t *testing.T) {
			old := legacyRangeSegmentPoints
			legacyRangeSegmentPoints = segmentPoints
			t.Cleanup(func() { legacyRangeSegmentPoints = old })
			model := legacyRegressionModel(legacyAlternativeProvider(true))
			model.timeGraphVMQuery = legacyCandidateVM(t, grid[100], "", time.Time{})
			path, buckets, err := query(model)
			require.NoError(t, err)
			require.Equal(t, []string{"source", "target"}, path)
			require.Equal(t, legacyCandidateBuckets(grid[100:], "direct"), buckets)
		})
	}
	t.Run("later_segment_failure_discards_candidate", func(t *testing.T) {
		model := legacyRegressionModel(legacyAlternativeProvider(true))
		var directReads []legacyRangeSegment
		backend := legacyCandidateVM(t, start, "source_target_flow", grid[64])
		model.timeGraphVMQuery = func(ctx context.Context, q *structured.QueryTs, expr string, instant bool, queryStart, queryEnd time.Time, step time.Duration) (pl.Matrix, error) {
			if q.QueryList[0].FieldName == "source_target_flow" {
				directReads = append(directReads, legacyRangeSegment{start: queryStart, end: queryEnd, points: len(legacyRangeGridPoints(queryStart, queryEnd, step))})
			}
			return backend(ctx, q, expr, instant, queryStart, queryEnd, step)
		}
		path, buckets, err := query(model)
		require.NoError(t, err)
		require.Equal(t, legacyGridSegments(start, time.Minute, 64, 66), directReads, "the first segment succeeded before the failure")
		require.Equal(t, []string{"source", "middle", "target"}, path)
		require.Equal(t, legacyCandidateBuckets(grid, "indirect"), buckets, "the failed candidate's earlier segments are not merged")
	})
}
