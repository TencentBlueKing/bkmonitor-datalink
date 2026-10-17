package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	promremote "github.com/prometheus/prometheus/storage/remote"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	prombackend "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

func legacyRegressionModel(provider SchemaProvider) *Model {
	m := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
	m.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) { return m, nil })
	return m
}

func legacyAlternativeProvider(directFirst bool) contractSchemaProvider {
	p := sharedTopologyQueryProvider().(contractSchemaProvider)
	direct := RelationSchema{RelationType: "source_to_target", FromType: "source", ToType: "target", Category: RelationCategoryStatic, IsDirectional: true, MetricName: "source_target_flow"}
	if directFirst {
		p.schemas = append([]RelationSchema{direct}, p.schemas...)
	} else {
		p.schemas = append(p.schemas, direct)
	}
	return p
}

func legacyAlternativeMatrix(name string, timestamps ...int64) pl.Matrix {
	switch name {
	case "source_target_flow":
		return contractMatrix(map[string]string{"source_id": "a", "target_id": "direct"}, timestamps...)
	case "source_middle_flow":
		return contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, timestamps...)
	case "middle_target_flow":
		return contractMatrix(map[string]string{"middle_id": "b", "target_id": "indirect"}, timestamps...)
	}
	return nil
}

func TestLegacyTimeGraphCandidateFailureIsolation(t *testing.T) {
	for _, mode := range []string{"instant", "range"} {
		for _, failed := range []string{"source_target_flow", "source_middle_flow", "all"} {
			t.Run(mode+"/"+failed, func(t *testing.T) {
				ctx := initTimeGraphQueryTestEnvironment()
				m := legacyRegressionModel(legacyAlternativeProvider(true))
				m.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, start, _ time.Time, _ time.Duration) (pl.Matrix, error) {
					name := q.QueryList[0].FieldName
					if name == failed || failed == "all" {
						return nil, fmt.Errorf("candidate metric unavailable: %s", name)
					}
					return legacyAlternativeMatrix(name, start.UnixMilli()), nil
				}
				var err error
				if mode == "instant" {
					_, _, _, _, _, err = m.QueryResourceMatcher(ctx, "10m", "space", "1700000040", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
				} else {
					_, _, _, _, _, err = m.QueryResourceMatcherRange(ctx, "10m", "space", "1m", "1700000040", "1700000100", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
				}
				if failed == "all" {
					require.ErrorContains(t, err, "all relation paths failed")
				} else {
					require.NoError(t, err, "a healthy candidate path must survive an independent candidate failure")
				}
			})
		}
	}
}

func TestLegacyTimeGraphPrefersShortestPath(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	m := legacyRegressionModel(legacyAlternativeProvider(false))
	m.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, start, _ time.Time, _ time.Duration) (pl.Matrix, error) {
		return legacyAlternativeMatrix(q.QueryList[0].FieldName, start.UnixMilli()), nil
	}
	_, _, path, _, targets, err := m.QueryResourceMatcher(ctx, "10m", "space", "1700000040", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
	require.NoError(t, err)
	t.Logf("selected path=%v targets=%v", path, targets)
	require.Equal(t, []string{"source", "target"}, path)
	require.Equal(t, cmdb.Matchers{{"target_id": "direct"}}, targets)
}

func TestYoloTopologyExtendedGridKeepsAllPoints(t *testing.T) {
	old := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = old })
	timestamps := sharedGraphTestTimes(65)
	m := sharedTopologyQueryModel(map[string]pl.Matrix{
		"source_info_relation": contractMatrix(map[string]string{"source_id": "a"}, timestamps...),
		"source_middle_flow":   contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, timestamps...),
		"middle_target_flow":   nil,
	})
	result, err := m.QuerySharedTopology(initTimeGraphQueryTestEnvironment(), cmdb.SharedTopologyQuery{
		SpaceUID: "space", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"},
		StartTime: timestamps[0], EndTime: timestamps[64], Step: "1s", MaxHops: 1,
	})
	require.NoError(t, err)
	require.Len(t, result.Snapshots, 65)
	t.Logf("snapshot 64: nodes=%d edges=%d; snapshot 65: nodes=%d edges=%d partial=%t",
		len(result.Snapshots[63].Nodes), len(result.Snapshots[63].Edges),
		len(result.Snapshots[64].Nodes), len(result.Snapshots[64].Edges), result.Snapshots[64].Partial)
	require.Len(t, result.Snapshots[64].Nodes, 2)
	require.Len(t, result.Snapshots[64].Edges, 1)
}

