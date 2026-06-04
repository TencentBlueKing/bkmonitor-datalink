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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/downsample"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
)

// 返回结构化数据
type PromData struct {
	dimensions map[string]bool
	// includeResultTableID 表示本次响应需要输出 result_table_id；即使为空也输出 []。
	includeResultTableID bool
	Tables               []*TablesItem    `json:"series"`
	Status               *metadata.Status `json:"status,omitempty"`
	TraceID              string           `json:"trace_id,omitempty"`
	IsPartial            bool             `json:"is_partial"`
	// ResultTableID 来自 QueryReference 路由解析结果；查询响应只暴露 RT 列表，不返回完整 RouteInfo。
	ResultTableID []string `json:"result_table_id,omitempty"`
}

// NewPromData
func NewPromData(dimensions []string) *PromData {
	dimensionsMap := make(map[string]bool)
	for _, dimension := range dimensions {
		dimensionsMap[dimension] = true
	}
	return &PromData{
		dimensions: dimensionsMap,
		Tables:     make([]*TablesItem, 0),
	}
}

// SetResultTableID 标记本次响应需要输出 result_table_id；即使没有路由也输出 []。
func (d *PromData) SetResultTableID(resultTableID []string) {
	d.ResultTableID = normalizeResultTableID(resultTableID)
	d.includeResultTableID = true
}

// normalizeResultTableID 保证成功响应中 result_table_id 为 [] 而不是 null。
func normalizeResultTableID(resultTableID []string) []string {
	if resultTableID == nil {
		return make([]string, 0)
	}
	return resultTableID
}

// SetResultTableIDFromRouteInfo 复用内部路由摘要，只在响应阶段投影为 RT 列表。
func (d *PromData) SetResultTableIDFromRouteInfo(routeInfo []metadata.RouteInfo) {
	d.SetResultTableID(resultTableIDFromRouteInfo(routeInfo))
}

// MarshalJSON 在未调用 SetResultTableID 时沿用 result_table_id 的 omitempty；调用后即使为空也输出 []。
func (d *PromData) MarshalJSON() ([]byte, error) {
	type promData struct {
		Tables        []*TablesItem    `json:"series"`
		Status        *metadata.Status `json:"status,omitempty"`
		TraceID       string           `json:"trace_id,omitempty"`
		IsPartial     bool             `json:"is_partial"`
		ResultTableID []string         `json:"result_table_id,omitempty"`
	}
	if d.includeResultTableID {
		type promDataWithResultTableID struct {
			Tables        []*TablesItem    `json:"series"`
			Status        *metadata.Status `json:"status,omitempty"`
			TraceID       string           `json:"trace_id,omitempty"`
			IsPartial     bool             `json:"is_partial"`
			ResultTableID []string         `json:"result_table_id"`
		}
		return json.Marshal(promDataWithResultTableID{
			Tables:        d.Tables,
			Status:        d.Status,
			TraceID:       d.TraceID,
			IsPartial:     d.IsPartial,
			ResultTableID: normalizeResultTableID(d.ResultTableID),
		})
	}
	return json.Marshal(promData{
		Tables:        d.Tables,
		Status:        d.Status,
		TraceID:       d.TraceID,
		IsPartial:     d.IsPartial,
		ResultTableID: d.ResultTableID,
	})
}

// Fill
func (d *PromData) Fill(tables *promql.Tables) error {
	d.Tables = make([]*TablesItem, 0)
	for index, table := range tables.Tables {
		tableItem := new(TablesItem)
		tableItem.Name = fmt.Sprintf("_result%d", index)
		tableItem.MetricName = table.MetricName
		tableItem.Columns = make([]string, 0, len(table.Headers))
		tableItem.Types = make([]string, 0, len(table.Headers))
		tableItem.GroupKeys = table.GroupKeys
		tableItem.GroupValues = table.GroupValues
		keyMap := make(map[string]bool)
		for _, key := range table.GroupKeys {
			keyMap[key] = true
		}

		indexList := make([]int, 0, len(table.Headers))
		for index, header := range table.Headers {
			// 是key则不输出
			if _, ok := keyMap[header]; ok {
				continue
			}
			if len(d.dimensions) != 0 {
				if _, ok := d.dimensions[header]; !ok {
					continue
				}
			}
			// 记录需要返回的字段及其索引
			tableItem.Columns = append(tableItem.Columns, header)
			tableItem.Types = append(tableItem.Types, table.Types[index])
			indexList = append(indexList, index)
		}
		values := make([][]any, 0)
		for _, data := range table.Data {
			value := make([]any, len(indexList))
			for valueIndex, headerIndex := range indexList {
				value[valueIndex] = data[headerIndex]
			}

			values = append(values, value)
		}
		tableItem.Values = values
		tableItem.Stat = ComputeStatFromPoints(tableItem.GetPromPoints())
		d.Tables = append(d.Tables, tableItem)
	}
	return nil
}

// Downsample 对结果数据进行降采样
func (d *PromData) Downsample(factor float64) {
	for _, table := range d.Tables {
		points := downsample.Downsample(table.GetPromPoints(), factor)
		table.SetValuesByPoints(points)
	}
}
