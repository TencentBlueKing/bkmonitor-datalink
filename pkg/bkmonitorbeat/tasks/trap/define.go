// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trap

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
)

const (
	coldStart = iota
	warmStart
	linkDown
	linkUp
	authenticationFailure
	egpNeighborLoss
	enterpriseSpecific
	snmptrapOIDKey = "1.3.6.1.6.3.1.1.4.1.0"
	snmptrapOID    = "TrapOID"
	sysUptimeOid   = "1_3_6_1_2_1_1_3_0"

	// dimension list
	EventCommunityKey    = "community"
	EventVersionKey      = "version"
	EventEnterpriseKey   = "enterprise"
	EventGenericTrapKey  = "generic_trap"
	EventSpecificTrapKey = "specific_trap"
	EventSnmpTrapOIDKey  = "snmptrapoid"
	EventDisplayNameKey  = "display_name"
	EventAgentAddressKey = "agent_address"
	EventAggentPortKey   = "agent_port"
	EventServerIPKey     = "server_ip"
	EventServerPortKey   = "server_port"
	EventTimestampKey    = "timestamp"

	module = "trap_gather"

	unKownTrapVersion = 4

	allowCommunityEmptyKey = "emptyCommunity"
)

// New :
func New(globalConfig define.Config, taskConfig define.TaskConfig) define.Task {
	gather := &Gather{}
	gather.GlobalConfig = globalConfig
	gather.TaskConfig = taskConfig

	gather.Init()
	return gather
}
