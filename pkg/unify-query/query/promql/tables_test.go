// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSortByValue(t *testing.T) {
	tables := &Tables{
		Tables: []*Table{
			{
				Name:        "series1",
				GroupKeys:   []string{"host"},
				GroupValues: []string{"host-a"},
				Data:        [][]any{{1000, 30.0}},
			},
			{
				Name:        "series2",
				GroupKeys:   []string{"host"},
				GroupValues: []string{"host-b"},
				Data:        [][]any{{1000, 10.0}},
			},
			{
				Name:        "series3",
				GroupKeys:   []string{"host"},
				GroupValues: []string{"host-c"},
				Data:        [][]any{{1000, 20.0}},
			},
		},
	}

	// 升序排序
	tables.SortByValue(true)
	assert.Equal(t, "host-b", tables.Tables[0].GroupValues[0]) // 10.0
	assert.Equal(t, "host-c", tables.Tables[1].GroupValues[0]) // 20.0
	assert.Equal(t, "host-a", tables.Tables[2].GroupValues[0]) // 30.0

	// 降序排序
	tables.SortByValue(false)
	assert.Equal(t, "host-a", tables.Tables[0].GroupValues[0]) // 30.0
	assert.Equal(t, "host-c", tables.Tables[1].GroupValues[0]) // 20.0
	assert.Equal(t, "host-b", tables.Tables[2].GroupValues[0]) // 10.0
}

func TestSortByOrders_SingleField(t *testing.T) {
	t.Run("sort by _value asc", func(t *testing.T) {
		tables := &Tables{
			Tables: []*Table{
				{
					Name:        "series1",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-a"},
					Data:        [][]any{{1000, 30.0}},
				},
				{
					Name:        "series2",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-b"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series3",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-c"},
					Data:        [][]any{{1000, 20.0}},
				},
			},
		}

		tables.SortByOrders([]Order{{Name: "_value", Asc: true}})
		assert.Equal(t, "host-b", tables.Tables[0].GroupValues[0]) // 10.0
		assert.Equal(t, "host-c", tables.Tables[1].GroupValues[0]) // 20.0
		assert.Equal(t, "host-a", tables.Tables[2].GroupValues[0]) // 30.0
	})

	t.Run("sort by _value desc", func(t *testing.T) {
		tables := &Tables{
			Tables: []*Table{
				{
					Name:        "series1",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-a"},
					Data:        [][]any{{1000, 30.0}},
				},
				{
					Name:        "series2",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-b"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series3",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-c"},
					Data:        [][]any{{1000, 20.0}},
				},
			},
		}

		tables.SortByOrders([]Order{{Name: "_value", Asc: false}})
		assert.Equal(t, "host-a", tables.Tables[0].GroupValues[0]) // 30.0
		assert.Equal(t, "host-c", tables.Tables[1].GroupValues[0]) // 20.0
		assert.Equal(t, "host-b", tables.Tables[2].GroupValues[0]) // 10.0
	})

	t.Run("sort by label field asc", func(t *testing.T) {
		tables := &Tables{
			Tables: []*Table{
				{
					Name:        "series1",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-c"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series2",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-a"},
					Data:        [][]any{{1000, 20.0}},
				},
				{
					Name:        "series3",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-b"},
					Data:        [][]any{{1000, 30.0}},
				},
			},
		}

		tables.SortByOrders([]Order{{Name: "host", Asc: true}})
		assert.Equal(t, "host-a", tables.Tables[0].GroupValues[0])
		assert.Equal(t, "host-b", tables.Tables[1].GroupValues[0])
		assert.Equal(t, "host-c", tables.Tables[2].GroupValues[0])
	})

	t.Run("sort by label field desc", func(t *testing.T) {
		tables := &Tables{
			Tables: []*Table{
				{
					Name:        "series1",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-a"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series2",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-c"},
					Data:        [][]any{{1000, 20.0}},
				},
				{
					Name:        "series3",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-b"},
					Data:        [][]any{{1000, 30.0}},
				},
			},
		}

		tables.SortByOrders([]Order{{Name: "host", Asc: false}})
		assert.Equal(t, "host-c", tables.Tables[0].GroupValues[0])
		assert.Equal(t, "host-b", tables.Tables[1].GroupValues[0])
		assert.Equal(t, "host-a", tables.Tables[2].GroupValues[0])
	})
}

