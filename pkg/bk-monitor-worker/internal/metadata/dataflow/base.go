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
	"reflect"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkdata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type BaseTask struct {
	DataFlow   *DataFlow
	NodeList   []Node
	FlowStatus string
	RtId       string
	Instance   Task
}

// FlowName flow name
func (b *BaseTask) FlowName() string {
	return ""
}

// CreateFlow 尝试创建flow,如果已经存在，则获取到整个flow的相关信息，包括node的信息,一个个比对，
// 如果有差异，则进行更新动作,如果不存在，则直接创建,对于节点的创建也是同样的逻辑，先看是否存在，存在则更新，不存在则创建之
func (b *BaseTask) CreateFlow(rebuild bool, projectId int) error {
	var err error
	// 创建任务
	b.DataFlow, err = DataFlow{}.EnsureDataFlowExists(b.Instance.FlowName(), rebuild, projectId)
	if err != nil {
		return err
	}
	if b.DataFlow == nil {
		return errors.New("DataFlowCreateFailed")
	}
	// 创建任务下的节点
	// 需按node_list的顺序来创建
	for _, node := range b.NodeList {
		if err := b.DataFlow.AddNode(node); err != nil {
			return err
		}
	}
	return nil
}

func (b *BaseTask) StartFlow(consumingMode string) error {
	if b.DataFlow != nil {
		if consumingMode == "" && b.DataFlow.SqlChanged {
			consumingMode = ConsumingModeTail
		}
		if err := b.DataFlow.Start(consumingMode); err != nil {
			return err
		}
		b.FlowStatus = b.DataFlow.FlowStatus()
	}
	return nil
}

type BaseNode struct {
	ParentList []Node
	nodeId     int
	NodeType   string
	Instance   Node
}

// NewBaseNode new BaseNode
func NewBaseNode(parentList []Node) *BaseNode {
	return &BaseNode{ParentList: parentList}
}

// Equal 判断节点是否相同
func (b *BaseNode) Equal(other map[string]interface{}) bool {
	equal, _ := jsonx.CompareObjects(b.Instance.Config(), other)
	return equal
}

// Name 节点名
func (b *BaseNode) Name() string {
	return reflect.TypeOf(b).Name()
}

// FrontendInfo 获取画布位置信息
func (b *BaseNode) FrontendInfo() map[string]int {
	if len(b.ParentList) != 0 {
		firstParent := b.ParentList[0]
		return map[string]int{
			"x": firstParent.FrontendInfo()["x"] + NodeDefaultFrontendOffset,
			"y": firstParent.FrontendInfo()["y"] + NodeDefaultFrontendOffset,
		}
	}
	return map[string]int{
		"x": NodeDefaultFrontedInfo[0],
		"y": NodeDefaultFrontedInfo[1],
	}
}

// Config 获取node配置
func (b *BaseNode) Config() map[string]interface{} {
	return map[string]interface{}{}
}

// NeedUpdate 根据config判断是否需要更新
func (b *BaseNode) NeedUpdate(otherConfig map[string]interface{}) bool {
	for k, v := range b.Instance.Config() {
		otherV := otherConfig[k]
		if equal, _ := jsonx.CompareObjects(v, otherV); !equal {
			return true
		}
	}
	return false
}

// NeedRestartFromTail 判定 flow 重启，是否需要从尾部直接开始
func (b *BaseNode) NeedRestartFromTail(otherConfig map[string]interface{}) bool {
	// 表结构变更后，历史数据里没有这个字段，会导致任务执行异常。上游新增字段后，如果下游任务使用到这个字段，最好重启任务时选择从尾部处理
	sqlConfig, ok := b.Instance.Config()["sql"]
	if !ok {
		return false
	}
	if otherConfig == nil {
		// 无 other_config表示新增节点
		return true
	}
	otherSqlConfig, _ := otherConfig["sql"]
	equal, _ := jsonx.CompareObjects(sqlConfig, otherSqlConfig)
	return !equal
}

// GetNodeType 获取node类型
func (b *BaseNode) GetNodeType() string {
	return b.NodeType
}

// GetApiParams 获取api所需参数
func (b *BaseNode) GetApiParams(flowId int) map[string]interface{} {
	fromLinks := make([]map[string]interface{}, 0)
	for _, p := range b.ParentList {
		fromLinks = append(fromLinks, map[string]interface{}{
			"source": map[string]interface{}{
				"node_id": p.GetNodeId(),
				"id":      fmt.Sprintf("ch_%v", p.GetNodeId()),
				"arrow":   "Right",
			},
			"target": map[string]interface{}{
				"id":    fmt.Sprintf("bk_node_%d", time.Now().UnixMilli()),
				"arrow": "Left",
			},
		})
	}
	return map[string]interface{}{
		"flow_id":       flowId,
		"from_links":    fromLinks,
		"node_type":     b.Instance.GetNodeType(),
		"config":        b.Instance.Config(),
		"frontend_info": b.Instance.FrontendInfo(),
	}
}

// Update 更新node
func (b *BaseNode) Update(flowId, nodeId int) error {
	apiParams := b.Instance.GetApiParams(flowId)
	var params bkdata.UpdateDataFlowNodeParams
	params.NodeId = nodeId
	params.FromLinks, _ = apiParams["from_links"].([]map[string]interface{})
	params.NodeType, _ = apiParams["node_type"].(string)
	params.Config, _ = apiParams["config"].(map[string]interface{})
	params.FrontendInfo, _ = apiParams["frontend_info"].(map[string]int)
	resp, err := apiservice.Bkdata.UpdateDataFlowNode(flowId, params)
	if err != nil {
		return errors.Wrapf(err, "update node [%s] to flow [%d] failed", b.Name(), flowId)
	}
	b.nodeId = nodeId
	logger.Infof("update node [%s] to flow [%d] success, result [%v]", b.Name(), flowId, resp)
	return nil
}

func (b *BaseNode) Create(flowId int) error {
	apiParams := b.Instance.GetApiParams(flowId)
	var params bkdata.DataFlowNodeParams
	params.FromLinks, _ = apiParams["from_links"].([]map[string]interface{})
	params.NodeType, _ = apiParams["node_type"].(string)
	params.Config, _ = apiParams["config"].(map[string]interface{})
	params.FrontendInfo, _ = apiParams["frontend_info"].(map[string]int)
	resp, err := apiservice.Bkdata.AddDataFlowNode(flowId, params)
	if err != nil {
		return errors.Wrapf(err, "add node [%s] to flow [%d] failed", b.Instance.Name(), flowId)
	}
	nodeId, _ := resp["node_id"].(float64)
	b.nodeId = int(nodeId)
	logger.Infof("add node [%s] to flow [%d] success, result [%v]", b.Instance.Name(), flowId, resp)
	return nil
}

// SetNodeId 配置节点ID
func (b *BaseNode) SetNodeId(nodeId int) {
	b.nodeId = nodeId
}

// GetNodeId 获取节点ID
func (b *BaseNode) GetNodeId() int {
	return b.nodeId
}

// TableName 获取表名
func (b *BaseNode) TableName() string {
	return ""
}
