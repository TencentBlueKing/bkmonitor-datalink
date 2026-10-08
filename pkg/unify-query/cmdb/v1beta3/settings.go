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
	MaxHopsConfigPath                          = "cmdb.v1beta3.max_hops"
	MaxAllowedHopsConfigPath                   = "cmdb.v1beta3.max_allowed_hops"
	DefaultLimitConfigPath                     = "cmdb.v1beta3.default_limit"
	MaxRangePointsConfigPath                   = "cmdb.v1beta3.max_range_points"
	MaxTargetsConfigPath                       = "cmdb.v1beta3.max_targets"
	MaxGraphNodesConfigPath                    = "cmdb.v1beta3.max_graph_nodes"
	MaxGraphEdgesConfigPath                    = "cmdb.v1beta3.max_graph_edges"
	MaxGraphResultsConfigPath                  = "cmdb.v1beta3.max_graph_results"
	MaxGraphNodeInfosConfigPath                = "cmdb.v1beta3.max_graph_node_infos"
	MaxSharedTopologyPointsConfigPath          = "cmdb.v1beta3.max_shared_topology_points"
	MaxSharedTopologyBackendBytesConfigPath    = "cmdb.v1beta3.max_shared_topology_backend_bytes"
	MaxSharedTopologyMatrixPointsConfigPath    = "cmdb.v1beta3.max_shared_topology_matrix_points"
	MaxSharedTopologyOutputElementsConfigPath  = "cmdb.v1beta3.max_shared_topology_output_elements"
	MaxSharedTopologyOutputBytesConfigPath     = "cmdb.v1beta3.max_shared_topology_output_bytes"
	MaxSharedTopologyRequestBytesConfigPath    = "cmdb.v1beta3.max_shared_topology_request_bytes"
	MaxSharedTopologyQueriesConfigPath         = "cmdb.v1beta3.max_shared_topology_queries"
	YoloModeConfigPath                         = "cmdb.v1beta3.yolo_mode"
	SharedTopologyReuseMatrixConfigPath        = "cmdb.v1beta3.shared_topology_reuse_matrix"
	SharedTopologyPlanCandidatesConfigPath     = "cmdb.v1beta3.shared_topology_plan_candidates"
	MaxSharedTopologyConcurrentConfigPath      = "cmdb.v1beta3.max_shared_topology_concurrent_requests"
	MaxSharedTopologyReservedBytesConfigPath   = "cmdb.v1beta3.max_shared_topology_reserved_bytes"
	SharedTopologyRequestMemoryBytesConfigPath = "cmdb.v1beta3.shared_topology_request_memory_bytes"
	SharedTopologyMemoryHeadroomConfigPath     = "cmdb.v1beta3.shared_topology_memory_headroom_bytes"
	DefaultLookBackDeltaConfigPath             = "cmdb.v1beta3.look_back_delta"
)

var (
	DefaultMaxHops                   = 2
	MaxAllowedHops                   = 5
	DefaultLimit                     = 100
	MaxRangePoints                   = 11000
	MaxTargets                       = 5000
	MaxGraphNodes                    = 100000
	MaxGraphEdges                    = 200000
	MaxGraphResults                  = 10000
	MaxGraphNodeInfos                = 1000000
	MaxSharedTopologyPoints          = 60
	MaxSharedTopologyBackendBytes    = 16 * 1024 * 1024
	MaxSharedTopologyMatrixPoints    = 1000000
	MaxSharedTopologyOutputElements  = 200000
	MaxSharedTopologyOutputBytes     = 64 * 1024 * 1024
	MaxSharedTopologyRequestBytes    = 1024 * 1024
	MaxSharedTopologyQueries         = 16
	SharedTopologyReuseMatrix        = true
	SharedTopologyPlanCandidates     = true
	MaxSharedTopologyConcurrent      = 8
	MaxSharedTopologyReservedBytes   = 4 * 1024 * 1024 * 1024
	SharedTopologyRequestMemoryBytes = 512 * 1024 * 1024
	SharedTopologyMemoryHeadroom     = 512 * 1024 * 1024
	DefaultLookBackDelta             = int64(86400000) // 24小时（毫秒）

	// yoloMode is an explicit capacity-test switch. It defaults to false and
	// can be enabled for an isolated test Pod through configuration.
	yoloMode bool
)

func effectiveMaxRangePoints() int {
	return effectiveTimeGraphLimit(MaxRangePoints, 11000)
}

func effectiveMaxTargets() int {
	return effectiveTimeGraphLimit(MaxTargets, 5000)
}

func effectiveMaxGraphNodes() int {
	return effectiveTimeGraphLimit(MaxGraphNodes, 100000)
}

func effectiveMaxGraphEdges() int {
	return effectiveTimeGraphLimit(MaxGraphEdges, 200000)
}

func effectiveMaxGraphResults() int {
	return effectiveTimeGraphLimit(MaxGraphResults, 10000)
}

func effectiveMaxGraphNodeInfos() int {
	return effectiveTimeGraphLimit(MaxGraphNodeInfos, 1000000)
}

func effectiveMaxSharedTopologyPoints() int {
	if yoloMode {
		return 0
	}
	if MaxSharedTopologyPoints > 0 && MaxSharedTopologyPoints <= 60 {
		return MaxSharedTopologyPoints
	}
	return 60
}

func effectiveTimeGraphLimit(value, fallback int) int {
	if yoloMode {
		return 0
	}
	if value > 0 {
		return value
	}
	return fallback
}

func TopologyRequestByteLimit() int {
	return effectiveTimeGraphLimit(MaxSharedTopologyRequestBytes, 1024*1024)
}

func TopologyQueryLimit() int { return effectiveTimeGraphLimit(MaxSharedTopologyQueries, 16) }
