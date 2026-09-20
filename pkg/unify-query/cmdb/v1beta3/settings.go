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
	MaxHopsConfigPath              = "cmdb.v1beta3.max_hops"
	MaxAllowedHopsConfigPath       = "cmdb.v1beta3.max_allowed_hops"
	DefaultLimitConfigPath         = "cmdb.v1beta3.default_limit"
	MaxRangePointsConfigPath       = "cmdb.v1beta3.max_range_points"
	MaxTargetsConfigPath           = "cmdb.v1beta3.max_targets"
	MaxGraphNodesConfigPath        = "cmdb.v1beta3.max_graph_nodes"
	MaxGraphEdgesConfigPath        = "cmdb.v1beta3.max_graph_edges"
	MaxGraphResultsConfigPath      = "cmdb.v1beta3.max_graph_results"
	MaxGraphNodeInfosConfigPath    = "cmdb.v1beta3.max_graph_node_infos"
	DefaultLookBackDeltaConfigPath = "cmdb.v1beta3.look_back_delta"
)

var (
	DefaultMaxHops       = 2
	MaxAllowedHops       = 5
	DefaultLimit         = 100
	MaxRangePoints       = 11000
	MaxTargets           = 5000
	MaxGraphNodes        = 100000
	MaxGraphEdges        = 200000
	MaxGraphResults      = 10000
	MaxGraphNodeInfos    = 1000000
	DefaultLookBackDelta = int64(86400000) // 24小时（毫秒）
)

func effectiveMaxRangePoints() int {
	if MaxRangePoints > 0 {
		return MaxRangePoints
	}
	return 11000
}

func effectiveMaxTargets() int {
	if MaxTargets > 0 {
		return MaxTargets
	}
	return 5000
}

func effectiveMaxGraphNodes() int {
	if MaxGraphNodes > 0 {
		return MaxGraphNodes
	}
	return 100000
}

func effectiveMaxGraphEdges() int {
	if MaxGraphEdges > 0 {
		return MaxGraphEdges
	}
	return 200000
}

func effectiveMaxGraphResults() int {
	if MaxGraphResults > 0 {
		return MaxGraphResults
	}
	return 10000
}

func effectiveMaxGraphNodeInfos() int {
	if MaxGraphNodeInfos > 0 {
		return MaxGraphNodeInfos
	}
	return 1000000
}
