// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	prommodel "github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

var legacyLookBackEvaluation = time.Unix(1700000000, 0)

// Each row is a period a relation was reported, in minutes before the evaluation
// time. Rows ending at 0 are still reported; the others stopped that long ago.
var legacyLookBackSamples = []struct {
	metric   string
	labels   []string
	from, to int
}{
	{"bkmonitor:node_with_pod_relation", []string{"pod", "stable", "node", "new"}, 240, 0},
	{"bkmonitor:node_with_pod_relation", []string{"pod", "moved", "node", "old"}, 240, 180},
	{"bkmonitor:node_with_pod_relation", []string{"pod", "moved", "node", "new"}, 120, 0},
	{"bkmonitor:node_with_pod_relation", []string{"pod", "moving", "node", "old"}, 240, 2},
	{"bkmonitor:node_with_pod_relation", []string{"pod", "moving", "node", "new"}, 2, 0},
	{"bkmonitor:node_with_pod_relation", []string{"pod", "gone", "node", "old"}, 240, 180},
	{"bkmonitor:node_with_pod_relation", []string{"pod", "orphan", "node", "retired"}, 240, 0},
	{"bkmonitor:node_with_system_relation", []string{"node", "old", "bk_target_ip", "192.0.2.1"}, 240, 0},
	{"bkmonitor:node_with_system_relation", []string{"node", "new", "bk_target_ip", "192.0.2.2"}, 240, 0},
	{"bkmonitor:node_with_system_relation", []string{"node", "retired", "bk_target_ip", "192.0.2.3"}, 240, 120},
}

// The backend evaluates both engines' queries on a real PromQL engine, so the
// window semantics under test come from the engine rather than from the fixture.
func newLegacyLookBackEngine(t *testing.T) func(statement string) pl.Vector {
	t.Helper()
	test, err := pl.NewTest(t, "")
	require.NoError(t, err)
	t.Cleanup(test.Close)
	require.NoError(t, test.Run())
	appender := test.Storage().Appender(test.Context())
	for _, series := range legacyLookBackSamples {
		pairs := append([]string{labels.MetricName, series.metric, "bcs_cluster_id", "c", "namespace", "ns"}, series.labels...)
		lset := labels.FromStrings(pairs...)
		for minute := series.from; minute >= series.to; minute-- {
			at := legacyLookBackEvaluation.Add(-time.Duration(minute) * time.Minute)
			_, err := appender.Append(0, lset, at.UnixMilli(), 1)
			require.NoError(t, err)
		}
	}
	require.NoError(t, appender.Commit())
	return func(statement string) pl.Vector {
		t.Helper()
		query, err := test.QueryEngine().NewInstantQuery(test.Queryable(), nil, statement, legacyLookBackEvaluation)
		require.NoError(t, err)
		defer query.Close()
		result := query.Exec(test.Context())
		require.NoError(t, result.Err)
		vector, err := result.Vector()
		require.NoError(t, err)
		return vector
	}
}

func legacyLookBackPod(pod string) cmdb.Matcher {
	return cmdb.Matcher{"bcs_cluster_id": "c", "namespace": "ns", "pod": pod}
}

// The expected answer of an omitted look_back_delta: the statement the old
// engine really generates for this path, with no window of its own.
func legacyInstantTargets(ctx context.Context, t *testing.T, instant func(string) pl.Vector, pod string) []string {
	t.Helper()
	factory := v1beta1.QueryFactory{
		Path: []string{"pod", "node", "system"}, Source: "pod", Target: "system",
		IndexMatcher: legacyLookBackPod(pod),
	}
	query, err := factory.MakeQueryTs()
	require.NoError(t, err)
	statement, err := query.ToPromQL(ctx)
	require.NoError(t, err)
	require.Contains(t, statement, "count by (bk_target_ip)")
	require.NotContains(t, statement, "count_over_time")
	targets := make([]string, 0)
	for _, sample := range instant(statement) {
		targets = append(targets, sample.Metric.Get("bk_target_ip"))
	}
	sort.Strings(targets)
	return targets
}

