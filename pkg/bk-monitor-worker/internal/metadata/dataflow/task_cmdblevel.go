// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dataflow

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/slicex"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type CMDBPrepareAggregateTask struct {
	BaseTask
	AggInterval     int
	AggMethod       string
	MetricField     []string
	DimensionFields []string
}

func NewCMDBPrepareAggregateTask(rtId string, aggInterval int, aggMethod string, metricField, dimensionFields []string) *CMDBPrepareAggregateTask {
	for _, field := range CMDBHostMustHaveFields {
		if !slicex.IsExistItem(dimensionFields, field) {
			logger.Errorf("bk_target_ip && bk_target_cloud_id must in dimension fields")
			return nil
		}
	}
	t := &CMDBPrepareAggregateTask{MetricField: metricField, DimensionFields: dimensionFields}
	t.RtId = rtId

	cmdbHostTopSourceNode := NewRelationSourceNode(CMDBHostTopRtId)
	streamSourceNode := NewStreamSourceNode(rtId)
	// 将两张原始表的数据，做合并，维度信息补充，1对1
	fullProcessNode := NewCMDBPrepareAggregateFullNode(t.RtId, aggInterval, aggMethod, metricField, dimensionFields, "", "", "", []Node{cmdbHostTopSourceNode, streamSourceNode})
	fullStorageNode := CreateTSpiderOrDruidNode(fullProcessNode.OutputTableName(), TmpFullStorageNodeExpires, []Node{fullProcessNode})
	// 将补充的信息进行拆解， 1对多
	splitProcessNode := NewCMDBPrepareAggregateSplitNode(fullProcessNode.OutputTableName(), aggInterval, aggMethod, metricField, dimensionFields, "", "", "", []Node{fullProcessNode})
	splitStorageNode := CreateTSpiderOrDruidNode(splitProcessNode.OutputTableName(), config.GlobalBkdataDataExpiresDays, []Node{splitProcessNode})

	t.NodeList = []Node{streamSourceNode, cmdbHostTopSourceNode, fullProcessNode, fullStorageNode, splitProcessNode, splitStorageNode}
	t.Instance = t
	return t
}

func (t CMDBPrepareAggregateTask) FlowName() string {
	return fmt.Sprintf("CMDB预聚合 %s", t.RtId)
}
