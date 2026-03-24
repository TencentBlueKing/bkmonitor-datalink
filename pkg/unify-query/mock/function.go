// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mock

import (
	"sort"
	"strings"

	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/tsdb/chunkenc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

type TimeSeriesList []prompb.TimeSeries

func SeriesSetToTimeSeries(ss storage.SeriesSet) (timeSeries TimeSeriesList, err error) {
	for ss.Next() {
		series := ss.At()
		lbs := series.Labels()
		newLbs := make([]prompb.Label, 0, len(lbs))
		for _, lb := range lbs {
			newLbs = append(newLbs, prompb.Label{
				Name:  lb.Name,
				Value: lb.Value,
			})
		}

		var newSamples []prompb.Sample
		it := series.Iterator(nil)
		for it.Next() == chunkenc.ValFloat {
			ts, val := it.At()

			newSamples = append(newSamples, prompb.Sample{
				Value:     val,
				Timestamp: ts,
			})
		}
		if it.Err() != nil {
			err = it.Err()
			return timeSeries, err
		}

		timeSeries = append(timeSeries, prompb.TimeSeries{Labels: newLbs, Samples: newSamples})
	}

	if ws := ss.Warnings(); len(ws) > 0 {
		var errorString strings.Builder
		for _, w := range ws {
			errorString.WriteString(w.Error())
		}
		err = errors.New(errorString.String())
		return timeSeries, err
	}

	if ss.Err() != nil {
		err = ss.Err()
		return timeSeries, err
	}

	sort.SliceStable(timeSeries, func(i, j int) bool {
		return timeSeries[i].String() < timeSeries[j].String()
	})

	return timeSeries, err
}

func (t *TimeSeriesList) String() string {
	s, _ := json.Marshal(t)
	return string(s)
}
