// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	localprom "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	prombackend "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	pl "github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	promremote "github.com/prometheus/prometheus/storage/remote"
	"github.com/stretchr/testify/require"
)

func TestSharedTopologyMissingPhysicalInstanceFails(t *testing.T) {
	mock.Init()
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	ctx := initTimeGraphQueryTestEnvironment()
	old := localprom.GlobalEngine
	localprom.GlobalEngine = pl.NewEngine(pl.EngineOpts{Timeout: time.Second, MaxSamples: 10000})
	t.Cleanup(func() { localprom.GlobalEngine = old })
	provider := sharedTopologyQueryProvider().(contractSchemaProvider)
	provider.schemas = provider.schemas[:1]
	m := &Model{schemaProvider: provider}
	m.timeGraphQueryReference = func(qctx context.Context, q *structured.QueryTs) (metadata.QueryReference, error) {
		if err := q.ToTime(qctx); err != nil {
			return nil, err
		}
		ref := metadata.QueryReference{}
		for _, query := range q.QueryList {
			ref[query.ReferenceName] = []*metadata.QueryMetric{{
				ReferenceName: query.ReferenceName, MetricName: query.FieldName,
				QueryList: metadata.QueryList{{StorageType: metadata.ElasticsearchStorageType, StorageID: "999999999999999", TableID: "test.synthetic"}},
			}}
		}
		metadata.SetQueryReference(qctx, ref)
		require.Greater(t, ref.Count(), 0)
		return ref, nil
	}
	result, err := m.QuerySharedTopology(ctx, cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: 1700000000, LookBackDelta: "1m", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, MaxHops: 1})
	t.Logf("missing storage: err=%v result=%+v status=%+v", err, result, metadata.GetStatus(ctx))
	require.Error(t, err, "required relation whose only physical instance cannot be constructed must not be reported complete-empty")
}

func TestSharedTopologyLocalEngineWindowBoundaries(t *testing.T) {
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	const at int64 = 1700000100000
	for _, mode := range []string{"instant", "range"} {
		for _, format := range []string{"snapshots", cmdb.CompactTopologyFormat} {
			for _, stage := range []string{"relation-edge", "source-info"} {
				for _, offset := range []int64{-60000, -59999, 0} {
					t.Run(fmt.Sprintf("%s/%s/%s/%d", mode, format, stage, offset), func(t *testing.T) {
						ctx := initTimeGraphQueryTestEnvironment()
						provider := sharedTopologyQueryProvider().(contractSchemaProvider)
						provider.schemas = provider.schemas[:1]
						provider.fields["source"] = append(provider.fields["source"], "zone")
						engine := pl.NewEngine(pl.EngineOpts{Timeout: time.Second, MaxSamples: 10000})
						m := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
						m.timeGraphVMQuery = func(qctx context.Context, q *structured.QueryTs, expr string, instant bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
							require.True(t, metadata.IsStrictQueryFailures(qctx))
							queryable := storage.QueryableFunc(func(context.Context, int64, int64) (storage.Querier, error) {
								return &storage.MockQuerier{SelectMockFunction: func(bool, *storage.SelectHints, ...*labels.Matcher) storage.SeriesSet {
									ts := &prompb.TimeSeries{Labels: []prompb.Label{{Name: "source_id", Value: "a"}, {Name: "middle_id", Value: "b"}}, Samples: []prompb.Sample{{Timestamp: at + offset, Value: 1}}}
									if stage == "source-info" {
										if q.QueryList[0].FieldName == "source_info_relation" {
											ts.Labels = []prompb.Label{{Name: "source_id", Value: "a"}, {Name: "zone", Value: "west"}}
										} else {
											ts.Samples = []prompb.Sample{{Timestamp: at, Value: 1}, {Timestamp: at + 60000, Value: 1}, {Timestamp: at + 120000, Value: 1}}
										}
									}
									return promremote.FromQueryResult(true, &prompb.QueryResult{Timeseries: []*prompb.TimeSeries{ts}})
								}}, nil
							})
							backend := prombackend.NewInstance(qctx, engine, queryable, time.Minute, 2)
							if instant {
								vector, err := backend.DirectQuery(qctx, expr, end)
								return vectorToMatrix(vector), err
							}
							matrix, _, err := backend.DirectQueryRange(qctx, expr, start, end, step)
							return matrix, err
						}
						request := cmdb.SharedTopologyQuery{SpaceUID: "space", Timestamp: at / 1000, LookBackDelta: "1m", SourceType: "source", SourceInfo: cmdb.Matcher{"source_id": "a"}, MaxHops: 1, ResponseFormat: format}
						if stage == "source-info" {
							request.SourceExpandInfo = cmdb.Matcher{"zone": "west"}
						}
						points := 1
						if mode == "range" {
							request.Timestamp = 0
							request.StartTime = at / 1000
							request.EndTime = at/1000 + 120
							request.Step = "1m"
							points = 3
						}
						result, err := m.QuerySharedTopology(ctx, request)
						require.NoError(t, err)
						snapshots := result.Snapshots
						if format == cmdb.CompactTopologyFormat {
							snapshots = decodeCompactForTest(t, result.Compact)
						}
						require.Len(t, snapshots, points)
						for i, snapshot := range snapshots {
							require.False(t, snapshot.Partial)
							if i == 0 && offset > -60000 {
								require.Len(t, snapshot.Nodes, 2)
								require.Len(t, snapshot.Edges, 1)
							} else {
								require.Empty(t, snapshot.Nodes)
								require.Empty(t, snapshot.Edges)
							}
						}
					})
				}
			}
		}
	}
}

