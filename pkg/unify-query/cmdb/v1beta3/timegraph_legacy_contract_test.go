package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	promremote "github.com/prometheus/prometheus/storage/remote"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	prombackend "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

func TestLegacyTimeGraphRootProjection(t *testing.T) {
	previous := yoloMode
	t.Cleanup(func() { yoloMode = previous })
	for _, yolo := range []bool{false, true} {
		for _, rangeQuery := range []bool{false, true} {
			for _, target := range []cmdb.Resource{"", "node"} {
				for _, tc := range []struct {
					name         string
					showInfo     bool
					noInfoFields bool
					missingRoute bool
					empty        bool
					wantError    bool
				}{
					{name: "no projection with data", wantError: true},
					{name: "no projection without route", missingRoute: true, wantError: true},
					{name: "projection without info fields", showInfo: true, noInfoFields: true, wantError: true},
					{name: "projected data", showInfo: true},
					{name: "projected empty", showInfo: true, empty: true},
					{name: "projected missing route", showInfo: true, missingRoute: true},
				} {
					t.Run(fmt.Sprintf("yolo=%t/range=%t/target=%s/%s", yolo, rangeQuery, target, tc.name), func(t *testing.T) {
						yoloMode = yolo
						ctx := initTimeGraphQueryTestEnvironment()
						provider := publicSourceExpandProvider().(contractSchemaProvider)
						if tc.noInfoFields {
							provider.fields["node"] = []string{"node_id"}
						}
						model := legacyRegressionModel(provider)
						routeCalls, queryCalls := 0, 0
						model.timeGraphQueryReference = func(ctx context.Context, query *structured.QueryTs) (metadata.QueryReference, error) {
							routeCalls++
							reference, err := timeGraphTestQueryReference(ctx, query)
							if tc.missingRoute {
								return metadata.QueryReference{}, err
							}
							return reference, err
						}
						model.timeGraphVMQuery = func(_ context.Context, query *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
							queryCalls++
							require.Equal(t, "node_info_relation", query.QueryList[0].FieldName)
							if tc.empty || tc.missingRoute {
								return nil, nil
							}
							var times []int64
							for ts := start; !ts.After(end); ts = ts.Add(step) {
								times = append(times, ts.UnixMilli())
							}
							return contractMatrix(map[string]string{"node_id": "n1", "zone": "east"}, times...), nil
						}
						var err error
						var targets cmdb.Matchers
						if rangeQuery {
							var buckets []cmdb.MatchersWithTimestamp
							_, _, _, _, buckets, err = model.QueryResourceMatcherRange(ctx, "", "space", "60s", "1700000000", "1700000060", target, "node", cmdb.Matcher{"node_id": "n1"}, nil, tc.showInfo, nil)
							for _, bucket := range buckets {
								targets = append(targets, bucket.Matchers...)
							}
						} else {
							_, _, _, _, targets, err = model.QueryResourceMatcher(ctx, "", "space", "1700000000", target, "node", cmdb.Matcher{"node_id": "n1"}, nil, tc.showInfo, nil)
						}
						if tc.wantError {
							require.ErrorContains(t, err, "表达式配置不能为空")
							require.Zero(t, routeCalls, "the old query fails before looking up physical routes")
							require.Zero(t, queryCalls)
							require.Nil(t, metadata.GetStatus(ctx), "invalid projections must not report missing routes")
							// New path APIs intentionally support roots without the legacy projection flag.
							var paths []cmdb.PathResourcesResult
							if rangeQuery {
								paths, err = model.QueryPathResourcesRange(ctx, "", "space", "60s", "1700000000", "1700000060", "node", []cmdb.Resource{"node"}, nil, cmdb.Matcher{"node_id": "n1"})
							} else {
								paths, err = model.QueryPathResources(ctx, "", "space", "1700000000", "node", []cmdb.Resource{"node"}, nil, cmdb.Matcher{"node_id": "n1"})
							}
							require.NoError(t, err)
							if tc.missingRoute {
								require.Empty(t, paths)
							} else {
								require.NotEmpty(t, paths)
							}
							return
						}
						require.NoError(t, err)
						require.Positive(t, routeCalls)
						if tc.empty || tc.missingRoute {
							require.Empty(t, targets)
						} else {
							require.NotEmpty(t, targets)
							for _, target := range targets {
								require.Equal(t, cmdb.Matcher{"node_id": "n1", "zone": "east"}, target)
							}
						}
						if tc.missingRoute {
							require.NotNil(t, metadata.GetStatus(ctx))
							require.Equal(t, metadata.QueryTsPartial, metadata.GetStatus(ctx).Code)
							require.Contains(t, metadata.GetStatus(ctx).Message, "source_info_no_route")
						} else {
							require.Nil(t, metadata.GetStatus(ctx))
						}
					})
				}
			}
		}
	}
}

