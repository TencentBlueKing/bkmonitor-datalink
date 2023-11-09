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
	defaultMetric      = "metric123456789"
	defaultStorageID   = "test"
	defaultClusterName = "test"
	defaultHost        = "test"
	defaultGrpcPort    = 8089
	defaultStart       = time.Unix(0, 0)
	testTag            = "kafka"
)

type instance struct {
	name string
	opt  *influxdb.StreamSeriesSetOption
}

var _ tsdb.Instance = (*instance)(nil)

func (i instance) LabelNames(ctx context.Context, query *metadata.Query, start time.Time, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	panic("implement me")
}

func (i instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	panic("implement me")
}

func (i instance) LabelValues(ctx context.Context, query *metadata.Query, name string, start time.Time, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	panic("implement me")
}

func (i instance) Series(ctx context.Context, query *metadata.Query, start time.Time, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
	panic("implement me")
}

func (i instance) QueryRaw(ctx context.Context, query *metadata.Query, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	return influxdb.StartStreamSeriesSet(ctx, i.name, i.opt)
}

func (i instance) QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, error) {
	return nil, nil
}

func (i instance) Query(ctx context.Context, qs string, end time.Time) (promql.Vector, error) {
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
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "12345",
				},
				{
					Name:  "le",
					Value: "+Inf",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		},
		{
			Labels: []*remote.LabelPair{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "12345",
				},
				{
					Name:  "le",
					Value: "0.01",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		}, {
			Labels: []*remote.LabelPair{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "12345",
				},
				{
					Name:  "le",
					Value: "0.5",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		}, {
			Labels: []*remote.LabelPair{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "12345",
				},
				{
					Name:  "le",
					Value: "1",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		}, {
			Labels: []*remote.LabelPair{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "12345",
				},
				{
					Name:  "le",
					Value: "2",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		}, {
			Labels: []*remote.LabelPair{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "7890",
				},
				{
					Name:  "le",
					Value: "5",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		}, {
			Labels: []*remote.LabelPair{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "7890",
				},
				{
					Name:  "le",
					Value: "10",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		}, {
			Labels: []*remote.LabelPair{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "7890",
				},
				{
					Name:  "le",
					Value: "30",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
			},
		}, {
			Labels: []*remote.LabelPair{
				{
					Name:  "__name__",
					Value: defaultMetric,
				},
				{
					Name:  "kafka",
					Value: "7890",
				},
				{
					Name:  "le",
					Value: "60",
				},
			},
			Samples: []*remote.Sample{
				{TimestampMs: defaultStart.UnixMilli() + 0*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 1*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 2*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 3*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 4*60*1e3 + 3*1e3, Value: 175},
				{TimestampMs: defaultStart.UnixMilli() + 5*60*1e3 + 3*1e3, Value: 175},
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
	}, 0)

	fakeData(rootCtx)
	testCases := map[string]struct {
		db          string
		measurement string
		field       string

		q        string
		start    time.Time
		end      time.Time
		step     time.Duration
		expected string
	}{
		"a1": {
			q:     fmt.Sprintf("count(%s) by (%s)", defaultMetric, testTag),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{kafka="12345"} =>
5 @[60000]
5 @[120000]
5 @[180000]
5 @[240000]
5 @[300000]
5 @[360000]
5 @[420000]
5 @[480000]
5 @[540000]
5 @[600000]
{kafka="7890"} =>
4 @[60000]
4 @[120000]
4 @[180000]
4 @[240000]
4 @[300000]
4 @[360000]
4 @[420000]
4 @[480000]
4 @[540000]
4 @[600000]`,
		},
		"a2": {
			q:     fmt.Sprintf(`avg(idelta(%s[2m])) by (%s) != bool 0`, defaultMetric, testTag),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{kafka="12345"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
{kafka="7890"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]`,
		},
		"a3": {
			q:     fmt.Sprintf(`histogram_quantile(0.95, max by (le) (rate(%s[2m])))`, defaultMetric),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{} =>
NaN @[120000]
NaN @[180000]
NaN @[240000]
NaN @[300000]
NaN @[360000]`,
		},
		"a4": {
			q:     fmt.Sprintf(`label_replace({__name__="%s"}, "metric_name", "$1", "__name__", "metric(.*)")`, defaultMetric),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{__name__="metric123456789", kafka="12345", le="+Inf", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="12345", le="0.01", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="12345", le="0.5", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="12345", le="1", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="12345", le="2", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="7890", le="10", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="7890", le="30", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="7890", le="5", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]
{__name__="metric123456789", kafka="7890", le="60", metric_name="123456789"} =>
175 @[60000]
175 @[120000]
175 @[180000]
175 @[240000]
175 @[300000]
175 @[360000]
175 @[420000]
175 @[480000]
175 @[540000]
175 @[600000]`,
		},
		"a5": {
			q:     fmt.Sprintf(`delta(label_replace({__name__="%s"}, "metric_name", "$1", "__name__", "metric(.*)")[2m:1m])`, defaultMetric),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{kafka="12345", le="+Inf", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="0.01", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="0.5", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="1", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="2", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="10", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="30", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="5", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="60", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]`,
		},
		"a6": {
			q:     fmt.Sprintf(`sum(delta(label_replace({__name__="%s"}, "metric_name", "$1", "__name__", "metric(.*)")[2m:1m])) by (metric_name)`, defaultMetric),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{kafka="12345", le="+Inf", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="0.01", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="0.5", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="1", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="2", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="10", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="30", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="5", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="60", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]`,
		},
		"a7": {
			q:     fmt.Sprintf(`sum(label_replace(delta({__name__="%s"}[2m:1m]), "metric_name", "$1", "__name__", "metric(.*)")) by (metric_name)`, defaultMetric),
			start: defaultStart,
			end:   defaultStart.Add(time.Hour),
			step:  time.Minute,
			expected: `{kafka="12345", le="+Inf", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="0.01", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="0.5", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="1", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="12345", le="2", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="10", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="30", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="5", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]
{kafka="7890", le="60", metric_name="123456789"} =>
0 @[120000]
0 @[180000]
0 @[240000]
0 @[300000]
0 @[360000]
0 @[420000]
0 @[480000]
0 @[540000]
0 @[600000]
0 @[660000]`,
		},
	}

	for k, c := range testCases {
		t.Run(k, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(rootCtx, timeout)
			defer cancel()

			res, err := ins.QueryRange(ctx, c.q, c.start, c.end, c.step)

			a := res.String()
			assert.Nil(t, err)
			assert.Equal(t, c.expected, a)
		})
	}

}
