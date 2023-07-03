// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/influxdata/influxdb/prometheus/remote"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/influxdb"
)

var (
	defaultMetric      = "metric"
	defaultStorageID   = "test"
	defaultClusterName = "test"
	defaultHost        = "test"
	defaultGrpcPort    = 8089
	defaultStart       = time.Unix(0, 0)
	testTag            = "__name"
)

type instance struct {
	name string
	opt  *influxdb.StreamSeriesSetOption
}

func (i instance) LabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	//TODO implement me
	panic("implement me")
}

func (i instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	//TODO implement me
	panic("implement me")
}

func (i instance) LabelValues(ctx context.Context, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	//TODO implement me
	panic("implement me")
}

func (i instance) Series(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
	//TODO implement me
	panic("implement me")
}

func (i instance) QueryRaw(ctx context.Context, query *metadata.Query, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	return influxdb.StartStreamSeriesSet(ctx, i.name, i.opt)
}

func (i instance) QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, error) {
	return nil, nil
}

func (i instance) Query(ctx context.Context, promql string, end time.Time, step time.Duration) (promql.Matrix, error) {
	return nil, nil
}

func (i instance) GetInstanceType() string {
	return "test"
}

type client struct {
	grpc.ClientStream

	cur  int
	data []*remote.TimeSeries
}

func (c *client) Recv() (*remote.TimeSeries, error) {
	c.cur++
	if c.cur < len(c.data) {
		return c.data[c.cur], nil
	} else {
		return nil, io.EOF
	}
}

func fakeData(ctx context.Context) {
	query := &metadata.Query{
		StorageID:   defaultStorageID,
		ClusterName: defaultClusterName,
	}
	metadata.SetQueryReference(ctx, metadata.QueryReference{
		defaultMetric: &metadata.QueryMetric{
			QueryList:     metadata.QueryList{query},
			ReferenceName: defaultMetric,
			MetricName:    defaultMetric,
		},
	})

	data := []*remote.TimeSeries{
		{
			Labels: []*remote.LabelPair{
				{
					Name:  testTag,
					Value: "kafka:12345",
				},
				{
					Name:  "ip",
					Value: "127.0.0.1",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3, Value: 1.01},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3, Value: 1.02},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3, Value: 1.03},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3, Value: 1.04},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3, Value: 1.05},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3, Value: 1.06},
				{TimestampMs: defaultStart.UnixMilli() + 6*60*1e3, Value: 1.07},
			},
		},
		{
			Labels: []*remote.LabelPair{
				{
					Name:  testTag,
					Value: "kafka:7890",
				},
				{
					Name:  "ip",
					Value: "127.0.0.1",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3, Value: 2.01},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3, Value: 2.02},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3, Value: 2.03},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3, Value: 2.04},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3, Value: 2.05},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3, Value: 2.06},
				{TimestampMs: defaultStart.UnixMilli() + 6*60*1e3, Value: 2.07},
			},
		},
		{
			Labels: []*remote.LabelPair{
				{
					Name:  testTag,
					Value: "kafka:7890",
				},
				{
					Name:  "ip",
					Value: "127.0.0.2",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3, Value: 3.01},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3, Value: 3.02},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3, Value: 3.03},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3, Value: 3.04},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3, Value: 3.05},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3, Value: 3.06},
				{TimestampMs: defaultStart.UnixMilli() + 6*60*1e3, Value: 3.07},
			},
		},
	}

	tsdb.SetStorage(defaultStorageID, &tsdb.Storage{
		Instance: &instance{
			name: defaultMetric,
			opt: &influxdb.StreamSeriesSetOption{
				Stream: &client{
					cur:  -1,
					data: data,
				},
			},
		},
	})
}

func TestQueryRange(t *testing.T) {
	log.InitTestLogger()
	rootCtx := context.Background()

	timeout := time.Second * 300
	engine := promql.NewEngine(promql.EngineOpts{
		Reg:        prometheus.DefaultRegisterer,
		Timeout:    timeout,
		MaxSamples: 1e10,
	})
	ins := NewInstance(rootCtx, engine, &QueryRangeStorage{
		QueryMaxRouting: 100,
		Timeout:         timeout,
	})

	fakeData(rootCtx)
	testCases := map[string]struct {
		db          string
		measurement string
		field       string

		q     string
		start time.Time
		end   time.Time
		step  time.Duration
	}{
		"a1": {
			q:     fmt.Sprintf("count(%s) by (%s)", defaultMetric, testTag),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
		},
	}

	for k, c := range testCases {
		t.Run(k, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(rootCtx, timeout)
			defer cancel()

			res, err := ins.QueryRange(ctx, c.q, c.start, c.end, c.step)
			assert.Nil(t, err)
			assert.Equal(t,
				`{__name="kafka:12345"} =>
1 @[0]
1 @[60000]
1 @[120000]
1 @[180000]
1 @[240000]
1 @[300000]
1 @[360000]
1 @[420000]
1 @[480000]
1 @[540000]
1 @[600000]
1 @[660000]
{__name="kafka:7890"} =>
2 @[0]
2 @[60000]
2 @[120000]
2 @[180000]
2 @[240000]
2 @[300000]
2 @[360000]
2 @[420000]
2 @[480000]
2 @[540000]
2 @[600000]
2 @[660000]`, res.String())
		})
	}

}
