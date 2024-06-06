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

	"github.com/prometheus/prometheus/storage"

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

type SqlFactory struct {
	ctx context.Context

	query *metadata.Query

	start time.Time
	end   time.Time
	step  time.Duration

	selects []string
	groups  []string

	sql strings.Builder
}

func NewSqlFactory(ctx context.Context, query *metadata.Query) *SqlFactory {
	f := &SqlFactory{
		ctx:     ctx,
		query:   query,
		selects: make([]string, 0),
		groups:  make([]string, 0),
	}
	return f
}

func (f *SqlFactory) write(s string) {
	f.sql.WriteString(s + " ")
}

func (f *SqlFactory) WithRangeTime(start, end time.Time, step time.Duration) *SqlFactory {
	f.start = start
	f.end = end
	f.step = step
	return f
}

func (f *SqlFactory) ParserQuery() (err error) {
	if len(f.query.AggregateMethodList) > 0 {
		var (
			funcName   string
			dimensions []string
			window     time.Duration
		)

		if f.query.IsNotPromQL {
			// 非 PromQL 聚合查询
			if len(f.query.AggregateMethodList) != 1 {
				err = fmt.Errorf("不支持函数嵌套, %+v", f.query.AggregateMethodList)
				return
			}

			am := f.query.AggregateMethodList[0]
			funcName = am.Name
			dimensions = am.Dimensions
		} else {
			if f.query.TimeAggregation != nil {
				hints := &storage.SelectHints{
					Start: f.start.UnixMilli(),
					End:   f.end.UnixMilli(),
					Step:  f.step.Milliseconds(),
					Func:  f.query.TimeAggregation.Function,
					Range: f.query.TimeAggregation.WindowDuration.Milliseconds(),
				}

				funcName, window, dimensions = f.query.GetDownSampleFunc(hints)
				if window == 0 {
					err = fmt.Errorf("聚合周期不能为 0")
					return
				}

				if funcName != "" {
					timeField := fmt.Sprintf("(`%s`- (`%s` %% %d))", dtEventTimeStamp, dtEventTimeStamp, window.Milliseconds())
					f.groups = append(f.groups, timeField)
					f.selects = append(f.selects, fmt.Sprintf("MAX(%s) AS `%s`", timeField, timeStamp))
				}
			}
		}

		if funcName != "" {
			f.selects = append(f.selects, fmt.Sprintf("%s(`%s`) AS `%s`", funcName, f.query.Field, value))
			for _, dim := range dimensions {
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

func (f *SqlFactory) String() string {
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
		orders = append(orders, fmt.Sprintf("%s %s", orderField, ascName))
	}
	if len(orders) > 0 {
		f.write("ORDER BY")
		f.write(strings.Join(orders, ", "))
	}

	return strings.Trim(f.sql.String(), " ")
}
