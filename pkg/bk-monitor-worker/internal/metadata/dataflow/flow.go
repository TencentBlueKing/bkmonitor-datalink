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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/apiservice"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type DataFlow struct {
	FlowId        int
	FlowName      string
	ProjectId     int
	flowInfo      map[string]interface{}
	flowGraphInfo []map[string]interface{}
	IsModified    bool
	SqlChanged    bool
}

// FlowInfo 获取dataflow的状态信息
func (f DataFlow) FlowInfo() (map[string]interface{}, error) {
	resp, err := apiservice.Bkdata.GetDataFlow(f.FlowId)
	if err != nil {
		return nil, errors.Wrapf(err, "GetDataFlow with flow_id [%d] failed", f.FlowId)
	}
	return resp, nil
}

// FlowDeployInfo 获取dataflow的最近一次部署信息
func (f DataFlow) FlowDeployInfo(rebuild bool, projectId int) (map[string]interface{}, error) {
	/*
		1. 如果 f.FlowDeployInfo 为空，说明是no-start状态，需要start
		2. 如果 f.FlowDeployInfo["status"] 为success则该flow运行正常
		3. 如果 f.FlowDeployInfo["status"] 为failure则该flow运行异常
	*/
	resp, err := apiservice.Bkdata.GetLatestDeployDataFlow(f.FlowId)
	if err != nil {
		return nil, errors.Wrapf(err, "GetLatestDeployDataFlow with flow_id [%d] failed", err)
	}
	return resp, err
}

func (f DataFlow) FlowGraphInfo() ([]map[string]interface{}, error) {
	if f.flowGraphInfo == nil {
		resp, err := apiservice.Bkdata.GetDataFlowGraph(f.FlowId)
		if err != nil {
			return nil, errors.Wrapf(err, "GetDataFlowGraph with flow_id [%d] failed", f.FlowId)
		}
		f.flowGraphInfo, _ = resp["nodes"].([]map[string]interface{})
	}
	return f.flowGraphInfo, nil
}

func (f DataFlow) FlowStatus() string {
	status, _ := f.flowInfo["status"].(string)
	return status
}

// FromBkdataByFlowId 从bkdata接口查询到flow相关信息，然后初始化一个DataFlow对象返回
func (f DataFlow) FromBkdataByFlowId(flowId int) (*DataFlow, error) {
	resp, err := apiservice.Bkdata.GetDataFlow(flowId)
	if err != nil {
		return nil, errors.Wrapf(err, "GetDataFlow with flow_id [%d] failed", flowId)
	}
	flowName, _ := resp["flow_name"].(string)
	projectId, _ := resp["project_id"].(float64)
	return &DataFlow{
		FlowId:    flowId,
		FlowName:  flowName,
		ProjectId: int(projectId),
	}, nil
}

// FromBkdataByFlowName 从bkdata接口查询到flow相关信息，根据flow_name，然后初始化一个DataFlow对象返回
func (f DataFlow) FromBkdataByFlowName(flowName string, projectId int) (*DataFlow, error) {
	params := make(map[string]interface{})
	if projectId != 0 {
		params["project_id"] = projectId
	} else {
		params["project_id"] = config.GlobalBkdataProjectId
	}
	resp, err := apiservice.Bkdata.GetDataFlowList(params)
	if err != nil {
		return nil, errors.Wrapf(err, "GetDataFlowList with [%v] failed", params)
	}
	if len(resp) == 0 {
		return nil, errors.Errorf("data flow project_id [%v] not exists", params)
	}
	for _, flow := range resp {
		name, _ := flow["flow_name"].(string)
		if flowName == name {
			flowId, _ := flow["flow_id"].(float64)
			prjId, _ := flow["project_id"].(float64)

			return &DataFlow{
				FlowId:    int(flowId),
				FlowName:  flowName,
				ProjectId: int(prjId),
			}, nil
		}
	}
	return nil, errors.Errorf("data flow project_id [%v] not exists", params)
}

