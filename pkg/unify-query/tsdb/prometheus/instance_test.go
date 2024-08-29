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

func (i instance) QueryRaw(ctx context.Context, query *metadata.Query, start, end time.Time) storage.SeriesSet {
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

	data = []*remote.TimeSeries{
		{
			Samples: []*remote.Sample{
				//{Value: 89254.443482, TimestampMs: 1723788900000},
				//{Value: 89271.419844, TimestampMs: 1723788915000},
				//{Value: 89281.17737, TimestampMs: 1723788930000},
				//{Value: 89294.234335, TimestampMs: 1723788945000},
				//{Value: 89310.166, TimestampMs: 1723788960000},
				//{Value: 89321.718902, TimestampMs: 1723788975000},
				//{Value: 89330.943656, TimestampMs: 1723788990000},
				//{Value: 89341.437103, TimestampMs: 1723789005000},
				//{Value: 89356.714587, TimestampMs: 1723789020000},
				//{Value: 89368.57525, TimestampMs: 1723789035000},
				//{Value: 89377.891269, TimestampMs: 1723789050000},
				//{Value: 89389.237067, TimestampMs: 1723789065000},
				//{Value: 89403.894449, TimestampMs: 1723789080000},
				//{Value: 89418.157712, TimestampMs: 1723789095000},
				//{Value: 89429.995274, TimestampMs: 1723789110000},
				//{Value: 89449.490131, TimestampMs: 1723789125000},
				//{Value: 89463.821452, TimestampMs: 1723789140000},
				//{Value: 89463.821452, TimestampMs: 1723789155000},
				//{Value: 89477.877679, TimestampMs: 1723789170000},
				//{Value: 89491.614274, TimestampMs: 1723789185000},
				//{Value: 89514.946665, TimestampMs: 1723789200000},
				//{Value: 89525.187888, TimestampMs: 1723789215000},
				//{Value: 89536.199943, TimestampMs: 1723789230000},
				//{Value: 89546.285583, TimestampMs: 1723789245000},
				//{Value: 89561.879647, TimestampMs: 1723789260000},
				//{Value: 89576.112585, TimestampMs: 1723789275000},
				//{Value: 89587.296897, TimestampMs: 1723789290000},
				//{Value: 89602.584535, TimestampMs: 1723789305000},
				//{Value: 89616.72048, TimestampMs: 1723789320000},
				//{Value: 89629.581803, TimestampMs: 1723789335000},
				//{Value: 89629.581803, TimestampMs: 1723789350000},
				//{Value: 89644.597443, TimestampMs: 1723789365000},
				//{Value: 89661.223251, TimestampMs: 1723789380000},
				//{Value: 89681.051627, TimestampMs: 1723789395000},
				//{Value: 89691.466575, TimestampMs: 1723789410000},
				//{Value: 89691.466575, TimestampMs: 1723789425000},
				//{Value: 89708.374236, TimestampMs: 1723789440000},
				//{Value: 89719.580165, TimestampMs: 1723789455000},
				//{Value: 89734.549785, TimestampMs: 1723789470000},
				//{Value: 89751.197662, TimestampMs: 1723789485000},
				//{Value: 89764.596974, TimestampMs: 1723789500000},
				//{Value: 89775.94415, TimestampMs: 1723789515000},
				//{Value: 89791.728723, TimestampMs: 1723789530000},
				//{Value: 89805.817496, TimestampMs: 1723789545000},
				//{Value: 89817.497365, TimestampMs: 1723789560000},
				//{Value: 89832.928259, TimestampMs: 1723789575000},
				//{Value: 89844.88877, TimestampMs: 1723789590000},
				//{Value: 89853.642183, TimestampMs: 1723789605000},
				//{Value: 89869.9064, TimestampMs: 1723789620000},
				//{Value: 89881.06497, TimestampMs: 1723789635000},
				//{Value: 89897.480595, TimestampMs: 1723789650000},
				//{Value: 89897.480595, TimestampMs: 1723789665000},
				//{Value: 89920.95489, TimestampMs: 1723789680000},
				//{Value: 89933.523817, TimestampMs: 1723789695000},
				//{Value: 89943.552657, TimestampMs: 1723789710000},
				//{Value: 89954.967603, TimestampMs: 1723789725000},
				//{Value: 89967.46319, TimestampMs: 1723789740000},
				//{Value: 89978.93852, TimestampMs: 1723789755000},
				//{Value: 89990.527473, TimestampMs: 1723789770000},
				//{Value: 90003.530246, TimestampMs: 1723789785000},
				//{Value: 90016.504908, TimestampMs: 1723789800000},
				//{Value: 90026.261807, TimestampMs: 1723789815000},
				//{Value: 90040.375899, TimestampMs: 1723789830000},
				//{Value: 90051.124329, TimestampMs: 1723789845000},
				//{Value: 90072.787219, TimestampMs: 1723789860000},
				//{Value: 90083.422473, TimestampMs: 1723789875000},
				//{Value: 90096.186985, TimestampMs: 1723789890000},
				//{Value: 90107.191318, TimestampMs: 1723789905000},
				//{Value: 90122.454399, TimestampMs: 1723789920000},
				//{Value: 90131.422952, TimestampMs: 1723789935000},
				//{Value: 90148.22782, TimestampMs: 1723789950000},
				//{Value: 90159.659718, TimestampMs: 1723789965000},
				//{Value: 90171.257627, TimestampMs: 1723789980000},
				//{Value: 90186.055175, TimestampMs: 1723789995000},
				//{Value: 90194.81363, TimestampMs: 1723790010000},
				//{Value: 90204.743511, TimestampMs: 1723790025000},
				//{Value: 90219.01107, TimestampMs: 1723790040000},
				//{Value: 90229.438247, TimestampMs: 1723790055000},
				//{Value: 90244.301206, TimestampMs: 1723790070000},
				//{Value: 90258.517053, TimestampMs: 1723790085000},
				//{Value: 90274.690343, TimestampMs: 1723790100000},
				//{Value: 90290.162218, TimestampMs: 1723790115000},
				//{Value: 90299.116268, TimestampMs: 1723790130000},
				//{Value: 90309.50359, TimestampMs: 1723790145000},
				//{Value: 90323.006066, TimestampMs: 1723790160000},
				//{Value: 90342.314282, TimestampMs: 1723790175000},
				//{Value: 90354.520428, TimestampMs: 1723790190000},
				//{Value: 90365.376755, TimestampMs: 1723790205000},
				//{Value: 90376.388391, TimestampMs: 1723790220000},
				//{Value: 90389.190165, TimestampMs: 1723790235000},
				//{Value: 90402.389841, TimestampMs: 1723790250000},
				//{Value: 90418.680096, TimestampMs: 1723790265000},
				//{Value: 90432.830444, TimestampMs: 1723790280000},
				//{Value: 90432.830444, TimestampMs: 1723790295000},
				//{Value: 90448.193001, TimestampMs: 1723790310000},
				//{Value: 90463.955856, TimestampMs: 1723790325000},
				//{Value: 90473.328555, TimestampMs: 1723790340000},
				//{Value: 90485.445743, TimestampMs: 1723790355000},
				//{Value: 90500.308975, TimestampMs: 1723790370000},
				//{Value: 90513.996363, TimestampMs: 1723790385000},
				//{Value: 90524.583692, TimestampMs: 1723790400000},
				//{Value: 90539.642466, TimestampMs: 1723790415000},
				//{Value: 90554.297539, TimestampMs: 1723790430000},
				//{Value: 90568.418917, TimestampMs: 1723790445000},
				//{Value: 90578.465894, TimestampMs: 1723790460000},
				//{Value: 90590.308212, TimestampMs: 1723790475000},
				//{Value: 90610.786895, TimestampMs: 1723790490000},
				//{Value: 90619.853563, TimestampMs: 1723790505000},
				//{Value: 90633.344401, TimestampMs: 1723790520000},
				//{Value: 90643.642528, TimestampMs: 1723790535000},
				//{Value: 90654.855284, TimestampMs: 1723790550000},
				//{Value: 90671.192098, TimestampMs: 1723790565000},
				//{Value: 90685.384962, TimestampMs: 1723790580000},
				//{Value: 90695.301196, TimestampMs: 1723790595000},
				//{Value: 90706.41933, TimestampMs: 1723790610000},
				//{Value: 90717.570439, TimestampMs: 1723790625000},
				//{Value: 90730.782064, TimestampMs: 1723790640000},
				//{Value: 90740.953431, TimestampMs: 1723790655000},
				//{Value: 90757.409502, TimestampMs: 1723790670000},
				//{Value: 90769.1984, TimestampMs: 1723790685000},
				//{Value: 90783.309249, TimestampMs: 1723790700000},
				//{Value: 90797.414003, TimestampMs: 1723790715000},
				//{Value: 90807.548935, TimestampMs: 1723790730000},
				//{Value: 90820.106166, TimestampMs: 1723790745000},
				//{Value: 90835.907679, TimestampMs: 1723790760000},
				//{Value: 90846.165194, TimestampMs: 1723790775000},
				//{Value: 90863.011773, TimestampMs: 1723790790000},
				//{Value: 90874.307508, TimestampMs: 1723790805000},
				//{Value: 90889.763538, TimestampMs: 1723790820000},
				//{Value: 90900.335085, TimestampMs: 1723790835000},
				//{Value: 90908.988402, TimestampMs: 1723790850000},
				//{Value: 90920.469334, TimestampMs: 1723790865000},
				//{Value: 90933.826482, TimestampMs: 1723790880000},
				//{Value: 90947.39323, TimestampMs: 1723790895000},
				//{Value: 90963.091511, TimestampMs: 1723790910000},
				//{Value: 90977.368179, TimestampMs: 1723790925000},
				//{Value: 90986.163197, TimestampMs: 1723790940000},
				//{Value: 90995.061955, TimestampMs: 1723790955000},
				//{Value: 91006.194755, TimestampMs: 1723790970000},
				//{Value: 91029.735577, TimestampMs: 1723790985000},
				//{Value: 91029.735577, TimestampMs: 1723791000000},
				//{Value: 91044.818113, TimestampMs: 1723791015000},
				//{Value: 91059.875089, TimestampMs: 1723791030000},
				//{Value: 91080.865333, TimestampMs: 1723791045000},
				//{Value: 91090.649849, TimestampMs: 1723791060000},
				//{Value: 91104.369272, TimestampMs: 1723791075000},
				//{Value: 91117.172565, TimestampMs: 1723791090000},
				//{Value: 91131.69536, TimestampMs: 1723791105000},
				//{Value: 91131.69536, TimestampMs: 1723791120000},
				//{Value: 91148.506416, TimestampMs: 1723791135000},
				//{Value: 91170.250174, TimestampMs: 1723791150000},
				//{Value: 91180.622433, TimestampMs: 1723791165000},
				//{Value: 91193.951386, TimestampMs: 1723791180000},
				//{Value: 91203.445343, TimestampMs: 1723791195000},
				//{Value: 91216.939029, TimestampMs: 1723791210000},
				//{Value: 91225.686877, TimestampMs: 1723791225000},
				//{Value: 91241.017744, TimestampMs: 1723791240000},
				//{Value: 91250.610082, TimestampMs: 1723791255000},
				//{Value: 91266.805103, TimestampMs: 1723791270000},
				//{Value: 91280.751589, TimestampMs: 1723791285000},
				//{Value: 91293.822708, TimestampMs: 1723791300000},
				//{Value: 91303.847491, TimestampMs: 1723791315000},
				//{Value: 91323.330187, TimestampMs: 1723791330000},
				//{Value: 91333.262969, TimestampMs: 1723791345000},
				//{Value: 91344.740628, TimestampMs: 1723791360000},
				//{Value: 91354.903372, TimestampMs: 1723791375000},
				//{Value: 91367.906703, TimestampMs: 1723791390000},
				//{Value: 91378.553063, TimestampMs: 1723791405000},
				//{Value: 91394.636486, TimestampMs: 1723791420000},
				//{Value: 91410.204225, TimestampMs: 1723791435000},
				//{Value: 91422.493961, TimestampMs: 1723791450000},
				//{Value: 91433.819912, TimestampMs: 1723791465000},
				//{Value: 91443.898819, TimestampMs: 1723791480000},
				//{Value: 91459.981929, TimestampMs: 1723791495000},
				//{Value: 91470.091996, TimestampMs: 1723791510000},
				//{Value: 91486.074301, TimestampMs: 1723791525000},
				//{Value: 91500.000259, TimestampMs: 1723791540000},
				//{Value: 91500.000259, TimestampMs: 1723791555000},
				//{Value: 91525.132208, TimestampMs: 1723791570000},
				//{Value: 91538.572387, TimestampMs: 1723791585000},
				//{Value: 91552.01917, TimestampMs: 1723791600000},
				//{Value: 91563.870545, TimestampMs: 1723791615000},
				//{Value: 91574.424943, TimestampMs: 1723791630000},
				//{Value: 91589.326142, TimestampMs: 1723791645000},
				//{Value: 91599.479193, TimestampMs: 1723791660000},
				//{Value: 91616.369025, TimestampMs: 1723791675000},
				//{Value: 91616.369025, TimestampMs: 1723791690000},
				//{Value: 91641.707954, TimestampMs: 1723791705000},
				//{Value: 91650.981637, TimestampMs: 1723791720000},
				//{Value: 91663.434798, TimestampMs: 1723791735000},
				//{Value: 91679.998225, TimestampMs: 1723791750000},
				//{Value: 91679.998225, TimestampMs: 1723791765000},
				//{Value: 91703.713244, TimestampMs: 1723791780000},
				//{Value: 91715.546001, TimestampMs: 1723791795000},
				//{Value: 91715.546001, TimestampMs: 1723791810000},
				//{Value: 91732.134697, TimestampMs: 1723791825000},
				//{Value: 91747.85583, TimestampMs: 1723791840000},
				//{Value: 91759.740987, TimestampMs: 1723791855000},
				//{Value: 91771.015463, TimestampMs: 1723791870000},
				//{Value: 91784.90931, TimestampMs: 1723791885000},
				//{Value: 91800.034562, TimestampMs: 1723791900000},
				//{Value: 91813.36678, TimestampMs: 1723791915000},
				//{Value: 91826.134639, TimestampMs: 1723791930000},
				//{Value: 91840.4055, TimestampMs: 1723791945000},
				//{Value: 91850.050692, TimestampMs: 1723791960000},
				//{Value: 91864.529685, TimestampMs: 1723791975000},
				//{Value: 91879.74966, TimestampMs: 1723791990000},
				//{Value: 91896.755603, TimestampMs: 1723792005000},
				//{Value: 91896.755603, TimestampMs: 1723792020000},
				//{Value: 91912.51985, TimestampMs: 1723792035000},
				//{Value: 91933.378418, TimestampMs: 1723792050000},
				//{Value: 91947.25425, TimestampMs: 1723792065000},
				//{Value: 91960.974708, TimestampMs: 1723792080000},
				//{Value: 91970.51603, TimestampMs: 1723792095000},
				//{Value: 91980.078045, TimestampMs: 1723792110000},
				//{Value: 91995.510175, TimestampMs: 1723792125000},
				//{Value: 92008.588068, TimestampMs: 1723792140000},
				//{Value: 92022.377881, TimestampMs: 1723792155000},
				//{Value: 92032.231931, TimestampMs: 1723792170000},
				//{Value: 92045.974103, TimestampMs: 1723792185000},
				//{Value: 92057.361389, TimestampMs: 1723792200000},
				//{Value: 92073.260322, TimestampMs: 1723792215000},
				//{Value: 92082.037532, TimestampMs: 1723792230000},
				//{Value: 92095.530672, TimestampMs: 1723792245000},
				//{Value: 92107.013675, TimestampMs: 1723792260000},
				//{Value: 92119.510858, TimestampMs: 1723792275000},
				//{Value: 92132.906524, TimestampMs: 1723792290000},
				//{Value: 92145.149405, TimestampMs: 1723792305000},
				//{Value: 92160.632786, TimestampMs: 1723792320000},
				//{Value: 92171.862029, TimestampMs: 1723792335000},
				//{Value: 92186.777817, TimestampMs: 1723792350000},
				//{Value: 92199.992426, TimestampMs: 1723792365000},
				//{Value: 92215.714791, TimestampMs: 1723792380000},
				//{Value: 92215.714791, TimestampMs: 1723792395000},
				//{Value: 92231.82981, TimestampMs: 1723792410000},
				//{Value: 92244.71939, TimestampMs: 1723792425000},
				//{Value: 92258.157934, TimestampMs: 1723792440000},
				//{Value: 92276.828395, TimestampMs: 1723792455000},
				//{Value: 92288.292571, TimestampMs: 1723792470000},
				//{Value: 92288.292571, TimestampMs: 1723792485000},

				// 2024-08-16 12:14:19
				{Value: 83110.230240566, TimestampMs: 1723781659695},
				// 2024-08-16 12:14:31
				{Value: 83119.89771368, TimestampMs: 1723781671058},
				// 14
				{Value: 83133.668603386, TimestampMs: 1723781687260},
				//
				{Value: 83147.653484962, TimestampMs: 1723781703717},
				{Value: 83157.071587397, TimestampMs: 1723781714799},
				{Value: 83171.669781249, TimestampMs: 1723781731964},
				{Value: 83181.811100567, TimestampMs: 1723781743907},
				{Value: 83202.702299213, TimestampMs: 1723781768507},
				{Value: 83218.092858449, TimestampMs: 1723781786611},
				{Value: 83229.663421875, TimestampMs: 1723781800221},
				{Value: 83239.297822156, TimestampMs: 1723781811557},
				{Value: 83248.615420503, TimestampMs: 1723781822522},
				{Value: 83259.889640533, TimestampMs: 1723781835794},
				{Value: 83275.307092186, TimestampMs: 1723781853931},
				{Value: 83288.462744969, TimestampMs: 1723781869409},
				{Value: 83305.330399466, TimestampMs: 1723781889250},
				{Value: 83322.062551678, TimestampMs: 1723781908944},
				{Value: 83322.062551678, TimestampMs: 1723781908944},
				{Value: 83348.089331753, TimestampMs: 1723781939566},
				{Value: 83360.367689322, TimestampMs: 1723781954012},
				{Value: 83371.169228288, TimestampMs: 1723781966718},
				{Value: 83381.452888248, TimestampMs: 1723781978819},
				{Value: 83391.218637505, TimestampMs: 1723781990309},
				{Value: 83413.079859323, TimestampMs: 1723782016025},
				{Value: 83422.446287501, TimestampMs: 1723782027045},
				{Value: 83438.201603583, TimestampMs: 1723782045595},
				{Value: 83449.84403652, TimestampMs: 1723782059280},
				{Value: 83461.737374577, TimestampMs: 1723782073268},
				{Value: 83471.474445544, TimestampMs: 1723782084742},
				{Value: 83481.967704588, TimestampMs: 1723782097091},
				{Value: 83502.994504377, TimestampMs: 1723782121831},
				{Value: 83517.097210706, TimestampMs: 1723782138417},
				{Value: 83517.097210706, TimestampMs: 1723782138417},
				{Value: 83533.634034338, TimestampMs: 1723782157868},
				{Value: 83557.936638677, TimestampMs: 1723782186471},
				{Value: 83567.707069277, TimestampMs: 1723782197968},
				{Value: 83579.86422778, TimestampMs: 1723782212285},
				{Value: 83590.57310578, TimestampMs: 1723782224886},
				{Value: 83599.637284459, TimestampMs: 1723782235547},
				{Value: 83611.89931635, TimestampMs: 1723782249968},
				{Value: 83636.249048845, TimestampMs: 1723782278627},
				{Value: 83636.249048845, TimestampMs: 1723782278627},
				{Value: 83651.360371096, TimestampMs: 1723782296419},
				{Value: 83667.688770921, TimestampMs: 1723782315626},
				{Value: 83682.595590758, TimestampMs: 1723782333163},
				{Value: 83697.930974282, TimestampMs: 1723782351207},
				{Value: 83713.996785709, TimestampMs: 1723782370108},
				{Value: 83726.730892809, TimestampMs: 1723782385088},
				{Value: 83740.495341398, TimestampMs: 1723782401294},
				{Value: 83752.122844543, TimestampMs: 1723782414970},
				{Value: 83764.467953563, TimestampMs: 1723782429501},
				{Value: 83774.025976015, TimestampMs: 1723782440750},
				{Value: 83784.08183661, TimestampMs: 1723782452579},
				{Value: 83798.448884967, TimestampMs: 1723782469483},
				{Value: 83809.829149871, TimestampMs: 1723782482873},
				{Value: 83824.122434913, TimestampMs: 1723782499699},
				{Value: 83839.23376819, TimestampMs: 1723782517473},
				{Value: 83851.435562208, TimestampMs: 1723782531827},
				{Value: 83865.753165636, TimestampMs: 1723782548674},
				{Value: 83881.084374015, TimestampMs: 1723782566911},
				{Value: 83894.489397571, TimestampMs: 1723782582674},
				{Value: 83894.489397571, TimestampMs: 1723782582674},
				{Value: 83920.336955168, TimestampMs: 1723782613091},
				{Value: 83935.777101942, TimestampMs: 1723782631247},
				{Value: 83944.958825087, TimestampMs: 1723782642049},
				{Value: 83954.930618518, TimestampMs: 1723782653796},
				{Value: 83964.61676056, TimestampMs: 1723782665176},
				{Value: 83976.311339758, TimestampMs: 1723782678940},
				{Value: 83992.240992968, TimestampMs: 1723782697684},
				{Value: 84005.192565515, TimestampMs: 1723782712920},
				{Value: 84018.50751575, TimestampMs: 1723782728590},
				{Value: 84031.419153556, TimestampMs: 1723782743789},
				{Value: 84044.052355138, TimestampMs: 1723782758649},
				{Value: 84066.50921023, TimestampMs: 1723782785064},
				{Value: 84076.887606491, TimestampMs: 1723782797286},
				{Value: 84088.660599813, TimestampMs: 1723782811139},
				{Value: 84100.347848796, TimestampMs: 1723782824897},
				{Value: 84118.886448797, TimestampMs: 1723782846704},
				{Value: 84118.886448797, TimestampMs: 1723782846704},
				{Value: 84145.303297995, TimestampMs: 1723782877795},
				{Value: 84157.022505363, TimestampMs: 1723782891569},
				{Value: 84171.570885322, TimestampMs: 1723782908697},
				{Value: 84171.570885322, TimestampMs: 1723782908697},
				{Value: 84185.029034896, TimestampMs: 1723782924536},
				{Value: 84200.795632308, TimestampMs: 1723782943074},
				{Value: 84217.338205034, TimestampMs: 1723782962543},
				{Value: 84232.566871791, TimestampMs: 1723782980478},
				{Value: 84247.952157671, TimestampMs: 1723782998589},
				{Value: 84259.687514364, TimestampMs: 1723783012395},
				{Value: 84270.870575426, TimestampMs: 1723783025544},
				{Value: 84283.327474379, TimestampMs: 1723783040211},
				{Value: 84292.834037796, TimestampMs: 1723783051384},
				{Value: 84307.01297336, TimestampMs: 1723783068060},
				{Value: 84321.282299361, TimestampMs: 1723783084865},
				{Value: 84335.160947557, TimestampMs: 1723783101198},
				{Value: 84349.886931705, TimestampMs: 1723783118519},
				{Value: 84365.812366229, TimestampMs: 1723783137248},
				{Value: 84379.187285421, TimestampMs: 1723783152998},
				{Value: 84379.187285421, TimestampMs: 1723783152998},
				{Value: 84395.868999243, TimestampMs: 1723783172620},
				{Value: 84408.729856768, TimestampMs: 1723783187749},
				{Value: 84423.244350773, TimestampMs: 1723783204825},
				{Value: 84436.261392845, TimestampMs: 1723783220144},
				{Value: 84452.379284541, TimestampMs: 1723783239108},
				{Value: 84464.15768923, TimestampMs: 1723783252962},
				{Value: 84473.640088229, TimestampMs: 1723783264123},
				{Value: 84496.70578817, TimestampMs: 1723783291251},
				{Value: 84505.274005051, TimestampMs: 1723783301336},
				{Value: 84513.825175274, TimestampMs: 1723783311406},
				{Value: 84535.988590779, TimestampMs: 1723783337476},
				{Value: 84549.278094191, TimestampMs: 1723783353113},
				{Value: 84559.065807903, TimestampMs: 1723783364626},
				{Value: 84575.328212243, TimestampMs: 1723783383755},
				{Value: 84585.130274946, TimestampMs: 1723783395304},
				{Value: 84597.818248467, TimestampMs: 1723783410232},
				{Value: 84608.916253026, TimestampMs: 1723783423291},
				{Value: 84623.16625411, TimestampMs: 1723783440053},
				{Value: 84631.944909125, TimestampMs: 1723783450378},
				{Value: 84647.499217182, TimestampMs: 1723783468687},
				{Value: 84660.506692489, TimestampMs: 1723783484006},
				{Value: 84674.60893123, TimestampMs: 1723783500583},
				{Value: 84684.505685069, TimestampMs: 1723783512235},
				{Value: 84705.219658059, TimestampMs: 1723783536607},
				{Value: 84714.060915456, TimestampMs: 1723783547009},
				{Value: 84728.836124879, TimestampMs: 1723783564397},
				{Value: 84744.277426466, TimestampMs: 1723783582557},
				{Value: 84754.042469269, TimestampMs: 1723783594040},
				{Value: 84763.213702676, TimestampMs: 1723783604841},
				{Value: 84777.274878899, TimestampMs: 1723783621373},
				{Value: 84798.384659124, TimestampMs: 1723783646224},
				{Value: 84811.581806096, TimestampMs: 1723783661741},
				{Value: 84811.581806096, TimestampMs: 1723783661741},
				{Value: 84837.063572746, TimestampMs: 1723783691729},
				{Value: 84837.063572746, TimestampMs: 1723783691729},
				{Value: 84862.534809174, TimestampMs: 1723783721702},
				{Value: 84862.534809174, TimestampMs: 1723783721702},
				{Value: 84889.552986904, TimestampMs: 1723783753474},
				{Value: 84889.552986904, TimestampMs: 1723783753474},
				{Value: 84904.59475183, TimestampMs: 1723783771171},
				{Value: 84919.763932416, TimestampMs: 1723783789025},
				{Value: 84930.610916691, TimestampMs: 1723783801792},
				{Value: 84942.863905387, TimestampMs: 1723783816203},
				{Value: 84966.699414609, TimestampMs: 1723783844241},
				{Value: 84977.086454212, TimestampMs: 1723783856458},
				{Value: 84986.667846844, TimestampMs: 1723783867734},
				{Value: 85002.292679538, TimestampMs: 1723783886117},
				{Value: 85011.128037669, TimestampMs: 1723783896512},
				{Value: 85021.452751276, TimestampMs: 1723783908656},
				{Value: 85046.656737888, TimestampMs: 1723783938316},
				{Value: 85057.149303109, TimestampMs: 1723783950657},
				{Value: 85072.792663737, TimestampMs: 1723783969061},
				{Value: 85081.322519298, TimestampMs: 1723783979106},
				{Value: 85098.016534644, TimestampMs: 1723783998744},
				{Value: 85108.933812104, TimestampMs: 1723784011582},
				{Value: 85124.353810677, TimestampMs: 1723784029730},
				{Value: 85138.578992055, TimestampMs: 1723784046459},
				{Value: 85149.470757779, TimestampMs: 1723784059272},
				{Value: 85159.861953756, TimestampMs: 1723784071513},
				{Value: 85174.736633926, TimestampMs: 1723784089017},
				{Value: 85184.332789852, TimestampMs: 1723784100309},
				{Value: 85197.244814493, TimestampMs: 1723784115500},
				{Value: 85212.897341606, TimestampMs: 1723784133914},
				{Value: 85223.068417736, TimestampMs: 1723784145868},
				{Value: 85233.018931946, TimestampMs: 1723784157589},
				{Value: 85248.461401113, TimestampMs: 1723784175749},
				{Value: 85259.273556646, TimestampMs: 1723784188478},
				{Value: 85271.303120745, TimestampMs: 1723784202637},
				{Value: 85291.400852908, TimestampMs: 1723784226274},
				{Value: 85304.31033708, TimestampMs: 1723784241464},
				{Value: 85318.279367583, TimestampMs: 1723784257908},
				{Value: 85333.847398315, TimestampMs: 1723784276227},
				{Value: 85333.847398315, TimestampMs: 1723784276227},
				{Value: 85359.864088454, TimestampMs: 1723784306834},
				{Value: 85369.000247364, TimestampMs: 1723784317593},
				{Value: 85377.988741041, TimestampMs: 1723784328153},
				{Value: 85390.953853559, TimestampMs: 1723784343418},
				{Value: 85400.274651041, TimestampMs: 1723784354373},
				{Value: 85414.367124498, TimestampMs: 1723784370992},
				{Value: 85428.392179361, TimestampMs: 1723784387486},
				{Value: 85441.068542796, TimestampMs: 1723784402406},
				{Value: 85455.090792166, TimestampMs: 1723784418899},

				{Value: 85469.916951658, TimestampMs: 1723784436340},
				// 85481.057819604-85469.916951658 = 11.140867946
				{Value: 85481.057819604, TimestampMs: 1723784449448},
				// 85492.957819034-85481.057819604 = 11.89999943
				{Value: 85492.957819034, TimestampMs: 1723784463447},
				//  +34.5613010639936 @[1723784479695]  (85492.957819034-85469.916951658)/(1723784463447-1723784436340)*1000*60 = 50.9998171
				// 23.040867376 / 27107 * 1000 * 60

				{Value: 85507.169793341, TimestampMs: 1723784480165},
				{Value: 85523.205104841, TimestampMs: 1723784499037},
				{Value: 85534.871301536, TimestampMs: 1723784512760},
				{Value: 85550.176032782, TimestampMs: 1723784530770},
				////// 50.99050225194026 @[1723784539695]  (85550.176032782-85507.169793341)/(1723784530770-1723784480165)*1000*60 = 50.9905023
				//// 43.006239441 / 50605 * 1000 * 60
				//
				////
				{Value: 85567.054311861, TimestampMs: 1723784550635},
				{Value: 85578.065671268, TimestampMs: 1723784563577},
				{Value: 85593.712675185, TimestampMs: 1723784581999},
				{Value: 85608.3864412, TimestampMs: 1723784599280},
				//// 50.980116360164764 @[1723784599695]  (85608.3864412-85567.054311861)/(1723784599280-1723784550635)*1000*60 = 50.9801164
				//
				////
				{Value: 85621.216462262, TimestampMs: 1723784614371},
				{Value: 85631.684176047, TimestampMs: 1723784626702},
				{Value: 85644.681704276, TimestampMs: 1723784641979},
				{Value: 85660.264511262, TimestampMs: 1723784660320},
				{Value: 85670.48807541, TimestampMs: 1723784672345},
				{Value: 85681.373292269, TimestampMs: 1723784685162},
				{Value: 85696.401263333, TimestampMs: 1723784702839},
				{Value: 85706.099345088, TimestampMs: 1723784714247},
				{Value: 85716.135249545, TimestampMs: 1723784726058},
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
		"test": {
			q:     fmt.Sprintf(`rate(%s[1m])`, defaultMetric),
			start: time.UnixMilli(1723781659695),
			end:   time.UnixMilli(1723784726058),
			step:  time.Minute,
		},
		"now": {
			q:     fmt.Sprintf(`rate(%s[1m])`, defaultMetric),
			start: time.Unix(1723788909, 0),
			end:   time.Unix(1723792509, 0),
			step:  time.Minute,
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

func TestDemoGo(t *testing.T) {
	a := []*remote.Sample{
		//{Value: 89254.443482, TimestampMs: 1723788900000},
		//{Value: 89271.419844, TimestampMs: 1723788915000},
		//{Value: 89281.17737, TimestampMs: 1723788930000},
		//{Value: 89294.234335, TimestampMs: 1723788945000},
		//{Value: 89310.166, TimestampMs: 1723788960000},
		//{Value: 89321.718902, TimestampMs: 1723788975000},
		//{Value: 89330.943656, TimestampMs: 1723788990000},
		//{Value: 89341.437103, TimestampMs: 1723789005000},
		//{Value: 89356.714587, TimestampMs: 1723789020000},
		//{Value: 89368.57525, TimestampMs: 1723789035000},
		//{Value: 89377.891269, TimestampMs: 1723789050000},
		//{Value: 89389.237067, TimestampMs: 1723789065000},
		//{Value: 89403.894449, TimestampMs: 1723789080000},
		//{Value: 89418.157712, TimestampMs: 1723789095000},
		//{Value: 89429.995274, TimestampMs: 1723789110000},
		//{Value: 89449.490131, TimestampMs: 1723789125000},
		//{Value: 89463.821452, TimestampMs: 1723789140000},
		//{Value: 89463.821452, TimestampMs: 1723789155000},
		//{Value: 89477.877679, TimestampMs: 1723789170000},
		//{Value: 89491.614274, TimestampMs: 1723789185000},
		//{Value: 89514.946665, TimestampMs: 1723789200000},
		//{Value: 89525.187888, TimestampMs: 1723789215000},
		//{Value: 89536.199943, TimestampMs: 1723789230000},
		//{Value: 89546.285583, TimestampMs: 1723789245000},
		//{Value: 89561.879647, TimestampMs: 1723789260000},
		//{Value: 89576.112585, TimestampMs: 1723789275000},
		//{Value: 89587.296897, TimestampMs: 1723789290000},
		//{Value: 89602.584535, TimestampMs: 1723789305000},
		//{Value: 89616.72048, TimestampMs: 1723789320000},
		//{Value: 89629.581803, TimestampMs: 1723789335000},
		//{Value: 89629.581803, TimestampMs: 1723789350000},
		//{Value: 89644.597443, TimestampMs: 1723789365000},
		//{Value: 89661.223251, TimestampMs: 1723789380000},
		//{Value: 89681.051627, TimestampMs: 1723789395000},
		//{Value: 89691.466575, TimestampMs: 1723789410000},
		//{Value: 89691.466575, TimestampMs: 1723789425000},
		//{Value: 89708.374236, TimestampMs: 1723789440000},
		//{Value: 89719.580165, TimestampMs: 1723789455000},
		//{Value: 89734.549785, TimestampMs: 1723789470000},
		//{Value: 89751.197662, TimestampMs: 1723789485000},
		//{Value: 89764.596974, TimestampMs: 1723789500000},
		//{Value: 89775.94415, TimestampMs: 1723789515000},
		//{Value: 89791.728723, TimestampMs: 1723789530000},
		//{Value: 89805.817496, TimestampMs: 1723789545000},
		//{Value: 89817.497365, TimestampMs: 1723789560000},
		//{Value: 89832.928259, TimestampMs: 1723789575000},
		//{Value: 89844.88877, TimestampMs: 1723789590000},
		//{Value: 89853.642183, TimestampMs: 1723789605000},
		//{Value: 89869.9064, TimestampMs: 1723789620000},
		//{Value: 89881.06497, TimestampMs: 1723789635000},
		//{Value: 89897.480595, TimestampMs: 1723789650000},
		//{Value: 89897.480595, TimestampMs: 1723789665000},
		//{Value: 89920.95489, TimestampMs: 1723789680000},
		//{Value: 89933.523817, TimestampMs: 1723789695000},
		//{Value: 89943.552657, TimestampMs: 1723789710000},
		//{Value: 89954.967603, TimestampMs: 1723789725000},
		//{Value: 89967.46319, TimestampMs: 1723789740000},
		//{Value: 89978.93852, TimestampMs: 1723789755000},
		//{Value: 89990.527473, TimestampMs: 1723789770000},
		//{Value: 90003.530246, TimestampMs: 1723789785000},
		//{Value: 90016.504908, TimestampMs: 1723789800000},
		//{Value: 90026.261807, TimestampMs: 1723789815000},
		//{Value: 90040.375899, TimestampMs: 1723789830000},
		//{Value: 90051.124329, TimestampMs: 1723789845000},
		//{Value: 90072.787219, TimestampMs: 1723789860000},
		//{Value: 90083.422473, TimestampMs: 1723789875000},
		//{Value: 90096.186985, TimestampMs: 1723789890000},
		//{Value: 90107.191318, TimestampMs: 1723789905000},
		//{Value: 90122.454399, TimestampMs: 1723789920000},
		//{Value: 90131.422952, TimestampMs: 1723789935000},
		//{Value: 90148.22782, TimestampMs: 1723789950000},
		//{Value: 90159.659718, TimestampMs: 1723789965000},
		//{Value: 90171.257627, TimestampMs: 1723789980000},
		//{Value: 90186.055175, TimestampMs: 1723789995000},
		//{Value: 90194.81363, TimestampMs: 1723790010000},
		//{Value: 90204.743511, TimestampMs: 1723790025000},
		//{Value: 90219.01107, TimestampMs: 1723790040000},
		//{Value: 90229.438247, TimestampMs: 1723790055000},
		//{Value: 90244.301206, TimestampMs: 1723790070000},
		//{Value: 90258.517053, TimestampMs: 1723790085000},
		//{Value: 90274.690343, TimestampMs: 1723790100000},
		//{Value: 90290.162218, TimestampMs: 1723790115000},
		//{Value: 90299.116268, TimestampMs: 1723790130000},
		//{Value: 90309.50359, TimestampMs: 1723790145000},
		//{Value: 90323.006066, TimestampMs: 1723790160000},
		//{Value: 90342.314282, TimestampMs: 1723790175000},
		//{Value: 90354.520428, TimestampMs: 1723790190000},
		//{Value: 90365.376755, TimestampMs: 1723790205000},
		//{Value: 90376.388391, TimestampMs: 1723790220000},
		//{Value: 90389.190165, TimestampMs: 1723790235000},
		//{Value: 90402.389841, TimestampMs: 1723790250000},
		//{Value: 90418.680096, TimestampMs: 1723790265000},
		//{Value: 90432.830444, TimestampMs: 1723790280000},
		//{Value: 90432.830444, TimestampMs: 1723790295000},
		//{Value: 90448.193001, TimestampMs: 1723790310000},
		//{Value: 90463.955856, TimestampMs: 1723790325000},
		//{Value: 90473.328555, TimestampMs: 1723790340000},
		//{Value: 90485.445743, TimestampMs: 1723790355000},
		//{Value: 90500.308975, TimestampMs: 1723790370000},
		//{Value: 90513.996363, TimestampMs: 1723790385000},
		//{Value: 90524.583692, TimestampMs: 1723790400000},
		//{Value: 90539.642466, TimestampMs: 1723790415000},
		//{Value: 90554.297539, TimestampMs: 1723790430000},
		//{Value: 90568.418917, TimestampMs: 1723790445000},
		//{Value: 90578.465894, TimestampMs: 1723790460000},
		//{Value: 90590.308212, TimestampMs: 1723790475000},
		//{Value: 90610.786895, TimestampMs: 1723790490000},
		//{Value: 90619.853563, TimestampMs: 1723790505000},
		//{Value: 90633.344401, TimestampMs: 1723790520000},
		//{Value: 90643.642528, TimestampMs: 1723790535000},
		//{Value: 90654.855284, TimestampMs: 1723790550000},
		//{Value: 90671.192098, TimestampMs: 1723790565000},
		//{Value: 90685.384962, TimestampMs: 1723790580000},
		//{Value: 90695.301196, TimestampMs: 1723790595000},
		//{Value: 90706.41933, TimestampMs: 1723790610000},
		//{Value: 90717.570439, TimestampMs: 1723790625000},
		//{Value: 90730.782064, TimestampMs: 1723790640000},
		//{Value: 90740.953431, TimestampMs: 1723790655000},
		//{Value: 90757.409502, TimestampMs: 1723790670000},
		//{Value: 90769.1984, TimestampMs: 1723790685000},
		//{Value: 90783.309249, TimestampMs: 1723790700000},
		//{Value: 90797.414003, TimestampMs: 1723790715000},
		//{Value: 90807.548935, TimestampMs: 1723790730000},
		//{Value: 90820.106166, TimestampMs: 1723790745000},
		//{Value: 90835.907679, TimestampMs: 1723790760000},
		//{Value: 90846.165194, TimestampMs: 1723790775000},
		//{Value: 90863.011773, TimestampMs: 1723790790000},
		//{Value: 90874.307508, TimestampMs: 1723790805000},
		//{Value: 90889.763538, TimestampMs: 1723790820000},
		//{Value: 90900.335085, TimestampMs: 1723790835000},
		//{Value: 90908.988402, TimestampMs: 1723790850000},
		//{Value: 90920.469334, TimestampMs: 1723790865000},
		//{Value: 90933.826482, TimestampMs: 1723790880000},
		//{Value: 90947.39323, TimestampMs: 1723790895000},
		//{Value: 90963.091511, TimestampMs: 1723790910000},
		//{Value: 90977.368179, TimestampMs: 1723790925000},
		//{Value: 90986.163197, TimestampMs: 1723790940000},
		//{Value: 90995.061955, TimestampMs: 1723790955000},
		//{Value: 91006.194755, TimestampMs: 1723790970000},
		//{Value: 91029.735577, TimestampMs: 1723790985000},
		//{Value: 91029.735577, TimestampMs: 1723791000000},
		//{Value: 91044.818113, TimestampMs: 1723791015000},
		//{Value: 91059.875089, TimestampMs: 1723791030000},
		//{Value: 91080.865333, TimestampMs: 1723791045000},
		//{Value: 91090.649849, TimestampMs: 1723791060000},
		//{Value: 91104.369272, TimestampMs: 1723791075000},
		//{Value: 91117.172565, TimestampMs: 1723791090000},
		//{Value: 91131.69536, TimestampMs: 1723791105000},
		//{Value: 91131.69536, TimestampMs: 1723791120000},
		//{Value: 91148.506416, TimestampMs: 1723791135000},
		//{Value: 91170.250174, TimestampMs: 1723791150000},
		//{Value: 91180.622433, TimestampMs: 1723791165000},
		//{Value: 91193.951386, TimestampMs: 1723791180000},
		//{Value: 91203.445343, TimestampMs: 1723791195000},
		//{Value: 91216.939029, TimestampMs: 1723791210000},
		//{Value: 91225.686877, TimestampMs: 1723791225000},
		//{Value: 91241.017744, TimestampMs: 1723791240000},
		//{Value: 91250.610082, TimestampMs: 1723791255000},
		//{Value: 91266.805103, TimestampMs: 1723791270000},
		//{Value: 91280.751589, TimestampMs: 1723791285000},
		//{Value: 91293.822708, TimestampMs: 1723791300000},
		//{Value: 91303.847491, TimestampMs: 1723791315000},
		//{Value: 91323.330187, TimestampMs: 1723791330000},
		//{Value: 91333.262969, TimestampMs: 1723791345000},
		//{Value: 91344.740628, TimestampMs: 1723791360000},
		//{Value: 91354.903372, TimestampMs: 1723791375000},
		//{Value: 91367.906703, TimestampMs: 1723791390000},
		//{Value: 91378.553063, TimestampMs: 1723791405000},
		//{Value: 91394.636486, TimestampMs: 1723791420000},
		//{Value: 91410.204225, TimestampMs: 1723791435000},
		//{Value: 91422.493961, TimestampMs: 1723791450000},
		//{Value: 91433.819912, TimestampMs: 1723791465000},
		//{Value: 91443.898819, TimestampMs: 1723791480000},
		//{Value: 91459.981929, TimestampMs: 1723791495000},
		//{Value: 91470.091996, TimestampMs: 1723791510000},
		//{Value: 91486.074301, TimestampMs: 1723791525000},
		//{Value: 91500.000259, TimestampMs: 1723791540000},
		//{Value: 91500.000259, TimestampMs: 1723791555000},
		//{Value: 91525.132208, TimestampMs: 1723791570000},
		//{Value: 91538.572387, TimestampMs: 1723791585000},
		//{Value: 91552.01917, TimestampMs: 1723791600000},
		//{Value: 91563.870545, TimestampMs: 1723791615000},
		//{Value: 91574.424943, TimestampMs: 1723791630000},
		//{Value: 91589.326142, TimestampMs: 1723791645000},
		//{Value: 91599.479193, TimestampMs: 1723791660000},
		//{Value: 91616.369025, TimestampMs: 1723791675000},
		//{Value: 91616.369025, TimestampMs: 1723791690000},
		//{Value: 91641.707954, TimestampMs: 1723791705000},
		//{Value: 91650.981637, TimestampMs: 1723791720000},
		//{Value: 91663.434798, TimestampMs: 1723791735000},
		//{Value: 91679.998225, TimestampMs: 1723791750000},
		//{Value: 91679.998225, TimestampMs: 1723791765000},
		//{Value: 91703.713244, TimestampMs: 1723791780000},
		//{Value: 91715.546001, TimestampMs: 1723791795000},
		//{Value: 91715.546001, TimestampMs: 1723791810000},
		//{Value: 91732.134697, TimestampMs: 1723791825000},
		//{Value: 91747.85583, TimestampMs: 1723791840000},
		//{Value: 91759.740987, TimestampMs: 1723791855000},
		//{Value: 91771.015463, TimestampMs: 1723791870000},
		//{Value: 91784.90931, TimestampMs: 1723791885000},
		//{Value: 91800.034562, TimestampMs: 1723791900000},
		//{Value: 91813.36678, TimestampMs: 1723791915000},
		//{Value: 91826.134639, TimestampMs: 1723791930000},
		//{Value: 91840.4055, TimestampMs: 1723791945000},
		//{Value: 91850.050692, TimestampMs: 1723791960000},
		//{Value: 91864.529685, TimestampMs: 1723791975000},
		//{Value: 91879.74966, TimestampMs: 1723791990000},
		//{Value: 91896.755603, TimestampMs: 1723792005000},
		//{Value: 91896.755603, TimestampMs: 1723792020000},
		//{Value: 91912.51985, TimestampMs: 1723792035000},
		//{Value: 91933.378418, TimestampMs: 1723792050000},
		//{Value: 91947.25425, TimestampMs: 1723792065000},
		//{Value: 91960.974708, TimestampMs: 1723792080000},
		//{Value: 91970.51603, TimestampMs: 1723792095000},
		//{Value: 91980.078045, TimestampMs: 1723792110000},
		//{Value: 91995.510175, TimestampMs: 1723792125000},
		//{Value: 92008.588068, TimestampMs: 1723792140000},
		//{Value: 92022.377881, TimestampMs: 1723792155000},
		//{Value: 92032.231931, TimestampMs: 1723792170000},
		//{Value: 92045.974103, TimestampMs: 1723792185000},
		//{Value: 92057.361389, TimestampMs: 1723792200000},
		//{Value: 92073.260322, TimestampMs: 1723792215000},
		//{Value: 92082.037532, TimestampMs: 1723792230000},
		//{Value: 92095.530672, TimestampMs: 1723792245000},
		//{Value: 92107.013675, TimestampMs: 1723792260000},
		//{Value: 92119.510858, TimestampMs: 1723792275000},
		//{Value: 92132.906524, TimestampMs: 1723792290000},
		//{Value: 92145.149405, TimestampMs: 1723792305000},
		//{Value: 92160.632786, TimestampMs: 1723792320000},
		//{Value: 92171.862029, TimestampMs: 1723792335000},
		//{Value: 92186.777817, TimestampMs: 1723792350000},
		//{Value: 92199.992426, TimestampMs: 1723792365000},
		//{Value: 92215.714791, TimestampMs: 1723792380000},
		//{Value: 92215.714791, TimestampMs: 1723792395000},
		//{Value: 92231.82981, TimestampMs: 1723792410000},
		//{Value: 92244.71939, TimestampMs: 1723792425000},
		//{Value: 92258.157934, TimestampMs: 1723792440000},
		//{Value: 92276.828395, TimestampMs: 1723792455000},
		//{Value: 92288.292571, TimestampMs: 1723792470000},
		//{Value: 92288.292571, TimestampMs: 1723792485000},

		// 2024-08-16 12:14:19
		{Value: 83110.230240566, TimestampMs: 1723781659695},
		// 2024-08-16 12:14:31
		{Value: 83119.89771368, TimestampMs: 1723781671058},
		// 14
		{Value: 83133.668603386, TimestampMs: 1723781687260},
		//
		{Value: 83147.653484962, TimestampMs: 1723781703717},
		{Value: 83157.071587397, TimestampMs: 1723781714799},
		{Value: 83171.669781249, TimestampMs: 1723781731964},
		{Value: 83181.811100567, TimestampMs: 1723781743907},
		{Value: 83202.702299213, TimestampMs: 1723781768507},
		{Value: 83218.092858449, TimestampMs: 1723781786611},
		{Value: 83229.663421875, TimestampMs: 1723781800221},
		{Value: 83239.297822156, TimestampMs: 1723781811557},
		{Value: 83248.615420503, TimestampMs: 1723781822522},
		{Value: 83259.889640533, TimestampMs: 1723781835794},
		{Value: 83275.307092186, TimestampMs: 1723781853931},
		{Value: 83288.462744969, TimestampMs: 1723781869409},
		{Value: 83305.330399466, TimestampMs: 1723781889250},
		{Value: 83322.062551678, TimestampMs: 1723781908944},
		{Value: 83322.062551678, TimestampMs: 1723781908944},
		{Value: 83348.089331753, TimestampMs: 1723781939566},
		{Value: 83360.367689322, TimestampMs: 1723781954012},
		{Value: 83371.169228288, TimestampMs: 1723781966718},
		{Value: 83381.452888248, TimestampMs: 1723781978819},
		{Value: 83391.218637505, TimestampMs: 1723781990309},
		{Value: 83413.079859323, TimestampMs: 1723782016025},
		{Value: 83422.446287501, TimestampMs: 1723782027045},
		{Value: 83438.201603583, TimestampMs: 1723782045595},
		{Value: 83449.84403652, TimestampMs: 1723782059280},
		{Value: 83461.737374577, TimestampMs: 1723782073268},
		{Value: 83471.474445544, TimestampMs: 1723782084742},
		{Value: 83481.967704588, TimestampMs: 1723782097091},
		{Value: 83502.994504377, TimestampMs: 1723782121831},
		{Value: 83517.097210706, TimestampMs: 1723782138417},
		{Value: 83517.097210706, TimestampMs: 1723782138417},
		{Value: 83533.634034338, TimestampMs: 1723782157868},
		{Value: 83557.936638677, TimestampMs: 1723782186471},
		{Value: 83567.707069277, TimestampMs: 1723782197968},
		{Value: 83579.86422778, TimestampMs: 1723782212285},
		{Value: 83590.57310578, TimestampMs: 1723782224886},
		{Value: 83599.637284459, TimestampMs: 1723782235547},
		{Value: 83611.89931635, TimestampMs: 1723782249968},
		{Value: 83636.249048845, TimestampMs: 1723782278627},
		{Value: 83636.249048845, TimestampMs: 1723782278627},
		{Value: 83651.360371096, TimestampMs: 1723782296419},
		{Value: 83667.688770921, TimestampMs: 1723782315626},
		{Value: 83682.595590758, TimestampMs: 1723782333163},
		{Value: 83697.930974282, TimestampMs: 1723782351207},
		{Value: 83713.996785709, TimestampMs: 1723782370108},
		{Value: 83726.730892809, TimestampMs: 1723782385088},
		{Value: 83740.495341398, TimestampMs: 1723782401294},
		{Value: 83752.122844543, TimestampMs: 1723782414970},
		{Value: 83764.467953563, TimestampMs: 1723782429501},
		{Value: 83774.025976015, TimestampMs: 1723782440750},
		{Value: 83784.08183661, TimestampMs: 1723782452579},
		{Value: 83798.448884967, TimestampMs: 1723782469483},
		{Value: 83809.829149871, TimestampMs: 1723782482873},
		{Value: 83824.122434913, TimestampMs: 1723782499699},
		{Value: 83839.23376819, TimestampMs: 1723782517473},
		{Value: 83851.435562208, TimestampMs: 1723782531827},
		{Value: 83865.753165636, TimestampMs: 1723782548674},
		{Value: 83881.084374015, TimestampMs: 1723782566911},
		{Value: 83894.489397571, TimestampMs: 1723782582674},
		{Value: 83894.489397571, TimestampMs: 1723782582674},
		{Value: 83920.336955168, TimestampMs: 1723782613091},
		{Value: 83935.777101942, TimestampMs: 1723782631247},
		{Value: 83944.958825087, TimestampMs: 1723782642049},
		{Value: 83954.930618518, TimestampMs: 1723782653796},
		{Value: 83964.61676056, TimestampMs: 1723782665176},
		{Value: 83976.311339758, TimestampMs: 1723782678940},
		{Value: 83992.240992968, TimestampMs: 1723782697684},
		{Value: 84005.192565515, TimestampMs: 1723782712920},
		{Value: 84018.50751575, TimestampMs: 1723782728590},
		{Value: 84031.419153556, TimestampMs: 1723782743789},
		{Value: 84044.052355138, TimestampMs: 1723782758649},
		{Value: 84066.50921023, TimestampMs: 1723782785064},
		{Value: 84076.887606491, TimestampMs: 1723782797286},
		{Value: 84088.660599813, TimestampMs: 1723782811139},
		{Value: 84100.347848796, TimestampMs: 1723782824897},
		{Value: 84118.886448797, TimestampMs: 1723782846704},
		{Value: 84118.886448797, TimestampMs: 1723782846704},
		{Value: 84145.303297995, TimestampMs: 1723782877795},
		{Value: 84157.022505363, TimestampMs: 1723782891569},
		{Value: 84171.570885322, TimestampMs: 1723782908697},
		{Value: 84171.570885322, TimestampMs: 1723782908697},
		{Value: 84185.029034896, TimestampMs: 1723782924536},
		{Value: 84200.795632308, TimestampMs: 1723782943074},
		{Value: 84217.338205034, TimestampMs: 1723782962543},
		{Value: 84232.566871791, TimestampMs: 1723782980478},
		{Value: 84247.952157671, TimestampMs: 1723782998589},
		{Value: 84259.687514364, TimestampMs: 1723783012395},
		{Value: 84270.870575426, TimestampMs: 1723783025544},
		{Value: 84283.327474379, TimestampMs: 1723783040211},
		{Value: 84292.834037796, TimestampMs: 1723783051384},
		{Value: 84307.01297336, TimestampMs: 1723783068060},
		{Value: 84321.282299361, TimestampMs: 1723783084865},
		{Value: 84335.160947557, TimestampMs: 1723783101198},
		{Value: 84349.886931705, TimestampMs: 1723783118519},
		{Value: 84365.812366229, TimestampMs: 1723783137248},
		{Value: 84379.187285421, TimestampMs: 1723783152998},
		{Value: 84379.187285421, TimestampMs: 1723783152998},
		{Value: 84395.868999243, TimestampMs: 1723783172620},
		{Value: 84408.729856768, TimestampMs: 1723783187749},
		{Value: 84423.244350773, TimestampMs: 1723783204825},
		{Value: 84436.261392845, TimestampMs: 1723783220144},
		{Value: 84452.379284541, TimestampMs: 1723783239108},
		{Value: 84464.15768923, TimestampMs: 1723783252962},
		{Value: 84473.640088229, TimestampMs: 1723783264123},
		{Value: 84496.70578817, TimestampMs: 1723783291251},
		{Value: 84505.274005051, TimestampMs: 1723783301336},
		{Value: 84513.825175274, TimestampMs: 1723783311406},
		{Value: 84535.988590779, TimestampMs: 1723783337476},
		{Value: 84549.278094191, TimestampMs: 1723783353113},
		{Value: 84559.065807903, TimestampMs: 1723783364626},
		{Value: 84575.328212243, TimestampMs: 1723783383755},
		{Value: 84585.130274946, TimestampMs: 1723783395304},
		{Value: 84597.818248467, TimestampMs: 1723783410232},
		{Value: 84608.916253026, TimestampMs: 1723783423291},
		{Value: 84623.16625411, TimestampMs: 1723783440053},
		{Value: 84631.944909125, TimestampMs: 1723783450378},
		{Value: 84647.499217182, TimestampMs: 1723783468687},
		{Value: 84660.506692489, TimestampMs: 1723783484006},
		{Value: 84674.60893123, TimestampMs: 1723783500583},
		{Value: 84684.505685069, TimestampMs: 1723783512235},
		{Value: 84705.219658059, TimestampMs: 1723783536607},
		{Value: 84714.060915456, TimestampMs: 1723783547009},
		{Value: 84728.836124879, TimestampMs: 1723783564397},
		{Value: 84744.277426466, TimestampMs: 1723783582557},
		{Value: 84754.042469269, TimestampMs: 1723783594040},
		{Value: 84763.213702676, TimestampMs: 1723783604841},
		{Value: 84777.274878899, TimestampMs: 1723783621373},
		{Value: 84798.384659124, TimestampMs: 1723783646224},
		{Value: 84811.581806096, TimestampMs: 1723783661741},
		{Value: 84811.581806096, TimestampMs: 1723783661741},
		{Value: 84837.063572746, TimestampMs: 1723783691729},
		{Value: 84837.063572746, TimestampMs: 1723783691729},
		{Value: 84862.534809174, TimestampMs: 1723783721702},
		{Value: 84862.534809174, TimestampMs: 1723783721702},
		{Value: 84889.552986904, TimestampMs: 1723783753474},
		{Value: 84889.552986904, TimestampMs: 1723783753474},
		{Value: 84904.59475183, TimestampMs: 1723783771171},
		{Value: 84919.763932416, TimestampMs: 1723783789025},
		{Value: 84930.610916691, TimestampMs: 1723783801792},
		{Value: 84942.863905387, TimestampMs: 1723783816203},
		{Value: 84966.699414609, TimestampMs: 1723783844241},
		{Value: 84977.086454212, TimestampMs: 1723783856458},
		{Value: 84986.667846844, TimestampMs: 1723783867734},
		{Value: 85002.292679538, TimestampMs: 1723783886117},
		{Value: 85011.128037669, TimestampMs: 1723783896512},
		{Value: 85021.452751276, TimestampMs: 1723783908656},
		{Value: 85046.656737888, TimestampMs: 1723783938316},
		{Value: 85057.149303109, TimestampMs: 1723783950657},
		{Value: 85072.792663737, TimestampMs: 1723783969061},
		{Value: 85081.322519298, TimestampMs: 1723783979106},
		{Value: 85098.016534644, TimestampMs: 1723783998744},
		{Value: 85108.933812104, TimestampMs: 1723784011582},
		{Value: 85124.353810677, TimestampMs: 1723784029730},
		{Value: 85138.578992055, TimestampMs: 1723784046459},
		{Value: 85149.470757779, TimestampMs: 1723784059272},
		{Value: 85159.861953756, TimestampMs: 1723784071513},
		{Value: 85174.736633926, TimestampMs: 1723784089017},
		{Value: 85184.332789852, TimestampMs: 1723784100309},
		{Value: 85197.244814493, TimestampMs: 1723784115500},
		{Value: 85212.897341606, TimestampMs: 1723784133914},
		{Value: 85223.068417736, TimestampMs: 1723784145868},
		{Value: 85233.018931946, TimestampMs: 1723784157589},
		{Value: 85248.461401113, TimestampMs: 1723784175749},
		{Value: 85259.273556646, TimestampMs: 1723784188478},
		{Value: 85271.303120745, TimestampMs: 1723784202637},
		{Value: 85291.400852908, TimestampMs: 1723784226274},
		{Value: 85304.31033708, TimestampMs: 1723784241464},
		{Value: 85318.279367583, TimestampMs: 1723784257908},
		{Value: 85333.847398315, TimestampMs: 1723784276227},
		{Value: 85333.847398315, TimestampMs: 1723784276227},
		{Value: 85359.864088454, TimestampMs: 1723784306834},
		{Value: 85369.000247364, TimestampMs: 1723784317593},
		{Value: 85377.988741041, TimestampMs: 1723784328153},
		{Value: 85390.953853559, TimestampMs: 1723784343418},
		{Value: 85400.274651041, TimestampMs: 1723784354373},
		{Value: 85414.367124498, TimestampMs: 1723784370992},
		{Value: 85428.392179361, TimestampMs: 1723784387486},
		{Value: 85441.068542796, TimestampMs: 1723784402406},
		{Value: 85455.090792166, TimestampMs: 1723784418899},

		{Value: 85469.916951658, TimestampMs: 1723784436340},
		// 85481.057819604-85469.916951658 = 11.140867946
		{Value: 85481.057819604, TimestampMs: 1723784449448},
		// 85492.957819034-85481.057819604 = 11.89999943
		{Value: 85492.957819034, TimestampMs: 1723784463447},
		//  +34.5613010639936 @[1723784479695]  (85492.957819034-85469.916951658)/(1723784463447-1723784436340)*1000*60 = 50.9998171
		// 23.040867376 / 27107 * 1000 * 60

		{Value: 85507.169793341, TimestampMs: 1723784480165},
		{Value: 85523.205104841, TimestampMs: 1723784499037},
		{Value: 85534.871301536, TimestampMs: 1723784512760},
		{Value: 85550.176032782, TimestampMs: 1723784530770},
		////// 50.99050225194026 @[1723784539695]  (85550.176032782-85507.169793341)/(1723784530770-1723784480165)*1000*60 = 50.9905023
		//// 43.006239441 / 50605 * 1000 * 60
		//
		////
		{Value: 85567.054311861, TimestampMs: 1723784550635},
		{Value: 85578.065671268, TimestampMs: 1723784563577},
		{Value: 85593.712675185, TimestampMs: 1723784581999},
		{Value: 85608.3864412, TimestampMs: 1723784599280},
		//// 50.980116360164764 @[1723784599695]  (85608.3864412-85567.054311861)/(1723784599280-1723784550635)*1000*60 = 50.9801164
		//
		////
		{Value: 85621.216462262, TimestampMs: 1723784614371},
		{Value: 85631.684176047, TimestampMs: 1723784626702},
		{Value: 85644.681704276, TimestampMs: 1723784641979},
		{Value: 85660.264511262, TimestampMs: 1723784660320},
		{Value: 85670.48807541, TimestampMs: 1723784672345},
		{Value: 85681.373292269, TimestampMs: 1723784685162},
		{Value: 85696.401263333, TimestampMs: 1723784702839},
		{Value: 85706.099345088, TimestampMs: 1723784714247},
		{Value: 85716.135249545, TimestampMs: 1723784726058},
	}

	for i, v := range a {
		if i > 0 {
			last := a[i-1]
			if last.TimestampMs != v.TimestampMs {
				fmt.Printf("%d, value: %f / time: %d / increase 1m: %f\n", v.TimestampMs, v.Value-last.Value, v.TimestampMs-last.TimestampMs, (float64(v.Value-last.Value) * 1000 / float64(v.TimestampMs-last.TimestampMs)))
			}
		}
	}
}
