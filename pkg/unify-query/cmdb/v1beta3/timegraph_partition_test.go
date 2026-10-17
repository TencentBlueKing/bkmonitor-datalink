// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.
package v1beta3

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func partitionTestMatches(t testing.TB, ts *structured.QueryTs, ls labels.Labels) bool {
	t.Helper()
	groups, err := ts.QueryList[0].Conditions.AnalysisConditions()
	require.NoError(t, err)
	if len(groups) == 0 {
		return true
	}
	for _, group := range groups {
		matches := true
		for _, f := range group {
			require.Len(t, f.Value, 1)
			matcher, err := labels.NewMatcher(f.ToPromOperator(), f.DimensionName, f.Value[0])
			require.NoError(t, err)
			matches = matches && matcher.Matches(ls.Get(f.DimensionName))
		}
		if matches {
			return true
		}
	}
	return false
}

func TestTopologyPartitionComplementsPreserveORAndUnknownValues(t *testing.T) {
	q := &structured.QueryTs{Start: "1700000000", End: "1700003540", Step: "60s", QueryList: []*structured.Query{{Conditions: structured.Conditions{
		FieldList: []structured.ConditionField{
			{DimensionName: "tenant", Operator: structured.ConditionEqual, Value: []string{"one"}},
			{DimensionName: "id", Operator: structured.ConditionEqual, Value: []string{"a"}},
			{DimensionName: "tenant", Operator: structured.ConditionEqual, Value: []string{"two"}},
			{DimensionName: "id", Operator: structured.ConditionEqual, Value: []string{"b"}},
		}, ConditionList: []string{"and", "or", "and"},
	}}}}
	before, err := json.Marshal(q)
	require.NoError(t, err)
	chosen := []string{"", "a.b", "x|y", "line\nnext", "雪"}
	pattern := "(?:"
	for i, v := range chosen {
		if i > 0 {
			pattern += "|"
		}
		pattern += regexp.QuoteMeta(v)
	}
	pattern += ")"
	left, err := topologyQueryWithPartition(q, "partition", pattern, structured.ConditionRegEqual)
	require.NoError(t, err)
	right, err := topologyQueryWithPartition(q, "partition", pattern, structured.ConditionNotRegEqual)
	require.NoError(t, err)
	for _, tenant := range []string{"one", "two", "other"} {
		for _, id := range []string{"a", "b", "other"} {
			for _, value := range append(slices.Clone(chosen), "axb", "x", "new-late-value", "\\") {
				ls := labels.FromMap(map[string]string{"tenant": tenant, "id": id, "partition": value})
				count := 0
				if partitionTestMatches(t, left, ls) {
					count++
				}
				if partitionTestMatches(t, right, ls) {
					count++
				}
				if partitionTestMatches(t, q, ls) {
					require.Equal(t, 1, count)
				} else {
					require.Zero(t, count)
				}
			}
		}
	}
	missing := labels.FromMap(map[string]string{"tenant": "one", "id": "a"})
	require.True(t, partitionTestMatches(t, left, missing))
	require.False(t, partitionTestMatches(t, right, missing))
	require.Equal(t, q.Start, left.Start)
	require.Equal(t, q.End, right.End)
	left.QueryList[0].Conditions.FieldList[0].Value[0] = "mutated"
	after, err := json.Marshal(q)
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func partitionTestModel(t testing.TB, limited, partialProbe, failProbe, cancelProbe bool, records *partitionTestRecords, cancel context.CancelFunc) *Model {
	provider := selectorProvider()
	provider.resources = []ResourceType{"a", "b"}
	provider.primary = map[ResourceType][]string{"a": {"a_id"}, "b": {"b_id"}}
	provider.fields = map[ResourceType][]string{}
	provider.schemas = []RelationSchema{{RelationType: "ab", Category: RelationCategoryStatic, FromType: "a", ToType: "b", MetricName: "ab_relation"}}
	m := &Model{schemaProvider: provider, timeGraphQueryReference: selectorQueryReference}
	const start = int64(1700000000000)
	data := pl.Matrix{}
	for i := 0; i < 16; i++ {
		stamps := []int64{}
		for n := 0; n < 60; n++ {
			if i != 15 || n >= 30 {
				stamps = append(stamps, start+int64(n)*60000)
			}
		}
		data = append(data, contractMatrix(map[string]string{"a_id": fmt.Sprintf("root-%02d", i), "b_id": "target"}, stamps...)...)
	}
	m.timeGraphVMQueryWithPartial = func(ctx context.Context, ts *structured.QueryTs, _ string, _ bool, s, e time.Time, _ time.Duration) (pl.Matrix, bool, error) {
		records.mu.Lock()
		defer records.mu.Unlock()
		require.True(t, metadata.GetQueryParams(ctx).IsSkipK8s)
		if timeGraphQueryStage(ctx) == "relation-partition" {
			records.probes++
			require.Equal(t, start, s.UnixMilli())
			require.Equal(t, start, e.UnixMilli(), "partition discovery uses one point")
			if cancelProbe {
				cancel()
				return nil, false, context.Canceled
			}
			if failProbe {
				return nil, false, errors.New("optional probe unavailable")
			}
			field := ts.QueryList[0].AggregateMethodList[0].Dimensions[0]
			seen := map[string]bool{}
			result := pl.Matrix{}
			for _, series := range data {
				if !partitionTestMatches(t, ts, series.Metric) {
					continue
				}
				if len(series.Points) == 0 || series.Points[0].T != start {
					continue // A late identity is absent from this probe, not from either data branch.
				}
				v := series.Metric.Get(field)
				if partialProbe && v == "root-14" {
					continue
				}
				if !seen[v] {
					seen[v] = true
					result = append(result, contractMatrix(map[string]string{field: v}, start)...)
				}
			}
			return result, partialProbe, nil
		}
		require.Len(t, ts.QueryList[0].AggregateMethodList[0].Dimensions, 2, "probe must not mutate result grouping")
		records.fetches++
		selected := pl.Matrix{}
		points := 0
		for _, series := range data {
			if !partitionTestMatches(t, ts, series.Metric) {
				continue
			}
			copySeries := series
			copySeries.Points = nil
			for _, p := range series.Points {
				if p.T >= s.UnixMilli() && p.T <= e.UnixMilli() {
					copySeries.Points = append(copySeries.Points, p)
				}
			}
			if len(copySeries.Points) > 0 {
				selected = append(selected, copySeries)
				points += len(copySeries.Points)
			}
		}
		// Large repeated label payload makes time slicing expensive. Cardinality
		// slices can carry the entire grid with the same independent byte budget.
		bytes := len(selected)*1000 + points*10
		limit := records.responseLimit
		if limit == 0 {
			limit = 16300
		}
		if limited && bytes > limit {
			records.rejected++
			return nil, false, metadata.ErrBackendResponseTooLarge
		}
		records.series += len(selected)
		records.points += points
		for _, series := range selected {
			for _, p := range series.Points {
				records.coverage[fmt.Sprintf("%s/%d", series.Metric.String(), p.T)]++
			}
		}
		return selected, false, nil
	}
	return m
}

type partitionTestRecords struct {
	responseLimit                             int
	mu                                        sync.Mutex
	probes, fetches, rejected, series, points int
	coverage                                  map[string]int
}

func TestTopologyPartitionKeepsFullFramesAndReducesRepeatedLabels(t *testing.T) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode = true
	SharedTopologyPlanCandidates = true
	t.Cleanup(func() { yoloMode = oldYolo; SharedTopologyPlanCandidates = oldPlan })
	request := cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", SourceInfo: cmdb.Matcher{}, StartTime: 1700000000, EndTime: 1700003540, Step: "60s", MaxHops: 2}
	var baseline []string
	for _, name := range []string{"unlimited", "partitioned", "partial-probe", "failed-probe"} {
		t.Run(name, func(t *testing.T) {
			records := partitionTestRecords{coverage: map[string]int{}}
			result, err := partitionTestModel(t, name != "unlimited", name == "partial-probe", name == "failed-probe", false, &records, nil).QuerySharedTopology(initTimeGraphQueryTestEnvironment(), request)
			require.NoError(t, err)
			got := canonicalTopologyForTest(t, result.Snapshots)
			if name == "unlimited" {
				baseline = got
			} else {
				require.Equal(t, baseline, got)
			}
			require.Len(t, result.Snapshots, 60)
			for i, frame := range result.Snapshots {
				n := 16
				if i >= 30 {
					n = 17
				}
				require.Len(t, frame.Nodes, n)
				require.Len(t, frame.Edges, (n-1)*2)
				require.False(t, frame.Partial)
			}
			require.Equal(t, 930, records.points)
			for k, n := range records.coverage {
				require.Equal(t, 1, n, k)
			}
			if name == "partitioned" || name == "partial-probe" {
				require.Positive(t, records.probes)
				require.Less(t, records.series, 32)
			}
			if name == "failed-probe" {
				require.Equal(t, 1, records.probes)
				require.Greater(t, records.series, 32)
				require.Less(t, records.rejected, 20, "a late cardinality increase must replace the formerly successful width")
			}
			t.Logf("%s probes=%d fetches=%d rejected=%d returnedSeries=%d points=%d", name, records.probes, records.fetches, records.rejected, records.series, records.points)
		})
	}
}