func TestLegacyTimeGraphInstantLookBack(t *testing.T) {
	v1beta1.InitSchemaProvider(relation.NewDefaultStaticSchemaProvider())
	instant := newLegacyLookBackEngine(t)
	for _, tc := range []struct {
		name         string
		pod          string
		lookBack     string
		inferSource  bool
		want         []string
		matchesOldVM bool
	}{
		{name: "stable pod", pod: "stable", want: []string{"192.0.2.2"}, matchesOldVM: true},
		{name: "pod that left a node hours ago", pod: "moved", want: []string{"192.0.2.2"}, matchesOldVM: true},
		{name: "pod moving inside the window", pod: "moving", want: []string{"192.0.2.1", "192.0.2.2"}, matchesOldVM: true},
		{name: "pod gone hours ago", pod: "gone", want: []string{}, matchesOldVM: true},
		{name: "node that stopped reporting its address", pod: "orphan", want: []string{}, matchesOldVM: true},
		{name: "moved without source type", pod: "moved", inferSource: true, want: []string{"192.0.2.2"}, matchesOldVM: true},
		{name: "gone without source type", pod: "gone", inferSource: true, want: []string{}, matchesOldVM: true},

		// An explicit window keeps its meaning. These rows also prove the fixture
		// holds the history that the default must not return.
		{name: "explicit default length", pod: "moved", lookBack: "5m", want: []string{"192.0.2.2"}},
		{name: "explicit window sees old node", pod: "moved", lookBack: "24h", want: []string{"192.0.2.1", "192.0.2.2"}},
		{name: "explicit window sees gone pod", pod: "gone", lookBack: "24h", want: []string{"192.0.2.1"}},
		{name: "explicit window sees retired address", pod: "orphan", lookBack: "24h", want: []string{"192.0.2.3"}},
		{name: "explicit window without source type", pod: "moved", lookBack: "24h", inferSource: true, want: []string{"192.0.2.1", "192.0.2.2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			if tc.matchesOldVM {
				require.Equal(t, tc.want, legacyInstantTargets(ctx, t, instant, tc.pod))
			}

			model := legacyRegressionModel(NewStaticSchemaProvider())
			model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, isInstant bool, _, end time.Time, _ time.Duration) (pl.Matrix, error) {
				require.True(t, isInstant)
				require.Equal(t, legacyLookBackEvaluation.Unix(), end.Unix())
				query := q.QueryList[0]
				window, err := query.TimeAggregation.Window.Duration()
				require.NoError(t, err)
				statement := fmt.Sprintf("count_over_time(bkmonitor:%s[%s])", query.FieldName, prommodel.Duration(window))
				return filteredTimeGraphMatrix(t, vectorToMatrix(instant(statement)), query.Conditions), nil
			}

			source := cmdb.Resource("pod")
			if tc.inferSource {
				source = ""
			}
			gotSource, _, path, _, matchers, err := model.QueryResourceMatcher(
				ctx, tc.lookBack, "space", fmt.Sprint(legacyLookBackEvaluation.Unix()),
				"system", source, legacyLookBackPod(tc.pod), nil, false, []cmdb.Resource{"node"},
			)
			require.NoError(t, err)
			require.Equal(t, cmdb.Resource("pod"), gotSource)
			if len(tc.want) > 0 {
				require.Equal(t, []string{"pod", "node", "system"}, path)
			}
			got := make([]string, 0, len(matchers))
			for _, matcher := range matchers {
				got = append(got, matcher["bk_target_ip"])
			}
			sort.Strings(got)
			require.Equal(t, tc.want, got)
		})
	}
}

// Only the instant compatibility entry follows the engine lookback. The other
// readers of an omitted value keep their own windows.
func TestOmittedLookBackOutsideLegacyInstant(t *testing.T) {
	windows := func(t *testing.T, model *Model) *[]string {
		t.Helper()
		seen := &[]string{}
		model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
			*seen = append(*seen, string(q.QueryList[0].TimeAggregation.Window))
			return nil, nil
		}
		return seen
	}

	t.Run("range follows step", func(t *testing.T) {
		model := legacyRegressionModel(NewStaticSchemaProvider())
		seen := windows(t, model)
		_, _, _, _, _, err := model.QueryResourceMatcherRange(
			initTimeGraphQueryTestEnvironment(), "", "space", "2m", "1700000000", "1700000120",
			"system", "pod", legacyLookBackPod("moved"), nil, false, []cmdb.Resource{"node"},
		)
		require.NoError(t, err)
		require.NotEmpty(t, *seen)
		for _, window := range *seen {
			require.Equal(t, "2m0s", window)
		}
	})

	t.Run("topology uses the configured default", func(t *testing.T) {
		model := legacyRegressionModel(sharedTopologyQueryProvider())
		seen := windows(t, model)
		_, err := model.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
			SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
			StartTime: 1700000000, EndTime: 1700000000, MaxHops: 1,
		})
		require.NoError(t, err)
		require.NotEmpty(t, *seen)
		for _, window := range *seen {
			require.Equal(t, "24h0m0s", window)
		}
	})
}
