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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
)

func TestPromData_Fill_FillsStat(t *testing.T) {
	tables := promql.NewTables()
	tables.Add(&promql.Table{
		Headers:     []string{"_time", "_value"},
		Types:       []string{"float", "float"},
		GroupKeys:   []string{},
		GroupValues: []string{},
		Data: [][]any{
			{int64(1000), 10.0},
			{int64(2000), 20.0},
		},
	})
	d := NewPromData(nil)
	err := d.Fill(tables)
	assert.NoError(t, err)
	assert.Len(t, d.Tables, 1)
	stat := d.Tables[0].Stat
	assert.NotNil(t, stat)
	assert.Equal(t, float64(2), stat.Count.V)
	assert.Equal(t, float64(30), stat.Sum.V)
	assert.Equal(t, int64(1000), stat.Min.T)
	assert.Equal(t, float64(10), stat.Min.V)
	assert.Equal(t, int64(2000), stat.Max.T)
	assert.Equal(t, float64(20), stat.Max.V)
	assert.Equal(t, float64(15), stat.Avg.V)
	assert.Equal(t, int64(2000), stat.Last.T)
	assert.Equal(t, float64(20), stat.Last.V)
}

// Fill 已写入 Stat；Downsample 只缩减 Values，Stat 仍为降采样前点集统计。
func TestPromData_Downsample_PreservesStatAndReducesPoints(t *testing.T) {
	tables := promql.NewTables()
	// 10 个点，降采样后应少于 10 个
	data := make([][]any, 10)
	for i := 0; i < 10; i++ {
		data[i] = []any{int64(1000 + i*100), float64(i + 1)}
	}
	tables.Add(&promql.Table{
		Headers:     []string{"_time", "_value"},
		Types:       []string{"float", "float"},
		GroupKeys:   []string{},
		GroupValues: []string{},
		Data:        data,
	})

	d := NewPromData(nil)
	err := d.Fill(tables)
	assert.NoError(t, err)
	assert.Len(t, d.Tables, 1)
	origLen := len(d.Tables[0].Values)
	assert.Equal(t, 10, origLen)

	statAfterFill := d.Tables[0].Stat
	assert.NotNil(t, statAfterFill)

	// factor 0.3 => threshold = ceil(10*0.3) = 3，降采样后 3 个点
	d.Downsample(0.3)
	assert.Same(t, statAfterFill, d.Tables[0].Stat, "Downsample 不应替换 Stat，仅缩减 Values")
	assert.NotNil(t, d.Tables[0].Stat)
	// Stat 应为降采样前的统计：10 个点
	assert.Equal(t, float64(10), d.Tables[0].Stat.Count.V)
	assert.Equal(t, float64(55), d.Tables[0].Stat.Sum.V) // 1+2+...+10
	assert.Equal(t, int64(1900), d.Tables[0].Stat.Last.T)
	assert.Equal(t, float64(10), d.Tables[0].Stat.Last.V)
	// Values 应为降采样后的点数（变少）
	assert.Less(t, len(d.Tables[0].Values), origLen)
}
