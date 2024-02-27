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
)

type FilterUnknownTimeTask struct {
	BaseTask
	MetricField     []string
	DimensionFields []string
}

func NewFilterUnknownTimeTask(rtId string, metricField, dimensionFields []string) *FilterUnknownTimeTask {
	t := &FilterUnknownTimeTask{MetricField: metricField, DimensionFields: dimensionFields}
	t.RtId = rtId
	streamSourceNode := NewStreamSourceNode(rtId)
	processNode := NewFilterUnknownTimeNode(streamSourceNode.OutputTableName(), 0, "", metricField, dimensionFields, "", "", "", []Node{streamSourceNode})
	storageNode := CreateTSpiderOrDruidNode(processNode.OutputTableName(), config.BkdataDataExpiresDays, []Node{processNode})
	t.NodeList = []Node{streamSourceNode, processNode, storageNode}
	t.Instance = t
	return t
}

func (t FilterUnknownTimeTask) FlowName() string {
	return fmt.Sprintf("过滤无效时间 %s", t.RtId)
}