func TestTopologyPartitionCancellationStopsBeforeChildQueries(t *testing.T) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode = true
	SharedTopologyPlanCandidates = true
	t.Cleanup(func() { yoloMode = oldYolo; SharedTopologyPlanCandidates = oldPlan })
	ctx, cancel := context.WithCancel(initTimeGraphQueryTestEnvironment())
	defer cancel()
	records := partitionTestRecords{coverage: map[string]int{}}
	_, err := partitionTestModel(t, true, false, false, true, &records, cancel).QuerySharedTopology(ctx, cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", StartTime: 1700000000, EndTime: 1700003540, Step: "60s", MaxHops: 2})
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, records.fetches, "preflight cancellation must stop before the full-range read")
	require.Equal(t, 1, records.probes)
	require.Zero(t, records.points)
}

func TestTopologyPartitionKeepsAttributeSensitivePlansUnchanged(t *testing.T) {
	oldYolo, oldPlan := yoloMode, SharedTopologyPlanCandidates
	yoloMode = true
	SharedTopologyPlanCandidates = true
	t.Cleanup(func() { yoloMode = oldYolo; SharedTopologyPlanCandidates = oldPlan })
	var baseline []string
	for _, limited := range []bool{false, true} {
		records := partitionTestRecords{coverage: map[string]int{}}
		model := partitionTestModel(t, limited, false, false, false, &records, nil)
		provider := model.schemaProvider.(contractSchemaProvider)
		provider.fields["a"] = []string{"a_id", "b_id"}
		model.schemaProvider = provider
		result, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{SpaceUID: "space", SourceType: "a", StartTime: 1700000000, EndTime: 1700003540, Step: "60s", MaxHops: 2})
		require.NoError(t, err)
		require.Zero(t, records.probes)
		got := canonicalTopologyForTest(t, result.Snapshots)
		if !limited {
			baseline = got
		} else {
			require.Equal(t, baseline, got)
		}
	}
}

