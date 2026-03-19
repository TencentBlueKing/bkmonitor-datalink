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

func TestPromData_NoDownsample_NoStat(t *testing.T) {
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
	// 未调用 Downsample 时不应有 Stat
	assert.Nil(t, d.Tables[0].Stat)
}

func TestPromData_Downsample_FillsStatAndReducesPoints(t *testing.T) {
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

	// factor 0.3 => threshold = ceil(10*0.3) = 3，降采样后 3 个点
	d.Downsample(0.3)
	assert.NotNil(t, d.Tables[0].Stat)
	// Stat 应为降采样前的统计：10 个点
	assert.Equal(t, float64(10), d.Tables[0].Stat.Count.V)
	assert.Equal(t, float64(55), d.Tables[0].Stat.Sum.V) // 1+2+...+10
	// Values 应为降采样后的点数（变少）
	assert.Less(t, len(d.Tables[0].Values), origLen)
}
