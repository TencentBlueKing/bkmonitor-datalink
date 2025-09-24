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
	"fmt"
	"math"
	"strconv"
	"strings"

	prom "github.com/prometheus/prometheus/promql"
)

// Table :
type Table struct {
	Name        string
	MetricName  string
	Headers     []string
	Types       []string
	GroupKeys   []string
	GroupValues []string
	Data        [][]any
}

// NewTableWithSample
func NewTableWithSample(index int, sample prom.Sample, queryRawFormat func(string) string) *Table {
	t := new(Table)
	// header对应的就是列名,promql的数据列是固定的
	t.Headers = []string{"_time", "_value"}
	t.Types = []string{"float", "float"}

	// 数据类型通过type提供，所以这里直接全转换为string
	t.Data = make([][]any, 0)
	t.Data = append(t.Data, []any{sample.Point.T, sample.Point.V})
	// group信息与tags对应
	t.GroupKeys = make([]string, 0, len(sample.Metric))
	t.GroupValues = make([]string, 0, len(sample.Metric))
	// 根据labels获取group信息
	for _, label := range sample.Metric {
		if queryRawFormat != nil {
			label.Name = queryRawFormat(label.Name)
		}

		// es 查询使用了空格作为占位符，所以这里需要将标签中的空格替换掉
		if label.Value == " " {
			label.Value = ""
		}

		t.GroupKeys = append(t.GroupKeys, label.Name)
		t.GroupValues = append(t.GroupValues, label.Value)
	}

	t.Name = "series" + strconv.Itoa(index)

	return t
}

// NewTable
func NewTable(index int, series prom.Series, queryRawFormat func(string) string) *Table {
	t := new(Table)
	// header对应的就是列名,promql的数据列是固定的
	t.Headers = []string{"_time", "_value"}
	t.Types = []string{"float", "float"}

	// 数据类型通过type提供，所以这里直接全转换为string
	t.Data = make([][]any, 0)
	for _, point := range series.Points {
		// 跳过Inf和NAN数据，这种数据无法通过json序列化
		if math.IsInf(point.V, 0) || math.IsNaN(point.V) {
			continue
		}
		t.Data = append(t.Data, []any{point.T, point.V})
	}

	// group信息与tags对应
	t.GroupKeys = make([]string, 0, len(series.Metric))
	t.GroupValues = make([]string, 0, len(series.Metric))
	// 根据labels获取group信息
	for _, label := range series.Metric {
		// 过滤随机标签数据
		if queryRawFormat != nil {
			label.Name = queryRawFormat(label.Name)
		}

		// es 查询使用了空格作为占位符，所以这里需要将标签中的空格替换掉
		if label.Value == " " {
			label.Value = ""
		}

		t.GroupKeys = append(t.GroupKeys, label.Name)
		t.GroupValues = append(t.GroupValues, label.Value)
	}

	t.Name = "series" + strconv.Itoa(index)

	return t
}

// String
func (t *Table) String() string {
	b := new(strings.Builder)
	b.WriteString(fmt.Sprintf("headers:%v\n", t.Headers))
	b.WriteString(fmt.Sprintf("types:%v\n", t.Types))
	b.WriteString(fmt.Sprintf("group keys:%v\n", t.GroupKeys))
	b.WriteString(fmt.Sprintf("group values:%v\n", t.GroupValues))
	for _, data := range t.Data {
		b.WriteString(fmt.Sprintf("%v\n", data))
	}
	return b.String()
}

// Tables
type Tables struct {
	Tables []*Table
}

// String
func (t *Tables) String() string {
	b := new(strings.Builder)
	for _, table := range t.Tables {
		b.WriteString(table.String())
	}
	return b.String()
}

// NewTables
func NewTables() *Tables {
	return &Tables{
		Tables: make([]*Table, 0),
	}
}

// Add
func (t *Tables) Add(table *Table) {
	t.Tables = append(t.Tables, table)
}

// Clear
func (t *Tables) Clear() error {
	t.Tables = make([]*Table, 0)
	return nil
}