func (f DataFlow) CreateFlow(flowName string, projectId int) (*DataFlow, error) {
	params := make(map[string]interface{})
	if projectId != 0 {
		params["project_id"] = projectId
	} else {
		params["project_id"] = config.GlobalBkdataProjectId
	}
	resp, err := apiservice.Bkdata.CreateDataFlow(params)
	if err != nil {
		return nil, errors.Wrapf(err, "GetDataFlowList with [%v] failed", params)
	}
	flowId, _ := resp["flow_id"].(float64)
	prjId, _ := resp["project_id"].(float64)
	return &DataFlow{
		FlowId:    int(flowId),
		FlowName:  flowName,
		ProjectId: int(prjId),
	}, nil
}

// EnsureDataFlowExists 从bkdata接口查询到flow相关信息，根据flow_name，然后初始化一个DataFlow对象返回
func (f DataFlow) EnsureDataFlowExists(flowName string, rebuild bool, projectId int) (*DataFlow, error) {
	flow, err := func() (*DataFlow, error) {
		flow, err := f.FromBkdataByFlowName(flowName, projectId)
		if err != nil {
			return nil, err
		}
		if rebuild {
			rebuildFlow, err := flow.Rebuild()
			if err != nil {
				return nil, err
			}
			return rebuildFlow, nil
		}
		return flow, nil
	}()
	if err == nil {
		return flow, nil
	}
	return f.CreateFlow(flowName, projectId)
}

func (f DataFlow) StartOrRestartFlow(isStart bool, consumingMode string) bool {
	//TODO implement me
	panic("implement me")
}

func (f DataFlow) Start(consumingMode string) error {
	//TODO implement me
	panic("implement me")
}

func (f DataFlow) Stop() {
	resp, err := apiservice.Bkdata.StopDataFlow(f.FlowId)
	if err != nil {
		logger.Errorf("StopDataFlow with flow_id [%d] failed, %v", f.FlowId, err)
		return
	}
	logger.Infof("StopDataFlow with flow_id [%d] result [%v]", f.FlowId, resp)
}

func (f DataFlow) AddNode(node Node) error {
	graphInfos, err := f.FlowGraphInfo()
	if err != nil {
		return errors.Wrap(err, "get FlowGraphInfo failed")
	}
	for _, graphNode := range graphInfos {
		nodeConfig, _ := graphNode["node_config"].(map[string]interface{})
		// 判断是否为同样的节点(只判断关键信息，比如输入和输出表ID等信息)
		if node.GetNodeType() == graphNode["node_type"].(string) && node.Equal(nodeConfig) {
			nodeId := graphNode["node_id"].(int)
			// 如果部分信息不一样，则做一遍更新
			if node.NeedUpdate(nodeConfig) {
				if err := node.Update(f.FlowId, nodeId); err != nil {
					return err
				}
				f.IsModified = true
				f.SqlChanged = f.SqlChanged || node.NeedRestartFromTail(nodeConfig)
			}
			node.SetNodeId(nodeId)
			return nil
		}
	}
	if err := node.Create(f.FlowId); err != nil {
		return err
	}
	f.IsModified = true
	f.SqlChanged = f.SqlChanged || node.NeedRestartFromTail(nil)
	return nil
}

func (f DataFlow) Delete() error {
	//TODO implement me
	panic("implement me")
}

// Rebuild 重建flow
func (f DataFlow) Rebuild() (*DataFlow, error) {
	logger.Infof("rebuild dataflow([%s]%d)", f.FlowName, f.FlowId)
	if err := f.Delete(); err != nil {
		return nil, errors.Wrapf(err, "delete flow [%d] failed", f.FlowId)
	}
	logger.Infof("delete old dataflow([%s]%d) success", f.FlowName, f.FlowId)
	flow, err := f.CreateFlow(f.FlowName, 0)
	if err != nil {
		return nil, errors.Wrapf(err, "CreateFlow [%d] failed", f.FlowId)
	}
	logger.Infof("rebuild dataflow([%s]%d) success, new dataflow([%s]%d)", f.FlowName, f.FlowId, flow.FlowName, flow.FlowId)
	return flow, nil
}
