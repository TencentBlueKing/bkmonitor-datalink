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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
)

// DataIDFilter 这里需要bizID, projectID, clusterID的路由
type DataIDFilter struct {
	onlyCondition bool
	metricDataID  []consul.DataID
	// [{"bk_biz_id": [1,12]},{"cluster_id": ["cluster1", "cluster2"]}]，这里interface{} 的类型有 []int, []string
	conditions []map[string]any
}

// NewDataIDFilter
func NewDataIDFilter(metric string) *DataIDFilter {
	dataIDFilter := &DataIDFilter{}

	// 如果metricName为空，则代表仅仅过滤condition
	if metric == "" {
		dataIDFilter.onlyCondition = true
		return dataIDFilter
	}
	dataIDList := GetMetricRouter().GetRouter(metric)
	if len(dataIDList) == 0 {
		return dataIDFilter
	}

	dataIDFilter.metricDataID = append(dataIDFilter.metricDataID, dataIDList...)
	return dataIDFilter
}

// FilterByBizIDs
func (d *DataIDFilter) FilterByBizIDs(bizIDs ...int) *DataIDFilter {
	if len(bizIDs) == 0 {
		return d
	}
	d.conditions = append(d.conditions, map[string]any{
		consul.BizID: bizIDs,
	})
	return d
}

// FilterByProjectIDs
func (d *DataIDFilter) FilterByProjectIDs(projectIDs ...string) *DataIDFilter {
	if len(projectIDs) == 0 {
		return d
	}
	d.conditions = append(d.conditions, map[string]any{
		consul.ProjectID: projectIDs,
	})
	return d
}

// FilterByClusterIDs
func (d *DataIDFilter) FilterByClusterIDs(clusterIDs ...string) *DataIDFilter {
	if len(clusterIDs) == 0 {
		return d
	}
	d.conditions = append(d.conditions, map[string]any{
		consul.ClusterID: clusterIDs,
	})
	return d
}

// Values 将metric过滤的dataIDList与biz，projectID等过滤的做交集
// 如果metric过滤出的结果为空，则以biz, projectID等过滤的结果为准
func (d *DataIDFilter) Values() []consul.DataID {
	// 如果不仅仅是过滤 mcondition 且metricName匹配为空，则unify-query不知道metric的所在db，直接返回空dataID列表
	if !d.onlyCondition && len(d.metricDataID) == 0 {
		return nil
	}

	// 根据bizID，projectID，clusterID过滤出dataID
	var tmpDataIDList []consul.DataID
	bizIDRouter := GetBizRouter()
	bcsInfo := consul.GetBcsInfo()
	for index, cond := range d.conditions {
		for key, filterIDs := range cond {
			var dataIDs []consul.DataID
			switch key {
			case consul.BizID:
				filterBizIDs, ok := filterIDs.([]int)
				if !ok {
					continue
				}
				dataIDs = bizIDRouter.GetRouter(filterBizIDs...)
			case consul.ProjectID:
				filterProjectIDs, ok := filterIDs.([]string)
				if !ok {
					continue
				}
				dataIDs = bcsInfo.GetRouterByProjectID(filterProjectIDs...)
			case consul.ClusterID:
				filterClusterIDs, ok := filterIDs.([]string)
				if !ok {
					continue
				}
				dataIDs = bcsInfo.GetRouterByClusterID(filterClusterIDs...)
			}

			if index == 0 {
				tmpDataIDList = dataIDs
				continue
			}
			tmpDataIDList = Intersection(tmpDataIDList, dataIDs)
		}
	}

	// 如果仅仅过滤条件，则直接返回过滤结果
	if d.onlyCondition {
		return tmpDataIDList
	}

	// 如果未过滤任何条件，则以metric过滤结果为准
	if len(d.conditions) == 0 {
		return d.metricDataID
	}

	return Intersection(d.metricDataID, tmpDataIDList)
}

// Intersection 取交集并去重
func Intersection(sli1, sli2 []consul.DataID) []consul.DataID {
	var (
		tmpMap    = make(map[consul.DataID]struct{}, len(sli1))
		resultMap = make(map[consul.DataID]struct{}, len(sli1))
		result    []consul.DataID
	)
	for _, item := range sli1 {
		tmpMap[item] = struct{}{}
	}

	for _, item := range sli2 {
		if _, ok := tmpMap[item]; ok {
			resultMap[item] = struct{}{}
		}
	}

	for item := range resultMap {
		result = append(result, item)
	}
	return result
}
