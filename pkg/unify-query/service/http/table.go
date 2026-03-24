// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"fmt"
	"strings"

	"github.com/prometheus/prometheus/promql"
)

const (
	DefaultTime  = "_time"
	DefaultValue = "_value"
)

// TablesItem
type TablesItem struct {
	Name        string   `json:"name"`
	MetricName  string   `json:"metric_name"`
	Columns     []string `json:"columns"`
	Types       []string `json:"types"`
	GroupKeys   []string `json:"group_keys"`
	GroupValues []string `json:"group_values"`
	Values      [][]any  `json:"values"`
}

// String
func (t *TablesItem) String() string {
	b := new(strings.Builder)
	b.WriteString(fmt.Sprintf("columns:%v\n", t.Columns))
	b.WriteString(fmt.Sprintf("types:%v\n", t.Types))
	b.WriteString(fmt.Sprintf("group keys:%v\n", t.GroupKeys))
	b.WriteString(fmt.Sprintf("group values:%v\n", t.GroupValues))
	for _, data := range t.Values {
		b.WriteString(fmt.Sprintf("%v\n", data))
	}
	return b.String()
}

// GetPromPoints values 转换为 prom 点格式
func (t *TablesItem) GetPromPoints() []promql.Point {
	points := make([]promql.Point, 0, len(t.Values))
	for _, value := range t.Values {
		var (
			v  float64
			ts int64
			ok bool
		)

		// influxdb db 类型
		if t.Columns[0] == DefaultTime && t.Columns[1] == DefaultValue {
			ts, ok = value[0].(int64)
			if !ok {
				continue
			}
			v, ok = value[1].(float64)
			if !ok {
				continue
			}
		} else { // argus 类型
			v, ok = value[0].(float64)
			if !ok {
				continue
			}

			ts, ok = value[1].(int64)
			if !ok {
				continue
			}
		}

		points = append(points, promql.Point{T: ts, V: v})
	}
	return points
}

// SetValuesByPoints
func (t *TablesItem) SetValuesByPoints(points []promql.Point) {
	values := make([][]any, 0, len(points))
	for _, point := range points {
		if t.Columns[0] == DefaultTime && t.Columns[1] == DefaultValue {
			values = append(values, []any{
				point.T, point.V,
			})
		} else {
			values = append(values, []any{
				point.V, point.T,
			})
		}
	}
	t.Values = values
}