func TestDirectedSchemaDefaultMetricMatchesWriter(t *testing.T) {
	for _, category := range []relation.RelationCategory{relation.RelationCategoryStatic, relation.RelationCategoryDynamic} {
		t.Run(string(category), func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			provider := NewSchemaProviderFromRelation(relation.NewStaticSchemaProvider(relation.StaticProviderConfig{
				ResourcePrimaryKeys: map[string][]string{"left": {"left_id"}, "right": {"right_id"}},
				RelationSchemas:     []relation.RelationSchema{{RelationName: "left_to_right", Category: category, FromType: "left", ToType: "right", IsDirectional: true}},
			}))
			m := legacyRegressionModel(provider)
			var queried []string
			m.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, start, _ time.Time, _ time.Duration) (pl.Matrix, error) {
				name := q.QueryList[0].FieldName
				queried = append(queried, name)
				if name != "left_to_right_flow" {
					return nil, nil
				}
				if category == relation.RelationCategoryDynamic {
					return contractMatrix(map[string]string{"from_left_id": "a", "to_right_id": "b"}, start.UnixMilli()), nil
				}
				return contractMatrix(map[string]string{"left_id": "a", "right_id": "b"}, start.UnixMilli()), nil
			}
			got, err := m.QueryPathResources(ctx, "10m", "space", "1700000040", "left", []cmdb.Resource{"right"}, nil, cmdb.Matcher{"left_id": "a"})
			require.NoError(t, err)
			t.Logf("queried metrics=%v paths=%v", queried, got)
			require.Len(t, got, 1, "the default metric name must match RelationDefinition.GetRelationName")
		})
	}
}

func TestLegacyTimeGraphRangeDoesNotResurrectStaleRelation(t *testing.T) {
	ctx := initTimeGraphQueryTestEnvironment()
	p := sharedTopologyQueryProvider().(contractSchemaProvider)
	p.schemas = p.schemas[:1]
	m := legacyRegressionModel(p)
	engine := pl.NewEngine(pl.EngineOpts{Timeout: 10 * time.Second, MaxSamples: 10000})
	m.timeGraphVMQuery = func(qctx context.Context, q *structured.QueryTs, expr string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
		t.Logf("evaluating expr=%s start=%s step=%s", expr, start, step)
		queryable := storage.QueryableFunc(func(context.Context, int64, int64) (storage.Querier, error) {
			return &storage.MockQuerier{SelectMockFunction: func(bool, *storage.SelectHints, ...*labels.Matcher) storage.SeriesSet {
				series := &prompb.TimeSeries{
					Labels:  []prompb.Label{{Name: "source_id", Value: "a"}, {Name: "middle_id", Value: "b"}},
					Samples: []prompb.Sample{{Timestamp: start.Add(-time.Hour).UnixMilli(), Value: 1}},
				}
				return promremote.FromQueryResult(true, &prompb.QueryResult{Timeseries: []*prompb.TimeSeries{series}})
			}}, nil
		})
		backend := prombackend.NewInstance(qctx, engine, queryable, 5*time.Minute, 2)
		matrix, _, err := backend.DirectQueryRange(qctx, expr, start, end, step)
		return matrix, err
	}
	_, _, _, _, targets, err := m.QueryResourceMatcherRange(ctx, "", "space", "1m", "1700000040", "1700000160", "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
	require.NoError(t, err)
	t.Logf("one-hour-old relation returned in %d one-minute buckets: %v", len(targets), targets)
	require.Empty(t, targets, "legacy range buckets only include relations active within the bucket window")
}

func TestLegacyTimeGraphFirstPathExecution(t *testing.T) {
	failure := errors.New("backend unavailable")
	for _, test := range []struct {
		name      string
		errors    []error
		hitAt     int
		wantCalls int
		wantError error
	}{
		{name: "first hit stops", errors: []error{nil, failure}, hitAt: 0, wantCalls: 1},
		{name: "failure falls back", errors: []error{failure, nil}, hitAt: 1, wantCalls: 2},
		{name: "empty falls back", errors: []error{nil, nil}, hitAt: 1, wantCalls: 2},
		{name: "empty does not mask prior failure", errors: []error{failure, nil}, hitAt: -1, wantCalls: 2, wantError: failure},
		{name: "failure after empty", errors: []error{nil, failure}, hitAt: -1, wantCalls: 2, wantError: failure},
		{name: "all empty", errors: []error{nil, nil}, hitAt: -1, wantCalls: 2},
		{name: "all fail", errors: []error{failure, failure}, hitAt: -1, wantCalls: 2, wantError: failure},
		{name: "cancellation stops", errors: []error{context.Canceled, nil}, hitAt: 1, wantCalls: 1, wantError: context.Canceled},
		{name: "deadline stops", errors: []error{context.DeadlineExceeded, nil}, hitAt: 1, wantCalls: 1, wantError: context.DeadlineExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			results, err := queryFirstTimeGraphPath(context.Background(), make([]resourcePath, len(test.errors)), func(resourcePath) ([]cmdb.PathResourcesResult, error) {
				index := calls
				calls++
				if index == test.hitAt {
					return []cmdb.PathResourcesResult{{TargetType: "target"}}, nil
				}
				return nil, test.errors[index]
			})
			require.Equal(t, test.wantCalls, calls)
			if test.wantError != nil {
				require.ErrorIs(t, err, test.wantError)
			} else {
				require.NoError(t, err)
				require.Equal(t, test.hitAt >= 0, len(results) > 0)
			}
		})
	}
}
