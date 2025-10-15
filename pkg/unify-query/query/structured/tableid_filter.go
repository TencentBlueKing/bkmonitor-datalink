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
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// TableIDFilter
type TableIDFilter struct {
	metric           string
	isAppointTableID bool
	values           []*Route
	dataIDList       []consul.DataID
}

// NewTableIDFilter
func NewTableIDFilter(
	metricName string, tableID TableID, dataIDList []consul.DataID, conditions Conditions,
) (*TableIDFilter, error) {
	// 找不到tableID一样需要返回一个实例，不然会报错
	tableIDFilter := &TableIDFilter{
		metric:           metricName,
		isAppointTableID: false,
		values:           make([]*Route, 0),
		dataIDList:       make([]consul.DataID, 0),
	}
	// 1. 解析tableID
	route, err := MakeRouteFromTableID(tableID)
	if err == nil {
		tableIDFilter.values = append(tableIDFilter.values, route)
		tableIDFilter.isAppointTableID = true
		return tableIDFilter, nil
	}
	if !errors.Is(err, ErrEmptyTableID) {
		return tableIDFilter, metadata.Sprintf(
			metadata.MsgQueryRouter,
			"table_id %s metric %s 路由解析",
			tableID, metricName,
		).Error(context.TODO(), err)
	}

	// 2. 如果tableID为空，则根据conditions获取bk_biz_id,bcs_cluster_id等，过滤出tableID
	// 如果传了tableID，则可以找到唯一的库表，不需要再用dataID遍历

	// 进行查询时，需要找出bk_biz_id, bk_project_id, cluster_id
	bizIDs, projectIDs, clusterIDs, err := conditions.GetRequiredFiled()
	if err != nil {
		return tableIDFilter, metadata.Sprintf(
			metadata.MsgQueryRouter,
			"table_id %s metric %s 路由解析",
			tableID, metricName,
		).Error(context.TODO(), err)
	}

	// 必传biz_id
	if len(bizIDs) == 0 {
		return nil, fmt.Errorf("bk_biz_id required")
	}

	// 添加公共data_id（公共data_id在0号业务下）
	bizIDs = append(bizIDs, 0)
	log.Debugf(context.TODO(),
		"field:[%s] filter: biz_ids:[%v], project_ids:[%v], cluster_ids:[%v]",
		metricName, bizIDs, projectIDs, clusterIDs,
	)
	resultDataIDList := influxdb.NewDataIDFilter(metricName).FilterByBizIDs(bizIDs...).FilterByProjectIDs(projectIDs...).
		FilterByClusterIDs(clusterIDs...).Values()
	if len(dataIDList) != 0 {
		// 将用户指定的dataIDList和过滤得到的做交集
		if len(resultDataIDList) == 0 {
			resultDataIDList = dataIDList
		} else {
			resultDataIDList = influxdb.Intersection(dataIDList, resultDataIDList)
		}
	}
	// DataID 查询为空不影响查询后续流程
	if len(resultDataIDList) == 0 {
		metadata.Sprintf(
			metadata.MsgQueryRouter,
			"table_id %s metric %s 路由获取为空",
			tableID, metricName,
		).Warn(context.TODO())

		return tableIDFilter, nil
	}
	tableIDFilter.dataIDList = resultDataIDList
	return tableIDFilter, nil
}

// GetRoutes 获取路由
func (t *TableIDFilter) GetRoutes() []*Route {
	// 如果是通过NewTableIDFilter新建的tableIDFilter，则必不可能返回空的route
	if len(t.values) == 0 && len(t.dataIDList) == 0 {
		return nil
	}

	// 解析单独的tableID获取到的route
	if len(t.values) != 0 {
		return t.values
	}

	tableIDs := influxdb.GetTsDBRouter().GetTableIDs(t.dataIDList...)

	result := make([]*Route, 0, len(t.values))
	for _, tableID := range tableIDs {
		var route *Route
		route.SetClusterID(tableID.ClusterID)
		if tableID.IsSplit() {
			route = MakeRouteByDBTable(tableID.DB, t.metric)
			route.SetMetricName(t.metric)
		} else {
			route = MakeRouteByDBTable(tableID.DB, tableID.Measurement)
			route.SetMetricName(t.metric)
		}
		result = append(result, route)
	}

	return result
}

// DataIDList
func (t *TableIDFilter) DataIDList() []consul.DataID {
	return t.dataIDList
}

// IsAppointTableID: 是否是根据table_id解析而来的
func (t *TableIDFilter) IsAppointTableID() bool {
	return t.isAppointTableID
}
