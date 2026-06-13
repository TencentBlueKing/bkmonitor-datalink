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
	"math"
	"testing"

	"github.com/prometheus/prometheus/promql"
	"github.com/stretchr/testify/assert"
)

func TestComputeStatFromPoints_Empty(t *testing.T) {
	got := ComputeStatFromPoints(nil)
	assert.Nil(t, got)
	got = ComputeStatFromPoints([]promql.Point{})
	assert.Nil(t, got)
}

func TestComputeStatFromPoints_SinglePoint(t *testing.T) {
	points := []promql.Point{{T: 1000, V: 10}}
	got := ComputeStatFromPoints(points)
	requireStat(t, got, 1, 10, 10, 10, 10, 1000, 1000, 1000, 10)
}

func TestComputeStatFromPoints_MultiplePoints(t *testing.T) {
	points := []promql.Point{
		{T: 1000, V: 10},
		{T: 2000, V: 20},
		{T: 3000, V: 30},
	}
	got := ComputeStatFromPoints(points)
	// count=3, sum=60, min=10@1000, max=30@3000, avg=20, last=30@3000
	requireStat(t, got, 3, 60, 10, 30, 20, 1000, 3000, 3000, 30)
}

func TestComputeStatFromPoints_MinMaxIndices(t *testing.T) {
	points := []promql.Point{
		{T: 1000, V: 50},
		{T: 2000, V: 10},
		{T: 3000, V: 90},
	}
	got := ComputeStatFromPoints(points)
	assert.NotNil(t, got)
	assert.Equal(t, int64(2000), got.Min.T, "min at 2000")
	assert.Equal(t, float64(10), got.Min.V)
	assert.Equal(t, int64(3000), got.Max.T, "max at 3000")
	assert.Equal(t, float64(90), got.Max.V)
	assert.Equal(t, float64(3), got.Count.V)
	assert.Equal(t, float64(150), got.Sum.V)
	assert.Equal(t, float64(50), got.Avg.V)
	assert.Equal(t, int64(3000), got.Last.T)
	assert.Equal(t, float64(90), got.Last.V)
}

func TestComputeStatFromPoints_OverflowStatMarshal(t *testing.T) {
	points := []promql.Point{
		{T: 1000, V: math.MaxFloat64},
		{T: 2000, V: math.MaxFloat64},
	}

	got := ComputeStatFromPoints(points)
	assert.NotNil(t, got)
	assert.True(t, math.IsInf(got.Sum.V, 1))
	assert.False(t, math.IsInf(got.Avg.V, 0))
	assert.False(t, math.IsNaN(got.Avg.V))
	assert.Equal(t, math.MaxFloat64, got.Avg.V)
	assert.Equal(t, math.MaxFloat64, got.Min.V)
	assert.Equal(t, int64(1000), got.Min.T)
	assert.Equal(t, math.MaxFloat64, got.Max.V)
	assert.Equal(t, int64(1000), got.Max.T)
	assert.Equal(t, math.MaxFloat64, got.Last.V)
	assert.Equal(t, int64(2000), got.Last.T)
	assert.Equal(t, float64(2), got.Count.V)

	b, err := json.Marshal(got)
	assert.NoError(t, err)
	assert.Contains(t, string(b), `"sum":[0,null]`)
	assert.Contains(t, string(b), `"avg":[0,1.7976931348623157e+308]`)
}

func requireStat(t *testing.T, s *StatItem, count int, sum, minV, maxV, avg float64, minT, maxT, lastT int64, lastV float64) {
	t.Helper()
	assert.NotNil(t, s)
	assert.Equal(t, float64(count), s.Count.V)
	assert.Equal(t, int64(0), s.Count.T)
	assert.Equal(t, sum, s.Sum.V)
	assert.Equal(t, int64(0), s.Sum.T)
	assert.Equal(t, minV, s.Min.V)
	assert.Equal(t, minT, s.Min.T)
	assert.Equal(t, maxV, s.Max.V)
	assert.Equal(t, maxT, s.Max.T)
	assert.Equal(t, avg, s.Avg.V)
	assert.Equal(t, int64(0), s.Avg.T)
	assert.Equal(t, lastT, s.Last.T)
	assert.Equal(t, lastV, s.Last.V)
}

func TestStatPoint_MarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		p    StatPoint
		want string
	}{
		{
			name: "finite",
			p:    StatPoint{T: 1773308220000, V: 10},
			want: `[1773308220000,10]`,
		},
		{
			name: "positive inf",
			p:    StatPoint{T: 1773308220000, V: math.Inf(1)},
			want: `[1773308220000,null]`,
		},
		{
			name: "negative inf",
			p:    StatPoint{T: 1773308220000, V: math.Inf(-1)},
			want: `[1773308220000,null]`,
		},
		{
			name: "nan",
			p:    StatPoint{T: 1773308220000, V: math.NaN()},
			want: `[1773308220000,null]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := json.Marshal(tt.p)
			assert.NoError(t, err)
			assert.Equal(t, tt.want, string(b))
		})
	}
}

func TestTablesItem_GetPromPoints_And_Stat(t *testing.T) {
	// TablesItem 使用 influxdb 列顺序 _time, _value
	item := &TablesItem{
		Columns: []string{DefaultTime, DefaultValue},
		Types:   []string{"float", "float"},
		Values: [][]any{
			{int64(1000), 10.0},
			{int64(2000), 20.0},
		},
	}
	points := item.GetPromPoints()
	assert.Len(t, points, 2)
	assert.Equal(t, int64(1000), points[0].T)
	assert.Equal(t, 10.0, points[0].V)
	assert.Equal(t, int64(2000), points[1].T)
	assert.Equal(t, 20.0, points[1].V)

	item.Stat = ComputeStatFromPoints(points)
	requireStat(t, item.Stat, 2, 30, 10, 20, 15, 1000, 2000, 2000, 20)
}
