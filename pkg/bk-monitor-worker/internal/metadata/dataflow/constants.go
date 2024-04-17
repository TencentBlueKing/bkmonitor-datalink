// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dataflow

const (
	ConsumingModeHead    = "from_head" // 从最早(from_head)位置消费
	ConsumingModeTail    = "from_tail" // 从最新(from_tail)位置消费
	ConsumingModeCurrent = "continue"  // 从当前位置继续(continue), 不填默认continue
)

// dataflow状态
const (
	FlowStatusNoStart  = "no-start"
	FlowStatusRunning  = "running"
	FlowStatusStarting = "starting"
	FlowStatusFailure  = "failure"
	FlowStatusStopping = "stopping"
	FlowStatusWarning  = "warning"
)

const NodeDefaultFrontendOffset = 100 // dataflow画布node默认偏移量

const CMDBHostTopoRtId = "591_bkpub_cmdb_host_rels_split_innerip" // bkdata中cmdb_host_topo数据源表

var CMDBHostMustHaveFields = []string{"bk_target_ip", "bk_target_cloud_id"} // dataflow中CMDB task必须存在的dimension

const TmpFullStorageNodeExpires = 1 // dataflow 存储临时表过期时间（天）

var NodeDefaultFrontedInfo = []int{100, 100} // dataflow画布node默认位置坐标
