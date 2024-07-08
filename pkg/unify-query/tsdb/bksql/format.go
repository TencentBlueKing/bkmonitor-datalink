// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	dtEventTimeStamp = "dtEventTimeStamp"
	dtEventTime      = "dtEventTime"
	localTime        = "localTime"
	startTime        = "_startTime_"
	endTime          = "_endTime_"
	theDate          = "thedate"

	timeStamp = "_timestamp_"
	value     = "_value_"

	FieldValue = "_value"
	FieldTime  = "_time"
)

var (
	internalDimension = map[string]struct{}{
		value:            {},
		timeStamp:        {},
		dtEventTimeStamp: {},
		dtEventTime:      {},
		localTime:        {},
		startTime:        {},
		endTime:          {},
		theDate:          {},
	}
)

type QueryFactory struct {
	ctx context.Context

	query *metadata.Query

	start time.Time
	end   time.Time
	step  time.Duration

	selects []string
	groups  []string

	sql strings.Builder
}

func NewQueryFactory(ctx context.Context, query *metadata.Query) *QueryFactory {
	f := &QueryFactory{
		ctx:     ctx,
		query:   query,
		selects: make([]string, 0),
		groups:  make([]string, 0),
	}
	return f
}

func (f *QueryFactory) write(s string) {
	f.sql.WriteString(s + " ")
}

func (f *QueryFactory) WithRangeTime(start, end time.Time, step time.Duration) *QueryFactory {
	f.start = start
	f.end = end
	f.step = step
	return f
}

func (f *QueryFactory) ParserQuery() (err error) {
	if len(f.query.Aggregates) > 0 {
		for _, agg := range f.query.Aggregates {
			if agg.Window > 0 {
				timeField := fmt.Sprintf("(`%s`- (`%s` %% %d))", dtEventTimeStamp, dtEventTimeStamp, agg.Window.Milliseconds())
				f.groups = append(f.groups, timeField)
				f.selects = append(f.selects, fmt.Sprintf("MAX(%s) AS `%s`", timeField, timeStamp))
			}

			f.selects = append(f.selects, fmt.Sprintf("%s(`%s`) AS `%s`", agg.Name, f.query.Field, value))
			for _, dim := range agg.Dimensions {
				dim = fmt.Sprintf("`%s`", dim)
				f.groups = append(f.groups, dim)
				f.selects = append(f.selects, dim)
			}
		}
	}

	if len(f.selects) == 0 {
		f.selects = append(f.selects, "*")
	}

	return
}

func (f *QueryFactory) SQL() string {
	f.sql.Reset()

	f.write("SELECT")
	f.write(strings.Join(f.selects, ", "))
	f.write("FROM")
	f.write(f.query.DB)
	f.write("WHERE")
	f.write(fmt.Sprintf("%s >= %d AND %s < %d", dtEventTimeStamp, f.start.UnixMilli(), dtEventTimeStamp, f.end.UnixMilli()))
	if f.query.BkSqlCondition != "" {
		f.write("AND")
		f.write(f.query.BkSqlCondition)
	}
	if len(f.groups) > 0 {
		f.write("GROUP BY")
		f.write(strings.Join(f.groups, ", "))
	}
	if f.query.From > 0 {
		f.write("OFFSET")
		f.write(fmt.Sprintf("%d", f.query.From))
	}
	if f.query.Size > 0 {
		f.write("LIMIT")
		f.write(fmt.Sprintf("%d", f.query.Size))
	}
	orders := make([]string, 0)
	for key, asc := range f.query.Orders {
		var orderField string
		switch key {
		case FieldValue:
			orderField = f.query.Field
		case FieldTime:
			orderField = timeStamp
		default:
			orderField = key
		}
		ascName := "ASC"
		if !asc {
			ascName = "DESC"
		}
		orders = append(orders, fmt.Sprintf("`%s` %s", orderField, ascName))
	}
	if len(orders) > 0 {
		f.write("ORDER BY")
		f.write(strings.Join(orders, ", "))
	}

	return strings.Trim(f.sql.String(), " ")
}

func (f *QueryFactory) dims(dims []string, field string) []string {
	dimensions := make([]string, 0)
	for _, dim := range dims {
		// 判断是否是内置维度，内置维度不是用户上报的维度
		if _, ok := internalDimension[dim]; ok {
			continue
		}
		// 如果是字段值也需要跳过
		if dim == field {
			continue
		}

		dimensions = append(dimensions, dim)
	}
	return dimensions
}

func (f *QueryFactory) FormatData(keys []string, list []map[string]interface{}) (*prompb.QueryResult, error) {
	res := &prompb.QueryResult{}

	if len(list) == 0 {
		return res, nil
	}
	// 维度结构体为空则任务异常
	if len(keys) == 0 {
		return res, fmt.Errorf("SelectFieldsOrder is empty")
	}

	// 获取该指标的维度 key
	field := f.query.Field
	dimensions := f.dims(keys, field)

	tsMap := make(map[string]*prompb.TimeSeries, 0)
	for _, d := range list {
		// 优先获取时间和值
		var (
			vt int64
			vv float64

			vtLong   interface{}
			vvDouble interface{}

			ok bool
		)

		if d == nil {
			continue
		}

		// 获取时间戳，单位是毫秒
		if vtLong, ok = d[timeStamp]; !ok {
			vtLong = 0
		}

		if vtLong == nil {
			continue
		}
		switch vtLong.(type) {
		case int64:
			vt = vtLong.(int64)
		case float64:
			vt = int64(vtLong.(float64))
		default:
			return res, fmt.Errorf("%s type is error %T, %v", dtEventTimeStamp, vtLong, vtLong)
		}

		// 获取值
		if vvDouble, ok = d[field]; !ok {
			return res, fmt.Errorf("dimension %s is emtpy", field)
		}

		if vvDouble == nil {
			continue
		}
		switch vvDouble.(type) {
		case int64:
			vv = float64(vvDouble.(int64))
		case float64:
			vv = vvDouble.(float64)
		default:
			return res, fmt.Errorf("%s type is error %T, %v", field, vvDouble, vvDouble)
		}

		var buf strings.Builder
		lbl := make([]prompb.Label, 0, len(dimensions))
		// 获取维度信息
		for _, dimName := range dimensions {
			val, err := getValue(dimName, d)
			if err != nil {
				return res, fmt.Errorf("dimensions %+v %s", dimensions, err.Error())
			}

			buf.WriteString(fmt.Sprintf("%s:%s,", dimName, val))
			lbl = append(lbl, prompb.Label{
				Name:  dimName,
				Value: val,
			})
		}

		// 同一个 series 进行合并分组
		key := buf.String()
		if _, ok := tsMap[key]; !ok {
			tsMap[key] = &prompb.TimeSeries{
				Labels:  lbl,
				Samples: make([]prompb.Sample, 0),
			}
		}

		tsMap[key].Samples = append(tsMap[key].Samples, prompb.Sample{
			Value:     vv,
			Timestamp: vt,
		})
	}

	// 转换结构体
	res.Timeseries = make([]*prompb.TimeSeries, 0, len(tsMap))
	for _, ts := range tsMap {
		res.Timeseries = append(res.Timeseries, ts)
	}

	return res, nil
}