func TestTopologyPartitionCompactPatternCoversSkewUnicodeAndLateValues(t *testing.T) {
	values := []string{"root-00", "root-01", "root-02", "root-03", "root-04"}
	matrix := pl.Matrix{}
	for i, v := range values {
		series := contractMatrix(map[string]string{"tenant": v}, 1700000000000)
		if i == 0 {
			series[0].Points[0].V = 1000
		}
		matrix = append(matrix, series...)
	}
	pattern, ok := topologyPartitionPattern(matrix, "tenant")
	require.True(t, ok)
	require.Less(t, len(pattern), 40)
	positive, err := labels.NewMatcher(labels.MatchRegexp, "tenant", pattern)
	require.NoError(t, err)
	negative, err := labels.NewMatcher(labels.MatchNotRegexp, "tenant", pattern)
	require.NoError(t, err)
	known := 0
	for _, v := range values {
		if positive.Matches(v) {
			known++
		}
	}
	require.Equal(t, 1, known, "isolate the heavy value")
	values = append(values, "", "\x00", "a.b", "x|y", "line\nnext", "雪", "😀", "new-late-value", "root-99")
	for _, v := range values {
		require.NotEqual(t, positive.Matches(v), negative.Matches(v), v)
		for _, op := range []string{structured.ConditionRegEqual, structured.ConditionNotRegEqual} {
			groups := structured.AllConditions{{{DimensionName: "tenant", Value: []string{pattern}, Operator: op}}}
			matched, err := groups.Compare("tenant", v)
			require.NoError(t, err)
			want := positive.Matches(v)
			if op == structured.ConditionNotRegEqual {
				want = !want
			}
			require.Equal(t, want, matched, "routing and VM regex semantics must agree")
		}
	}
	// An unhelpful discovery leaves the existing time-shard fallback available.
	unicodeOnly := append(contractMatrix(map[string]string{"tenant": "雪"}, 1700000000000), contractMatrix(map[string]string{"tenant": "雨"}, 1700000000000)...)
	_, ok = topologyPartitionPattern(unicodeOnly, "tenant")
	require.False(t, ok)
}
