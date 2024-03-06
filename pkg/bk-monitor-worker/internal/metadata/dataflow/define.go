// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dataflow

type Task interface {
	FlowName() string
	CreateFlow(rebuild bool, projectId int) error
	StartFlow(consumingMode string) error
}

type Node interface {
	Name() string
	FrontendInfo() map[string]int
	Config() map[string]interface{}
	NeedUpdate(map[string]interface{}) bool
	NeedRestartFromTail(map[string]interface{}) bool
	GetNodeType() string
	GetApiParams(flowId int) map[string]interface{}
	Update(flowId, NodeId int) error
	Create(flowId int) error
	Equal(map[string]interface{}) bool
	SetNodeId(nodeId int)
	GetNodeId() int
	TableName() string
}
