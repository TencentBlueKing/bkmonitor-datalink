// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

// QueryRequest 关联查询请求
type QueryRequest struct {
	Timestamp                int64              `json:"timestamp"`                            // 查询时间点（毫秒时间戳）
	SourceType               ResourceType       `json:"source_type"`                          // 源资源类型
	SourceInfo               map[string]string  `json:"source_info"`                          // 源资源过滤条件
	TargetType               ResourceType       `json:"target_type,omitempty"`                // 目标资源类型（可选，用于定向查询）
	PathResource             []ResourceType     `json:"path_resource,omitempty"`              // 路径约束资源类型
	MaxHops                  int                `json:"max_hops,omitempty"`                   // 最大跳数（默认2，范围1-5）
	AllowedRelationTypes     []RelationCategory `json:"allowed_relation_types,omitempty"`     // 允许的关系类别
	DynamicRelationDirection TraversalDirection `json:"dynamic_relation_direction,omitempty"` // 动态关系方向（默认both）
	LookBackDelta            int64              `json:"look_back_delta,omitempty"`            // 回溯时间窗口（毫秒，默认86400000）
	Limit                    int                `json:"limit,omitempty"`                      // 返回的Root数量限制（默认100）
}

// Normalize 规范化请求参数，填充默认值
func (r *QueryRequest) Normalize() {
	if r.MaxHops <= 0 {
		r.MaxHops = DefaultMaxHops
	}
	if r.MaxHops > MaxAllowedHops {
		r.MaxHops = MaxAllowedHops
	}
	if r.Limit <= 0 {
		r.Limit = DefaultLimit
	}
	if r.LookBackDelta <= 0 {
		r.LookBackDelta = DefaultLookBackDelta
	}
	if r.DynamicRelationDirection == "" {
		r.DynamicRelationDirection = DirectionBoth
	}
	if len(r.AllowedRelationTypes) == 0 {
		r.AllowedRelationTypes = []RelationCategory{RelationCategoryStatic, RelationCategoryDynamic}
	}
}

// GetQueryRange 获取查询时间范围
func (r *QueryRequest) GetQueryRange() (start, end int64) {
	end = r.Timestamp
	start = r.Timestamp - r.LookBackDelta
	if start < 0 {
		start = 0
	}
	return start, end
}

// GetSourceResourceID 获取源资源ID
func (r *QueryRequest) GetSourceResourceID() string {
	return GenerateResourceID(r.SourceType, r.SourceInfo)
}

// IsRelationCategoryAllowed 检查关系类别是否允许
func (r *QueryRequest) IsRelationCategoryAllowed(category RelationCategory) bool {
	if len(r.AllowedRelationTypes) == 0 {
		return true // 默认允许所有
	}
	for _, allowed := range r.AllowedRelationTypes {
		if allowed == category {
			return true
		}
	}
	return false
}
