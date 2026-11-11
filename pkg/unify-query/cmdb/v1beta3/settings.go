// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

const (
	MaxHopsConfigPath                       = "cmdb.v1beta3.max_hops"
	MaxAllowedHopsConfigPath                = "cmdb.v1beta3.max_allowed_hops"
	MaxRangePointsConfigPath                = "cmdb.v1beta3.max_range_points"
	MaxSharedTopologyPointsConfigPath       = "cmdb.v1beta3.max_shared_topology_points"
	MaxSharedTopologyRequestBytesConfigPath = "cmdb.v1beta3.max_shared_topology_request_bytes"
	MaxSharedTopologyQueriesConfigPath      = "cmdb.v1beta3.max_shared_topology_queries"
	SharedTopologyReuseMatrixConfigPath     = "cmdb.v1beta3.shared_topology_reuse_matrix"
	SharedTopologyPlanCandidatesConfigPath  = "cmdb.v1beta3.shared_topology_plan_candidates"
	MaxSharedTopologyConcurrentConfigPath   = "cmdb.v1beta3.max_shared_topology_concurrent_requests"
	DefaultLookBackDeltaConfigPath          = "cmdb.v1beta3.look_back_delta"
)

// 配置只约束请求的原始输入（跳数、点数、请求体、批次条数）和运维比程序更清楚的
// 部署参数（并发执行槽、开关）。构图、取数和输出的内部规模不是拒绝理由：
// 工作集由分批、分段和流式编码约束，容量按规格表配置实例资源。
var (
	DefaultMaxHops                = 2
	MaxAllowedHops                = 6
	MaxRangePoints                = 11000
	MaxSharedTopologyPoints       = 60
	MaxSharedTopologyRequestBytes = 1024 * 1024
	MaxSharedTopologyQueries      = 16
	SharedTopologyReuseMatrix     = true
	SharedTopologyPlanCandidates  = true
	MaxSharedTopologyConcurrent   = 8
	DefaultLookBackDelta          = int64(86400000) // 24小时（毫秒）
)

func effectiveMaxRangePoints() int {
	return effectiveTimeGraphLimit(MaxRangePoints, 11000)
}

func effectiveMaxSharedTopologyPoints() int {
	if MaxSharedTopologyPoints > 0 && MaxSharedTopologyPoints <= 60 {
		return MaxSharedTopologyPoints
	}
	return 60
}

func effectiveTimeGraphLimit(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func TopologyRequestByteLimit() int {
	return effectiveTimeGraphLimit(MaxSharedTopologyRequestBytes, 1024*1024)
}

func TopologyQueryLimit() int { return effectiveTimeGraphLimit(MaxSharedTopologyQueries, 16) }
