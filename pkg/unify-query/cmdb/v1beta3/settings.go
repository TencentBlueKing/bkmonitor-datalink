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
	MaxHopsConfigPath                    = "cmdb.v1beta3.max_hops"
	MaxAllowedHopsConfigPath             = "cmdb.v1beta3.max_allowed_hops"
	DefaultLimitConfigPath               = "cmdb.v1beta3.default_limit"
	MaxRangePointsConfigPath             = "cmdb.v1beta3.max_range_points"
	MaxEdgesPerHopConfigPath             = "cmdb.v1beta3.max_edges_per_hop"
	MaxTargetsConfigPath                 = "cmdb.v1beta3.max_targets"
	MaxResponseBytesConfigPath           = "cmdb.v1beta3.max_response_bytes"
	RootRecordIDEnabledConfigPath        = "cmdb.v1beta3.root_record_id.enabled"
	DefaultLookBackDeltaConfigPath       = "cmdb.v1beta3.look_back_delta"
	ActiveEdgeServingRelationsConfigPath = "cmdb.v1beta3.active_edge_serving.relations"
)

var (
	DefaultMaxHops = 2
	MaxAllowedHops = 5
	DefaultLimit   = 100
	MaxRangePoints = 11000
	// MaxEdgesPerHop 限制单个节点在每一跳可展开的关系边数量。
	MaxEdgesPerHop = 1000
	// MaxTargets 限制单个时间点可返回的目标数量。
	MaxTargets = 5000
	// MaxResponseBytes 限制 BKBase 查询响应体大小，防止超大响应占用过多内存。
	MaxResponseBytes = 10 * 1024 * 1024
	// RootRecordIDEnabled 控制是否使用完整主键生成 Record ID 定点查询根资源。
	RootRecordIDEnabled        = false
	DefaultLookBackDelta       = int64(86400000) // 24小时（毫秒）
	ActiveEdgeServingRelations = []string{}
)

// effectiveMaxEdgesPerHop 返回单个节点每跳允许展开的最大边数，并为非法配置提供安全默认值。
func effectiveMaxEdgesPerHop() int {
	if MaxEdgesPerHop > 0 {
		return MaxEdgesPerHop
	}
	return 1000
}

// maxEdgesPerHopQueryLimit 在配置上限之外额外查询一条边，使解析器能够明确识别结果超限，
// 避免把被静默截断的不完整关系数据当作正常结果返回。
func maxEdgesPerHopQueryLimit() int {
	limit := effectiveMaxEdgesPerHop()
	if limit == int(^uint(0)>>1) {
		return limit
	}
	return limit + 1
}

// effectiveMaxTargets 返回单次查询允许返回的最大目标数，并为非法配置提供安全默认值。
func effectiveMaxTargets() int {
	if MaxTargets > 0 {
		return MaxTargets
	}
	return 5000
}

// effectiveMaxResponseBytes 返回 BKBase 响应体大小上限，并为非法配置提供安全默认值。
func effectiveMaxResponseBytes() int {
	if MaxResponseBytes > 0 {
		return MaxResponseBytes
	}
	return 10 * 1024 * 1024
}