func TestLegacyTimeGraphRangeBucketContract(t *testing.T) {
	const startMS int64 = 1700000070000 // Not aligned to a minute.
	for _, tc := range []struct {
		name     string
		lookback string
		step     string
		sampleMS int64
		wantMS   int64
	}{
		{name: "first bucket", sampleMS: startMS - 10000, wantMS: startMS},
		{name: "last bucket", sampleMS: startMS + 110000, wantMS: startMS + 120000},
		{name: "wide lookback does not resurrect", lookback: "10m", sampleMS: startMS - 120000},
		{name: "narrow lookback does not shorten bucket", lookback: "1s", sampleMS: startMS - 10000, wantMS: startMS},
		{name: "left boundary excluded", lookback: "10m", sampleMS: startMS - 60000},
		{name: "first millisecond included", sampleMS: startMS - 60000 + 1, wantMS: startMS},
		{name: "right boundary included", sampleMS: startMS, wantMS: startMS},
		{name: "fractional window retains older sample", step: "1500ms", sampleMS: startMS - 1250, wantMS: startMS},
		{name: "fractional left boundary excluded", step: "1500ms", sampleMS: startMS - 1500},
		{name: "fractional first millisecond included", step: "1500ms", sampleMS: startMS - 1499, wantMS: startMS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			provider := sharedTopologyQueryProvider().(contractSchemaProvider)
			provider.schemas = provider.schemas[:1]
			model := legacyRegressionModel(provider)
			step, start, end := "1m", strconv.FormatInt(startMS/1000, 10), strconv.FormatInt(startMS/1000+120, 10)
			wireStep := time.Minute
			if tc.step != "" {
				step, start, end = tc.step, strconv.FormatInt(startMS+123, 10), strconv.FormatInt(startMS+3987, 10)
				wireStep = time.Second
			}
			engine := pl.NewEngine(pl.EngineOpts{Timeout: 10 * time.Second, MaxSamples: 10000})
			model.timeGraphVMQuery = func(qctx context.Context, q *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
				require.False(t, instant)
				require.True(t, metadata.IsExactTimeGrid(qctx))
				require.True(t, q.NotTimeAlign)
				require.Equal(t, startMS, start.UnixMilli())
				require.Equal(t, wireStep, step)
				queryable := storage.QueryableFunc(func(context.Context, int64, int64) (storage.Querier, error) {
					return &storage.MockQuerier{SelectMockFunction: func(bool, *storage.SelectHints, ...*labels.Matcher) storage.SeriesSet {
						series := &prompb.TimeSeries{
							Labels:  []prompb.Label{{Name: "source_id", Value: "a"}, {Name: "middle_id", Value: "b"}},
							Samples: []prompb.Sample{{Timestamp: tc.sampleMS, Value: 1}},
						}
						return promremote.FromQueryResult(true, &prompb.QueryResult{Timeseries: []*prompb.TimeSeries{series}})
					}}, nil
				})
				backend := prombackend.NewInstance(qctx, engine, queryable, 5*time.Minute, 2)
				matrix, _, err := backend.DirectQueryRange(qctx, expr, start, end, step)
				return matrix, err
			}
			_, _, _, _, result, err := model.QueryResourceMatcherRange(ctx, tc.lookback, "space", step, start, end, "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
			require.NoError(t, err)
			if tc.wantMS == 0 {
				require.Empty(t, result)
			} else {
				require.Equal(t, []cmdb.MatchersWithTimestamp{{Timestamp: tc.wantMS, Matchers: cmdb.Matchers{{"middle_id": "b"}}}}, result)
			}
		})
	}
}

