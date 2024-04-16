// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/prometheus/prometheus/prompb"
)

const (
	Timestamp  = "dtEventTimeStamp"
	TimeFormat = "epoch_millis"
)

type TimeSeriesResult struct {
	TimeSeriesMap map[string]*prompb.TimeSeries
}

type SortObject struct {
	Index string
	Start int64
}

type Result struct {
	Matrix []*Vector
}

type Vector struct {
	Data     map[string]interface{}
	ValueKey string
}

func (v *Vector) Sample(prefix string) (*prompb.Sample, error) {
	var (
		data map[string]interface{}
		ok   bool
		err  error
	)

	if prefix != "" {
		if data, ok = v.Data[prefix].(map[string]interface{}); !ok {
			return nil, fmt.Errorf("data format error prefix %s in %+v", prefix, v.Data)
		}
	} else {
		data = v.Data
	}

	sample := &prompb.Sample{}
	value := data[v.ValueKey]
	if value != nil {
		switch value.(type) {
		case float64:
			sample.Value = value.(float64)
		case int64:
			sample.Value = float64(value.(int64))
		case int:
			sample.Value = float64(value.(int))
		case string:
			sample.Value, err = strconv.ParseFloat(value.(string), 64)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("value key %s %s type is error: %T, %v", prefix, v.ValueKey, value, value)
		}
	} else {
		sample.Value = 0
	}

	timestamp := v.Data[Timestamp]
	switch timestamp.(type) {
	case int64:
		sample.Timestamp = timestamp.(int64) * 1e3
	case int:
		sample.Timestamp = int64(timestamp.(int) * 1e3)
	case string:
		sample.Timestamp, err = strconv.ParseInt(timestamp.(string), 10, 64)
	default:
		return nil, fmt.Errorf("timestamp key type is error: %T, %v", timestamp, timestamp)
	}

	return sample, nil
}

func (v *Vector) Labels(prefix string) (lbs *prompb.Labels, err error) {
	var (
		data map[string]interface{}
		ok   bool
	)

	if prefix != "" {
		if data, ok = v.Data[prefix].(map[string]interface{}); !ok {
			return nil, fmt.Errorf("data format error prefix %s in %+v", prefix, v.Data)
		}
	} else {
		data = v.Data
	}

	lbl := make([]string, 0)
	for k := range data {
		lbl = append(lbl, k)
	}

	sort.Strings(lbl)

	lbs = &prompb.Labels{
		Labels: make([]prompb.Label, 0, len(lbl)),
	}

	for _, k := range lbl {
		var value string
		d := data[k]
		switch d.(type) {
		case string:
			value = fmt.Sprintf("%s", d)
		case float64, float32:
			value = fmt.Sprintf("%.f", d)
		case int64, int32, int:
			value = fmt.Sprintf("%d", d)
		default:
			err = fmt.Errorf("dimensions key type is error: %T, %v", d, d)
			return
		}

		lbs.Labels = append(lbs.Labels, prompb.Label{
			Name:  k,
			Value: value,
		})
	}

	return
}