func TestSortByOrders_MultipleFields(t *testing.T) {
	t.Run("sort by host asc, then _value desc", func(t *testing.T) {
		tables := &Tables{
			Tables: []*Table{
				{
					Name:        "series1",
					GroupKeys:   []string{"host", "app"},
					GroupValues: []string{"host-a", "app1"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series2",
					GroupKeys:   []string{"host", "app"},
					GroupValues: []string{"host-b", "app2"},
					Data:        [][]any{{1000, 30.0}},
				},
				{
					Name:        "series3",
					GroupKeys:   []string{"host", "app"},
					GroupValues: []string{"host-a", "app3"},
					Data:        [][]any{{1000, 20.0}},
				},
				{
					Name:        "series4",
					GroupKeys:   []string{"host", "app"},
					GroupValues: []string{"host-b", "app4"},
					Data:        [][]any{{1000, 15.0}},
				},
			},
		}

		// 先按 host 升序，相同 host 再按 _value 降序
		tables.SortByOrders([]Order{
			{Name: "host", Asc: true},
			{Name: "_value", Asc: false},
		})

		// host-a 组: 20.0 > 10.0
		assert.Equal(t, "host-a", tables.Tables[0].GroupValues[0])
		assert.Equal(t, 20.0, tables.Tables[0].Data[0][1])
		assert.Equal(t, "host-a", tables.Tables[1].GroupValues[0])
		assert.Equal(t, 10.0, tables.Tables[1].Data[0][1])

		// host-b 组: 30.0 > 15.0
		assert.Equal(t, "host-b", tables.Tables[2].GroupValues[0])
		assert.Equal(t, 30.0, tables.Tables[2].Data[0][1])
		assert.Equal(t, "host-b", tables.Tables[3].GroupValues[0])
		assert.Equal(t, 15.0, tables.Tables[3].Data[0][1])
	})

	t.Run("sort by _value asc, then host desc", func(t *testing.T) {
		tables := &Tables{
			Tables: []*Table{
				{
					Name:        "series1",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-a"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series2",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-c"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series3",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-b"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series4",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-d"},
					Data:        [][]any{{1000, 20.0}},
				},
			},
		}

		// 先按 _value 升序，相同 _value 再按 host 降序
		tables.SortByOrders([]Order{
			{Name: "_value", Asc: true},
			{Name: "host", Asc: false},
		})

		// _value=10.0 组，按 host 降序: c > b > a
		assert.Equal(t, "host-c", tables.Tables[0].GroupValues[0])
		assert.Equal(t, "host-b", tables.Tables[1].GroupValues[0])
		assert.Equal(t, "host-a", tables.Tables[2].GroupValues[0])
		// _value=20.0
		assert.Equal(t, "host-d", tables.Tables[3].GroupValues[0])
	})

	t.Run("sort by multiple labels", func(t *testing.T) {
		tables := &Tables{
			Tables: []*Table{
				{
					Name:        "series1",
					GroupKeys:   []string{"region", "host"},
					GroupValues: []string{"us", "host-b"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series2",
					GroupKeys:   []string{"region", "host"},
					GroupValues: []string{"eu", "host-a"},
					Data:        [][]any{{1000, 20.0}},
				},
				{
					Name:        "series3",
					GroupKeys:   []string{"region", "host"},
					GroupValues: []string{"us", "host-a"},
					Data:        [][]any{{1000, 30.0}},
				},
				{
					Name:        "series4",
					GroupKeys:   []string{"region", "host"},
					GroupValues: []string{"eu", "host-b"},
					Data:        [][]any{{1000, 40.0}},
				},
			},
		}

		// 先按 region 升序，再按 host 升序
		tables.SortByOrders([]Order{
			{Name: "region", Asc: true},
			{Name: "host", Asc: true},
		})

		// eu 组: host-a, host-b
		assert.Equal(t, "eu", tables.Tables[0].GroupValues[0])
		assert.Equal(t, "host-a", tables.Tables[0].GroupValues[1])
		assert.Equal(t, "eu", tables.Tables[1].GroupValues[0])
		assert.Equal(t, "host-b", tables.Tables[1].GroupValues[1])

		// us 组: host-a, host-b
		assert.Equal(t, "us", tables.Tables[2].GroupValues[0])
		assert.Equal(t, "host-a", tables.Tables[2].GroupValues[1])
		assert.Equal(t, "us", tables.Tables[3].GroupValues[0])
		assert.Equal(t, "host-b", tables.Tables[3].GroupValues[1])
	})
}

func TestSortByOrders_EdgeCases(t *testing.T) {
	t.Run("empty tables", func(t *testing.T) {
		tables := &Tables{Tables: []*Table{}}
		tables.SortByOrders([]Order{{Name: "_value", Asc: true}})
		assert.Empty(t, tables.Tables)
	})

	t.Run("empty orders", func(t *testing.T) {
		tables := &Tables{
			Tables: []*Table{
				{
					Name:        "series1",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-b"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series2",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-a"},
					Data:        [][]any{{1000, 20.0}},
				},
			},
		}
		// 空 orders 不改变顺序
		tables.SortByOrders([]Order{})
		assert.Equal(t, "host-b", tables.Tables[0].GroupValues[0])
		assert.Equal(t, "host-a", tables.Tables[1].GroupValues[0])
	})

	t.Run("non-existent label field", func(t *testing.T) {
		tables := &Tables{
			Tables: []*Table{
				{
					Name:        "series1",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-b"},
					Data:        [][]any{{1000, 10.0}},
				},
				{
					Name:        "series2",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-a"},
					Data:        [][]any{{1000, 20.0}},
				},
			},
		}
		// 不存在的字段返回空字符串，所有值相等，保持原顺序（稳定排序）
		tables.SortByOrders([]Order{{Name: "non_existent", Asc: true}})
		assert.Equal(t, "host-b", tables.Tables[0].GroupValues[0])
		assert.Equal(t, "host-a", tables.Tables[1].GroupValues[0])
	})

	t.Run("empty data", func(t *testing.T) {
		tables := &Tables{
			Tables: []*Table{
				{
					Name:        "series1",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-a"},
					Data:        [][]any{},
				},
				{
					Name:        "series2",
					GroupKeys:   []string{"host"},
					GroupValues: []string{"host-b"},
					Data:        [][]any{{1000, 10.0}},
				},
			},
		}
		// 空 Data 的 _value 为 0
		tables.SortByOrders([]Order{{Name: "_value", Asc: true}})
		assert.Equal(t, "host-a", tables.Tables[0].GroupValues[0]) // 0
		assert.Equal(t, "host-b", tables.Tables[1].GroupValues[0]) // 10.0
	})
}

func TestGetGroupValue(t *testing.T) {
	table := &Table{
		GroupKeys:   []string{"host", "region", "app"},
		GroupValues: []string{"host-a", "us-west", "myapp"},
	}

	assert.Equal(t, "host-a", table.getGroupValue("host"))
	assert.Equal(t, "us-west", table.getGroupValue("region"))
	assert.Equal(t, "myapp", table.getGroupValue("app"))
	assert.Equal(t, "", table.getGroupValue("non_existent"))
}

func TestGetLastValue(t *testing.T) {
	t.Run("normal data", func(t *testing.T) {
		table := &Table{
			Data: [][]any{
				{1000, 10.0},
				{2000, 20.0},
				{3000, 30.0},
			},
		}
		assert.Equal(t, 30.0, table.getLastValue())
	})

	t.Run("empty data", func(t *testing.T) {
		table := &Table{Data: [][]any{}}
		assert.Equal(t, 0.0, table.getLastValue())
	})

	t.Run("single row with less than 2 columns", func(t *testing.T) {
		table := &Table{Data: [][]any{{1000}}}
		assert.Equal(t, 0.0, table.getLastValue())
	})
}