func TestLegacyTargetInfoFailureQuality(t *testing.T) {
	oldYolo := yoloMode
	yoloMode = true
	t.Cleanup(func() { yoloMode = oldYolo })
	for _, mode := range []string{"instant", "range"} {
		for _, tt := range []struct {
			name                         string
			infoErr                      error
			show, backendPartial, cancel bool
			wantReason                   string
			fatal                        bool
		}{
			{name: "success", show: true},
			{name: "optional-error", show: true, infoErr: errors.New("optional info unavailable"), wantReason: "target_info_failed"},
			{name: "merged-quality", show: true, backendPartial: true, infoErr: errors.New("optional info unavailable"), wantReason: "backend_partial;target_info_failed"},
			{name: "not-requested", infoErr: errors.New("must not be queried")},
			{name: "canceled", show: true, infoErr: context.Canceled, fatal: true},
			{name: "parent-canceled", show: true, infoErr: errors.New("interrupted backend"), cancel: true, fatal: true},
			{name: "deadline", show: true, infoErr: context.DeadlineExceeded, fatal: true},
			{name: "backend-byte-limit", show: true, infoErr: metadata.ErrBackendResponseTooLarge, fatal: true},
			{name: "sample-limit", show: true, infoErr: pl.ErrTooManySamples("target info"), fatal: true},
			{name: "engine-canceled", show: true, infoErr: pl.ErrQueryCanceled("target info"), fatal: true},
			{name: "engine-timeout", show: true, infoErr: pl.ErrQueryTimeout("target info"), fatal: true},
		} {
			t.Run(mode+"/"+tt.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(initTimeGraphQueryTestEnvironment())
				defer cancel()
				provider := sharedTopologyQueryProvider().(contractSchemaProvider)
				provider.schemas = provider.schemas[:1]
				provider.fields["middle"] = append(provider.fields["middle"], "zone")
				m := &Model{schemaProvider: provider, timeGraphQueryReference: timeGraphTestQueryReference}
				m.SetTimeGraphResolver(func(context.Context, string) (cmdb.CMDB, error) { return m, nil })
				infoCalls := 0
				m.timeGraphVMQuery = func(qctx context.Context, q *structured.QueryTs, _ string, _ bool, start, end time.Time, step time.Duration) (pl.Matrix, error) {
					require.True(t, metadata.IsStrictQueryFailures(qctx))
					switch q.QueryList[0].FieldName {
					case "source_middle_flow":
						if tt.backendPartial {
							metadata.SetStatus(qctx, metadata.QueryTsPartial, "synthetic route failure")
						}
						return contractMatrix(map[string]string{"source_id": "a", "middle_id": "b"}, 1700000000000, 1700000060000, 1700000120000), nil
					case "middle_info_relation":
						infoCalls++
						if tt.cancel {
							cancel()
						}
						return nil, tt.infoErr
					default:
						t.Fatalf("unexpected metric %s", q.QueryList[0].FieldName)
						return nil, nil
					}
				}
				var err error
				if mode == "instant" {
					var targets cmdb.Matchers
					_, _, _, _, targets, err = m.QueryResourceMatcher(ctx, "5m", "space", "1700000000", "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, tt.show, []cmdb.Resource{"source", "middle"})
					if !tt.fatal {
						require.Equal(t, cmdb.Matchers{{"middle_id": "b"}}, targets)
					}
				} else {
					var targets []cmdb.MatchersWithTimestamp
					_, _, _, _, targets, err = m.QueryResourceMatcherRange(ctx, "5m", "space", "1m", "1700000000", "1700000120", "middle", "source", cmdb.Matcher{"source_id": "a"}, nil, tt.show, []cmdb.Resource{"source", "middle"})
					if !tt.fatal {
						require.Len(t, targets, 3)
						for _, target := range targets {
							require.Equal(t, []cmdb.Matcher{{"middle_id": "b"}}, target.Matchers)
						}
					}
				}
				if tt.fatal {
					require.Error(t, err)
					if tt.cancel {
						require.ErrorIs(t, err, context.Canceled)
					}
					return
				}
				require.NoError(t, err)
				if tt.show {
					require.Greater(t, infoCalls, 0)
				} else {
					require.Zero(t, infoCalls)
				}
				status := metadata.GetStatus(ctx)
				if tt.wantReason == "" {
					require.Nil(t, status)
				} else {
					require.NotNil(t, status)
					require.Equal(t, metadata.QueryTsPartial, status.Code)
					require.Equal(t, tt.wantReason, status.Message)
				}
			})
		}
	}
}
