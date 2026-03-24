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
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/prometheus/prometheus/promql"
)

const (
	DefaultTime  = "_time"
	DefaultValue = "_value"
)

// StatPoint 表示 [时间戳, value]，时间戳 int64，数值 float64，JSON 序列化为 [t, v]
type StatPoint struct {
	T int64   `json:"-"` // 时间戳，序列化时写入数组第 0 位
	V float64 `json:"-"` // 数值，序列化时写入数组第 1 位
}

// MarshalJSON 输出为 [T, V]
func (p StatPoint) MarshalJSON() ([]byte, error) {
	return json.Marshal([2]any{p.T, p.V})
}

// StatItem 统计点集的 Count/Sum/Min/Max/Avg/Last，每项为 [时间戳, value]；Last 为按顺序的最后一个点
type StatItem struct {
	Count StatPoint `json:"count"`
	Sum   StatPoint `json:"sum"`
	Min   StatPoint `json:"min"`
	Max   StatPoint `json:"max"`
	Avg   StatPoint `json:"avg"`
	Last  StatPoint `json:"last"`
}

// TablesItem
type TablesItem struct {
	Name        string    `json:"name"`
	MetricName  string    `json:"metric_name"`
	Columns     []string  `json:"columns"`
	Types       []string  `json:"types"`
	GroupKeys   []string  `json:"group_keys"`
	GroupValues []string  `json:"group_values"`
	Values      [][]any   `json:"values"`
	Stat        *StatItem `json:"stat,omitempty"`
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

// ComputeStatFromPoints 根据点集计算 Stat（Count/Sum/Min/Max/Avg/Last），基于全部点
func ComputeStatFromPoints(points []promql.Point) *StatItem {
	if len(points) == 0 {
		return nil
	}
	var sum float64
	minV, maxV := math.MaxFloat64, -math.MaxFloat64
	minIdx, maxIdx := 0, 0
	for i, p := range points {
		sum += p.V
		if p.V < minV {
			minV = p.V
			minIdx = i
		}
		if p.V > maxV {
			maxV = p.V
			maxIdx = i
		}
	}
	n := float64(len(points))
	avg := sum / n
	last := points[len(points)-1]
	return &StatItem{
		Count: StatPoint{T: 0, V: n},
		Sum:   StatPoint{T: 0, V: sum},
		Min:   StatPoint{T: points[minIdx].T, V: minV},
		Max:   StatPoint{T: points[maxIdx].T, V: maxV},
		Avg:   StatPoint{T: 0, V: avg},
		Last:  StatPoint{T: last.T, V: last.V},
	}
}
