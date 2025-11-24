// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/influxdata/influxdb/models"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

const (
	ResultColumnName = "_value"
	TimeColumnName   = "_time"
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

// Length
func (t *Table) Length() int {
	return len(t.Data)
}

// github.com/influxdata/influxdb/models/rows.go
func (t *Table) SameSeries(o *Table) bool {
	return t.Hash() == o.Hash()
}

// tagsHash returns a hash of tag key/value pairs.
func (t *Table) Hash(ignoreMetric ...bool) uint64 {
	var isIgnoreMetric bool
	if len(ignoreMetric) == 0 {
		isIgnoreMetric = false
	} else {
		isIgnoreMetric = ignoreMetric[0]
	}
	h := models.NewInlineFNV64a()
	keys := t.GroupKeys
	for k, v := range keys {
		_, _ = h.Write([]byte(v))
		_, _ = h.Write([]byte(t.GroupValues[k]))
	}
	if !isIgnoreMetric {
		_, _ = h.Write([]byte(t.MetricName))
	}

	return h.Sum64()
}

// GroupBySeries 基于维度进行重新分组
func GroupBySeries(ctx context.Context, seriesList []*decoder.Row) []*decoder.Row {
	seriesMap := make(map[string]*decoder.Row)
	seriesLimit := make(map[string]int)
	keyList := make([]string, 0)
	// 生成series名
	seriesCount := 0
	for _, series := range seriesList {
		dimensions := make([]string, 0)
		// 先定位该series的位置
		columnIndex := make(map[string]int)
		// 通过columns获得维度列表
		for index, column := range series.Columns {
			// 除了value和time,其余全是维度
			if column != ResultColumnName && column != TimeColumnName {
				dimensions = append(dimensions, column)
			}
			columnIndex[column] = index
		}

		// map 需要先排序
		oldTagKeys := make([]string, 0, len(series.Tags))
		for k := range series.Tags {
			if k != "" {
				oldTagKeys = append(oldTagKeys, k)
			}
		}
		sort.Strings(oldTagKeys)

		// 逐行遍历数据，每行都获取维度并拼接为key
	valuesLoop:
		for _, values := range series.Values {
			keyBuilder := new(strings.Builder)
			equal := "="
			comma := ","

			tags := make(map[string]string, len(oldTagKeys)+len(dimensions))
			for _, k := range oldTagKeys {
				tags[k] = series.Tags[k]
				keyBuilder.WriteString(k)
				keyBuilder.WriteString(equal)
				keyBuilder.WriteString(series.Tags[k])
				keyBuilder.WriteString(comma)
			}

			for _, dimension := range dimensions {
				if index, ok := columnIndex[dimension]; ok {
					if values[index] == nil {
						continue
					}
					value, ok := values[index].(string)
					if !ok {
						metadata.NewMessage(
							metadata.MsgTableFormat,
							"数据类型 %v 错误",
							values[index],
						).Warn(ctx)
						continue
					}
					tags[dimension] = value

					keyBuilder.WriteString(dimension)
					keyBuilder.WriteString(equal)
					keyBuilder.WriteString(value)
					keyBuilder.WriteString(comma)
				} else {
					// 跳过获取不到的dimension，并打印日志
					metadata.NewMessage(
						metadata.MsgTableFormat,
						"维度缺失",
					).Warn(ctx)
				}
			}

			// 约定的column信息
			resultColumns := []string{ResultColumnName, TimeColumnName}

			// 获取value的值
			resultValues := make([]any, 0)
			for _, resultColumn := range resultColumns {
				if index, ok := columnIndex[resultColumn]; ok {
					resultValues = append(resultValues, values[index])
				} else {
					metadata.NewMessage(
						metadata.MsgTableFormat,
						"维度缺失",
					).Warn(ctx)
					continue valuesLoop
				}
			}

			// 基于维度生成唯一key，进行row的分组
			key := keyBuilder.String()
			// 如果key存在，则将对应数据直接追加到尾部,否则基于维度信息生成新row
			row, ok := seriesMap[key]
			if !ok {
				row = &decoder.Row{
					Name:    fmt.Sprintf("result_%d", seriesCount),
					Tags:    tags,
					Columns: resultColumns,
					Values:  make([][]any, 0),
				}
				seriesMap[key] = row
				seriesLimit[key] = 1
				keyList = append(keyList, key)
				seriesCount++
			}
			row.Values = append(row.Values, resultValues)
		}
	}
	// 排序key，以保证输出数据的顺序
	sort.Strings(keyList)
	rows := make([]*decoder.Row, 0, len(keyList))
	for _, key := range keyList {
		rows = append(rows, seriesMap[key])
	}
	return rows
}

// NewTable
func NewTable(metricName string, series *decoder.Row, expandTag map[string]string) *Table {
	t := new(Table)
	// 增加metricName
	t.MetricName = metricName
	// header对应的就是列名
	t.Headers = series.Columns

	// type在返回数据里没有给出，所以需要动态加载
	t.Types = make([]string, len(t.Headers))

	// 数据类型通过type提供，所以这里直接全转换为string
	t.Data = series.Values

	// 合并扩展Tag
	if expandTag != nil {
		if series.Tags != nil {
			for k, v := range expandTag {
				if _, ok := series.Tags[k]; !ok {
					series.Tags[k] = v
				} else {
					metadata.NewMessage(
						metadata.MsgTableFormat,
						"维度缺失",
					).Warn(context.TODO())
				}
			}
		} else {
			series.Tags = expandTag
		}
	}

	// group信息与tags对应
	t.GroupKeys = make([]string, 0, len(series.Tags))
	t.GroupValues = make([]string, 0, len(series.Tags))

	t.Name = series.Name

	tags := make([]string, 0)
	for tagKey := range series.Tags {
		tags = append(tags, tagKey)
	}
	sort.Strings(tags)
	// 根据tags获取group信息
	for _, tagKey := range tags {
		t.GroupKeys = append(t.GroupKeys, tagKey)
		t.GroupValues = append(t.GroupValues, series.Tags[tagKey])
	}

	sort.Strings(t.GroupKeys)

	// 获取数据类型,每个列根据数据查找对应的类型
	for headerIndex := range t.Headers {
		for _, value := range series.Values {
			item := value[headerIndex]
			if item == nil {
				continue
			}
			typeStr := ""
			// 不是字符串就是浮点型
			if _, ok := item.(string); ok {
				typeStr = "string"
			} else {
				typeStr = "float"
			}
			t.Types[headerIndex] = typeStr
			// 找到一个就可以中断这次的循环,然后进入下一个字段的查找
			break
		}
	}

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
	Index  int
	Tables []*Table
}

// Length
func (t *Tables) Length() int {
	length := 0
	for _, table := range t.Tables {
		length += table.Length()
	}
	return length
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
func (t *Tables) Add(tables ...*Table) {
	t.Tables = append(t.Tables, tables...)
}

// Clear
func (t *Tables) Clear() error {
	t.Tables = make([]*Table, 0)
	return nil
}

// MergeTables : 直接合并相同维度的数据
func MergeTables(tableList []*Tables, ignoreMetric bool) *Tables {
	resultTab := NewTables()
	mapTag := make(map[uint64]*Table, 0)

	// 增加排序逻辑
	sort.SliceStable(tableList, func(i, j int) bool {
		return tableList[i].Index < tableList[j].Index
	})
	for _, tables := range tableList {
		for _, table := range tables.Tables {
			key := table.Hash(ignoreMetric)
			if res, has := mapTag[key]; has {
				res.Data = append(res.Data, table.Data...)
			} else {
				mapTag[key] = table
			}
		}
	}

	for _, t := range mapTag {
		resultTab.Tables = append(resultTab.Tables, t)
	}
	return resultTab
}
