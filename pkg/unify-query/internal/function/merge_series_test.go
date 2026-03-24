// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package function_test

import (
	"testing"

	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/storage/remote"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestMergeSeriesSet(t *testing.T) {
	ts1 := &prompb.TimeSeries{
		Labels: []prompb.Label{
			{
				Name:  "__name__",
				Value: "up",
			},
			{
				Name:  "job",
				Value: "prometheus",
			},
		},
		Samples: []prompb.Sample{
			{
				Value:     1,
				Timestamp: 0,
			},
			{
				Value:     3,
				Timestamp: 120,
			},
		},
	}
	ts3 := &prompb.TimeSeries{
		Labels: []prompb.Label{
			{
				Name:  "__name__",
				Value: "up",
			},
			{
				Name:  "job",
				Value: "prometheus",
			},
		},
		Samples: []prompb.Sample{
			{
				Value:     4,
				Timestamp: 0,
			},
			{
				Value:     5,
				Timestamp: 60,
			},
		},
	}

	ts2 := &prompb.TimeSeries{
		Labels: []prompb.Label{
			{
				Name:  "__name__",
				Value: "up",
			},
			{
				Name:  "job",
				Value: "elasticsearch",
			},
		},
		Samples: []prompb.Sample{
			{
				Value:     2,
				Timestamp: 60,
			},
			{
				Value:     3,
				Timestamp: 120,
			},
		},
	}
	ts4 := &prompb.TimeSeries{
		Labels: []prompb.Label{
			{
				Name:  "__name__",
				Value: "up",
			},
			{
				Name:  "job",
				Value: "elasticsearch",
			},
		},
		Samples: []prompb.Sample{
			{
				Value:     8,
				Timestamp: 60,
			},
			{
				Value:     9,
				Timestamp: 120,
			},
		},
	}

	testCases := map[string]struct {
		qrs []*prompb.QueryResult
		ts  mock.TimeSeriesList
		fn  storage.VerticalSeriesMergeFunc
	}{
		"empty": {
			qrs: []*prompb.QueryResult{},
		},
		"one set": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1,
					},
				},
			},
			ts: mock.TimeSeriesList{
				*ts1,
			},
		},
		"two timeSeries with chainedSeriesMerge": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2, ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				*ts2, *ts4, *ts1, *ts3,
			},
		},
		"two timeSeries with mergeSeriesSetWithFuncAndSort": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2, ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				*ts2, *ts4, *ts1, *ts3,
			},
			fn: function.NewMergeSeriesSetWithFuncAndSort(""),
		},
		"two queryResult with chainedSeriesMerge": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2,
					},
				},
				{
					Timeseries: []*prompb.TimeSeries{
						ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "elasticsearch",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     8,
							Timestamp: 60,
						},
						{
							Value:     9,
							Timestamp: 120,
						},
					},
				},
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "prometheus",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     4,
							Timestamp: 0,
						},
						{
							Value:     5,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
			},
		},
		"two queryResult with mergeSeriesSetWithFuncAndSort": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2,
					},
				},
				{
					Timeseries: []*prompb.TimeSeries{
						ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "elasticsearch",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     10,
							Timestamp: 60,
						},
						{
							Value:     12,
							Timestamp: 120,
						},
					},
				},
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "prometheus",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     5,
							Timestamp: 0,
						},
						{
							Value:     5,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
			},
			fn: function.NewMergeSeriesSetWithFuncAndSort(""),
		},
		"two queryResult with mergeSeriesSetWithFuncAndSort max": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2,
					},
				},
				{
					Timeseries: []*prompb.TimeSeries{
						ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "elasticsearch",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     8,
							Timestamp: 60,
						},
						{
							Value:     9,
							Timestamp: 120,
						},
					},
				},
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "prometheus",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     4,
							Timestamp: 0,
						},
						{
							Value:     5,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
			},
			fn: function.NewMergeSeriesSetWithFuncAndSort("max"),
		},
		"two queryResult with mergeSeriesSetWithFuncAndSort min": {
			qrs: []*prompb.QueryResult{
				{
					Timeseries: []*prompb.TimeSeries{
						ts1, ts2,
					},
				},
				{
					Timeseries: []*prompb.TimeSeries{
						ts3, ts4,
					},
				},
			},
			ts: mock.TimeSeriesList{
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "elasticsearch",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     2,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
				{
					Labels: []prompb.Label{
						{
							Name:  "__name__",
							Value: "up",
						},
						{
							Name:  "job",
							Value: "prometheus",
						},
					},
					Samples: []prompb.Sample{
						{
							Value:     1,
							Timestamp: 0,
						},
						{
							Value:     5,
							Timestamp: 60,
						},
						{
							Value:     3,
							Timestamp: 120,
						},
					},
				},
			},
			fn: function.NewMergeSeriesSetWithFuncAndSort("min"),
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			var sets []storage.SeriesSet
			for _, r := range tc.qrs {
				sets = append(sets, remote.FromQueryResult(true, r))
			}

			if tc.fn == nil {
				tc.fn = storage.ChainedSeriesMerge
			}
			set := storage.NewMergeSeriesSet(sets, tc.fn)

			ts, err := mock.SeriesSetToTimeSeries(set)
			assert.Nil(t, err)
			assert.Equal(t, tc.ts, ts)
		})
	}
}
