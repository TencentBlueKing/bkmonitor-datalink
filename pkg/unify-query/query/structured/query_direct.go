// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"

	"github.com/jinzhu/copier"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

type QueryDirect struct {
	// References 查询引用的指标列表
	References metadata.QueryReference `json:"references,omitempty"`
	// SpaceUid 空间ID
	SpaceUid string `json:"space_uid,omitempty"`
	// MetricMerge 表达式：支持所有PromQL语法
	MetricMerge string `json:"metric_merge,omitempty" example:"a"`
	// OrderBy 排序字段列表，按顺序排序，负数代表倒序, ["_time", "-_time"]
	OrderBy OrderBy `json:"order_by,omitempty"`
	// ResultColumns 指定保留返回字段值
	ResultColumns []string `json:"result_columns,omitempty" swaggerignore:"true"`
	// Start 开始时间：单位为任意长度的时间戳
	Start string `json:"start_time,omitempty" example:"1657848000"`
	// End 结束时间：单位为任意长度的时间戳
	End string `json:"end_time,omitempty" example:"1657851600"`
	// Step 步长：最终返回的点数的时间间隔
	Step string `json:"step,omitempty" example:"1m"`
	// DownSampleRange 降采样：大于Step才能生效，可以为空
	DownSampleRange string `json:"down_sample_range,omitempty" example:"5m"`
	// Timezone 时区
	Timezone string `json:"timezone,omitempty" example:"Asia/Shanghai"`
	// LookBackDelta 偏移量
	LookBackDelta string `json:"look_back_delta,omitempty"`
	// Instant 瞬时数据
	Instant bool `json:"instant"`

	// Reference 查询开始时间是否需要对齐，
	// 例如：
	// true:  range: 10:03 - 10:23 window: 10m -> 10:03 - 10:10, 10:10 - 10:20, 10:20 - 10:23
	// false: range: 10:03 - 10:23 window: 10m -> 10:00 - 10:10, 10:10 - 10:20, 10:20 - 10:23
	Reference bool `json:"reference,omitempty"`

	// NotTimeAlign 查询开始时间和聚合是否需要对齐
	// 例如
	// true:  range: 10:03 - 10:23 window: 10m -> 10:03 - 10:13, 10:13 - 10:23
	// false: range: 10:03 - 10:23 window: 10m -> 10:00 - 10:10, 10:10 - 10:20, 10:20 - 10:23
	NotTimeAlign bool `json:"not_time_align"`

	// 增加公共限制
	// Limit 点数限制数量
	Limit int `json:"limit,omitempty" example:"0"`
	// From 翻页开启数字
	From int `json:"from,omitempty" example:"0"`

	// Scroll 是否启用 Scroll 查询
	Scroll string `json:"scroll,omitempty"`
	// SliceMax 最大切片数量
	SliceMax int `json:"slice_max,omitempty"`
	// IsMultiFrom 是否启用 MultiFrom 查询
	IsMultiFrom bool `json:"is_multi_from,omitempty"`
	// IsSearchAfter 是否启用 SearchAfter 查询
	IsSearchAfter bool `json:"is_search_after,omitempty"`
	// ClearCache 是否强制清理已存在的缓存会话
	ClearCache bool `json:"clear_cache,omitempty"`

	ResultTableOptions metadata.ResultTableOptions `json:"result_table_options,omitempty"`

	// HighLight 是否开启高亮
	HighLight *metadata.HighLight `json:"highlight,omitempty"`

	// DryRun 是否启用 DryRun
	DryRun bool `json:"dry_run,omitempty"`

	// IsMergeDB 是否启用合并 db 特性
	IsMergeDB bool `json:"is_merge_db,omitempty"`
}

func (q *QueryDirect) GetReferences(ctx context.Context) metadata.QueryReference {
	err := q.ToTime(ctx)
	if err != nil {
		_ = metadata.Sprintf(
			metadata.MsgQueryTs,
			"parse time error: %v",
			err,
		).Error(ctx, err)
		return nil
	}

	queryReference := make(metadata.QueryReference)
	for refName, queryMetrics := range q.References {
		for _, queryMetric := range queryMetrics {
			if queryMetric == nil {
				continue
			}

			newQueryMetric := &metadata.QueryMetric{
				ReferenceName: queryMetric.ReferenceName,
				MetricName:    queryMetric.MetricName,
				IsCount:       queryMetric.IsCount,
				QueryList:     make([]*metadata.Query, 0, len(queryMetric.QueryList)),
			}

			for _, query := range queryMetric.QueryList {
				if query == nil {
					continue
				}

				newQuery := &metadata.Query{}
				if copyErr := copier.CopyWithOption(newQuery, query, copier.Option{DeepCopy: true}); copyErr != nil {
					continue
				}

				// 复用 QueryDirect 的时间配置
				newQuery.Timezone = q.Timezone
				newQuery.TimeField = metadata.TimeField{
					Name: query.TimeField.Name,
					Type: query.TimeField.Type,
					Unit: query.TimeField.Unit,
				}

				// 复用 QueryDirect 的通用配置
				if q.DryRun {
					newQuery.DryRun = q.DryRun
				}

				if q.IsMergeDB {
					newQuery.IsMergeDB = q.IsMergeDB
				}

				// 应用 QueryDirect 的限制配置
				if query.From == 0 && q.From > 0 {
					newQuery.From = q.From
				}

				if query.Size == 0 && q.Limit > 0 {
					newQuery.Size = q.Limit
				}

				// 处理 ResultTableOptions
				if q.ResultTableOptions != nil {
					tableUUID := newQuery.TableUUID()
					newQuery.ResultTableOption = q.ResultTableOptions.GetOption(tableUUID)
				}

				// 处理 Scroll 配置
				if q.Scroll != "" {
					newQuery.Scroll = q.Scroll
				}

				newQueryMetric.QueryList = append(newQueryMetric.QueryList, newQuery)
			}

			if len(newQueryMetric.QueryList) > 0 {
				queryReference[refName] = append(queryReference[refName], newQueryMetric)
			}
		}
	}

	metadata.SetQueryReference(ctx, queryReference)

	return queryReference
}

// ToTime 解析并设置时间参数，复用 QueryTs 的逻辑
func (q *QueryDirect) ToTime(ctx context.Context) error {
	unit, startTime, endTime, err := function.QueryTimestamp(q.Start, q.End)
	if err != nil {
		return err
	}

	timezone := q.Timezone
	reference := q.Reference
	alianStart := startTime

	step := StepParse(q.Step)

	// 如果关闭了对齐，则无需对齐开始时间
	if !q.NotTimeAlign {
		// 根据 timezone 来对齐开始时间
		alianStart = function.TimeOffset(startTime, timezone, step)
	}

	metadata.GetQueryParams(ctx).SetTime(alianStart, startTime, endTime, step, unit, timezone).SetIsReference(reference)
	return nil
}

func (q *QueryDirect) GetLabelMap(ctx context.Context) map[string][]function.LabelMapValue {
	if len(q.References) == 0 {
		return nil
	}

	allLabelMap := make(map[string][]function.LabelMapValue)
	queryRef := q.GetReferences(ctx)
	if queryRef == nil {
		return nil
	}

	queryRef.Range("", func(qry *metadata.Query) {
		if labelMap := function.LabelMap(ctx, qry); labelMap != nil {
			// 合并 labelMap
			for k, lm := range labelMap {
				if _, ok := allLabelMap[k]; !ok {
					allLabelMap[k] = make([]function.LabelMapValue, 0)
				}
				allLabelMap[k] = append(allLabelMap[k], lm...)
			}
		}
	})

	return allLabelMap
}
