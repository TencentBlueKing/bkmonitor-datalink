package prometheus

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	promremote "github.com/prometheus/prometheus/storage/remote"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestLeftOpenTimeWindowIsOptIn(t *testing.T) {
	for _, mode := range []string{"instant", "range"} {
		for _, leftOpen := range []bool{false, true} {
			ctx := context.Background()
			if leftOpen {
				ctx = metadata.WithLeftOpenTimeWindow(ctx)
			}
			engine := promql.NewEngine(promql.EngineOpts{Timeout: time.Second, MaxSamples: 1000})
			queryable := storage.QueryableFunc(func(context.Context, int64, int64) (storage.Querier, error) {
				return &storage.MockQuerier{SelectMockFunction: func(bool, *storage.SelectHints, ...*labels.Matcher) storage.SeriesSet {
					return promremote.FromQueryResult(true, &prompb.QueryResult{Timeseries: []*prompb.TimeSeries{{
						Labels:  []prompb.Label{{Name: "__name__", Value: "a"}},
						Samples: []prompb.Sample{{Timestamp: 0, Value: 1}, {Timestamp: 1, Value: 1}, {Timestamp: 60000, Value: 1}},
					}}})
				}}, nil
			})
			backend := NewInstance(ctx, engine, queryable, time.Minute, 1)
			want := 3.0
			if leftOpen {
				want = 2
			}
			if mode == "instant" {
				vector, err := backend.DirectQuery(ctx, "count_over_time(a[1m])", time.Unix(60, 0))
				require.NoError(t, err)
				require.Len(t, vector, 1)
				require.Equal(t, want, vector[0].V)
			} else {
				matrix, _, err := backend.DirectQueryRange(ctx, "count_over_time(a[1m])", time.Unix(60, 0), time.Unix(120, 0), time.Minute)
				require.NoError(t, err)
				require.Len(t, matrix, 1)
				require.Equal(t, want, matrix[0].Points[0].V)
				if leftOpen {
					require.Len(t, matrix[0].Points, 1)
				} else {
					require.Len(t, matrix[0].Points, 2)
				}
			}
		}
	}
}