func TestLegacyTimeGraphFailureWithoutAlternativeHit(t *testing.T) {
	for _, mode := range []string{"instant", "range"} {
		for _, failed := range []string{"source_target_flow", "source_middle_flow"} {
			t.Run(mode+"/"+failed, func(t *testing.T) {
				ctx := initTimeGraphQueryTestEnvironment()
				model := legacyRegressionModel(legacyAlternativeProvider(true))
				failure := errors.New("relation unavailable")
				model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
					if q.QueryList[0].FieldName == failed {
						return nil, failure
					}
					return nil, nil
				}
				var err error
				if mode == "instant" {
					_, _, _, _, _, err = model.QueryResourceMatcher(ctx, "10m", "space", "1700000040", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
				} else {
					_, _, _, _, _, err = model.QueryResourceMatcherRange(ctx, "", "space", "1m", "1700000040", "1700000100", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
				}
				require.ErrorIs(t, err, failure)
			})
		}
	}
}

func TestLegacyTimeGraphRangeSelectsOnePathForWholeWindow(t *testing.T) {
	const firstMS int64 = 1700000040000
	const secondMS int64 = firstMS + 60000
	for _, tc := range []struct {
		name          string
		direct        pl.Matrix
		wantPath      []string
		wantTargets   []cmdb.MatchersWithTimestamp
		wantQueryList []string
	}{
		{
			name:          "first path wins even when second path hits later",
			direct:        legacyAlternativeMatrix("source_target_flow", firstMS),
			wantPath:      []string{"source", "target"},
			wantTargets:   []cmdb.MatchersWithTimestamp{{Timestamp: firstMS, Matchers: cmdb.Matchers{{"target_id": "direct"}}}},
			wantQueryList: []string{"source_target_flow"},
		},
		{
			name:          "second path fills window when first is empty",
			wantPath:      []string{"source", "middle", "target"},
			wantTargets:   []cmdb.MatchersWithTimestamp{{Timestamp: secondMS, Matchers: cmdb.Matchers{{"target_id": "indirect"}}}},
			wantQueryList: []string{"source_target_flow", "source_middle_flow", "middle_target_flow"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := legacyRegressionModel(legacyAlternativeProvider(true))
			var calls []string
			model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, _, _ time.Time, _ time.Duration) (pl.Matrix, error) {
				name := q.QueryList[0].FieldName
				calls = append(calls, name)
				if name == "source_target_flow" {
					return tc.direct, nil
				}
				return legacyAlternativeMatrix(name, secondMS), nil
			}
			_, source, path, target, buckets, err := model.QueryResourceMatcherRange(
				initTimeGraphQueryTestEnvironment(), "", "space", "1m", "1700000040", "1700000100",
				"target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil,
			)
			require.NoError(t, err)
			require.Equal(t, cmdb.Matcher{"source_id": "a"}, source)
			require.Equal(t, cmdb.Resource("target"), target)
			require.Equal(t, tc.wantPath, path)
			require.Equal(t, tc.wantTargets, buckets)
			require.Equal(t, tc.wantQueryList, calls)
		})
	}
}

func TestLegacyTimeGraphExplicitSelfLoop(t *testing.T) {
	for _, mode := range []string{"instant", "range"} {
		t.Run(mode, func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			provider := publicDynamicSelfProvider().(contractSchemaProvider)
			provider.schemas[0].Category = RelationCategoryStatic
			model := legacyRegressionModel(provider)
			model.timeGraphVMQuery = func(_ context.Context, q *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
				var timestamps []int64
				for ts := start; !ts.After(end); ts = ts.Add(step) {
					timestamps = append(timestamps, ts.UnixMilli())
				}
				return filterTopologyRegressionMatrix(q, contractMatrix(map[string]string{"id": "a"}, timestamps...)), nil
			}
			if mode == "instant" {
				_, _, path, _, result, err := model.QueryResourceMatcher(ctx, "10m", "space", "1700000070", "service", "service", cmdb.Matcher{"id": "a"}, nil, false, nil)
				require.NoError(t, err)
				require.Equal(t, []string{"service", "service"}, path)
				require.Equal(t, cmdb.Matchers{{"id": "a"}}, result)
			} else {
				_, _, path, _, result, err := model.QueryResourceMatcherRange(ctx, "10m", "space", "1m", "1700000070", "1700000130", "service", "service", cmdb.Matcher{"id": "a"}, nil, false, nil)
				require.NoError(t, err)
				require.Equal(t, []string{"service", "service"}, path)
				require.Equal(t, []cmdb.MatchersWithTimestamp{
					{Timestamp: 1700000070000, Matchers: []cmdb.Matcher{{"id": "a"}}},
					{Timestamp: 1700000130000, Matchers: []cmdb.Matcher{{"id": "a"}}},
				}, result)
			}
		})
	}
}

func TestLegacyTimeGraphRangeLookbackValidation(t *testing.T) {
	for _, lookback := range []string{"not-a-duration", "0s", "-1m"} {
		t.Run(lookback, func(t *testing.T) {
			model := legacyRegressionModel(sharedTopologyQueryProvider())
			model.timeGraphVMQuery = func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
				t.Fatal("invalid lookback must be rejected before querying")
				return nil, nil
			}
			_, _, _, _, _, err := model.QueryResourceMatcherRange(initTimeGraphQueryTestEnvironment(), lookback, "space", "1m", "1700000040", "1700000160", "target", "source", cmdb.Matcher{"source_id": "a"}, nil, false, nil)
			require.Error(t, err)
		})
	}
}

func TestLegacyTimeGraphRangeGridAcrossLoadStages(t *testing.T) {
	for _, shifted := range []bool{false, true} {
		t.Run(strconv.FormatBool(shifted), func(t *testing.T) {
			ctx := initTimeGraphQueryTestEnvironment()
			model := legacyRegressionModel(sharedTopologyQueryProvider())
			calls := make(map[string]bool)
			model.timeGraphVMQuery = func(qctx context.Context, q *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
				require.True(t, metadata.IsExactTimeGrid(qctx))
				require.True(t, q.NotTimeAlign)
				require.Equal(t, int64(1700000070000), start.UnixMilli())
				require.Equal(t, int64(1700000190000), end.UnixMilli())
				field := q.QueryList[0].FieldName
				calls[field] = true
				dimensions := map[string]map[string]string{
					"source_info_relation": {"source_id": "a", "region": "r"},
					"source_middle_flow":   {"source_id": "a", "middle_id": "b"},
					"middle_target_flow":   {"middle_id": "b", "target_id": "c"},
					"middle_info_relation": {"middle_id": "b"},
					"target_info_relation": {"target_id": "c"},
				}
				require.Contains(t, dimensions, field)
				var timestamps []int64
				for ts := start; !ts.After(end); ts = ts.Add(step) {
					timestamps = append(timestamps, ts.UnixMilli())
				}
				if shifted {
					timestamps[0]--
				}
				return contractMatrix(dimensions[field], timestamps...), nil
			}
			_, _, _, _, result, err := model.QueryResourceMatcherRange(ctx, "10m", "space", "1m", "1700000070", "1700000190", "target", "source", cmdb.Matcher{"source_id": "a"}, cmdb.Matcher{"region": "r"}, true, nil)
			if shifted {
				require.ErrorContains(t, err, "does not match")
				require.Empty(t, result)
				return
			}
			require.NoError(t, err)
			require.Len(t, calls, 5)
			require.Len(t, result, 3)
			for index, bucket := range result {
				require.Equal(t, int64(1700000070000+index*60000), bucket.Timestamp)
				require.Equal(t, []cmdb.Matcher{{"target_id": "c"}}, bucket.Matchers)
			}
		})
	}
}

func TestTimeGraphRejectsUnsupportedPrecisionBeforeQuery(t *testing.T) {
	oldYolo := yoloMode
	t.Cleanup(func() { yoloMode = oldYolo })
	for _, yolo := range []bool{false, true} {
		yoloMode = yolo
		for _, api := range []string{"paths", "relation_paths"} {
			for _, tc := range []struct {
				name, start, end, step string
				instant                bool
			}{
				{name: "instant milliseconds", start: "1700000040123", instant: true},
				{name: "range milliseconds", start: "1700000040123", end: "1700000100123", step: "1m"},
				{name: "fractional step", start: "1700000040", end: "1700000100", step: "1500ms"},
				{name: "sub-millisecond remainder", start: "1700000040", end: "1700000100", step: "1.000001s"},
			} {
				t.Run(strconv.FormatBool(yolo)+"/"+api+"/"+tc.name, func(t *testing.T) {
					model := legacyRegressionModel(sharedTopologyQueryProvider())
					model.timeGraphVMQuery = func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error) {
						t.Fatal("unsupported precision must fail before querying, including in YOLO mode")
						return nil, nil
					}
					ctx := initTimeGraphQueryTestEnvironment()
					matcher := cmdb.Matcher{"source_id": "a"}
					var err error
					switch api {
					case "paths":
						if tc.instant {
							_, err = model.QueryPathResources(ctx, "1m", "space", tc.start, "source", []cmdb.Resource{"target"}, nil, matcher)
						} else {
							_, err = model.QueryPathResourcesRange(ctx, "1m", "space", tc.step, tc.start, tc.end, "source", []cmdb.Resource{"target"}, nil, matcher)
						}
					case "relation_paths":
						paths := cmdb.RelationPathsFromResourcePaths([][]cmdb.Resource{{"source", "middle", "target"}})
						if tc.instant {
							_, err = model.QueryRelationPathResources(ctx, "1m", "space", tc.start, "source", []cmdb.Resource{"target"}, paths, matcher)
						} else {
							_, err = model.QueryRelationPathResourcesRange(ctx, "1m", "space", tc.step, tc.start, tc.end, "source", []cmdb.Resource{"target"}, paths, matcher)
						}
					}
					require.ErrorContains(t, err, "whole")
				})
			}
		}
	}
}
