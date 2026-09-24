// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdb

// Index 实例关键维度
type Index []string

// Matcher 维度映射
type Matcher map[string]string

// Matchers 多组维度映射
type Matchers []Matcher

// MatchersWithTimestamp 带时间的多维度映射
type MatchersWithTimestamp struct {
	Timestamp int64     `json:"timestamp"`
	Matchers  []Matcher `json:"items"`
}

// Resource 资源
type Resource string

// Relation 两点关联路径
type Relation struct {
	V            []Resource
	RelationType string `json:"relation_type,omitempty"`
	MetricName   string `json:"metric_name,omitempty"`
	Category     string `json:"category,omitempty"`
	Direction    string `json:"direction,omitempty"`
}

// RelationPathStep describes one resource hop and the relation schema that
// produced it. The relation metadata is optional for legacy callers that
// only provide resource-type paths.
type RelationPathStep struct {
	ResourceType Resource `json:"resource_type"`
	RelationType string   `json:"relation_type,omitempty"`
	Category     string   `json:"category,omitempty"`
	Direction    string   `json:"direction,omitempty"`
	MetricName   string   `json:"metric_name,omitempty"`
}

// RelationPath is a planned source-to-target path with relation metadata for
// each hop.
type RelationPath struct {
	Steps []RelationPathStep `json:"steps"`
}

// Path 关联路径 (v1)
type Path []Relation

// Paths 多组关联路径
type Paths []Path

// PathNode describes a resource on a relation path together with its dimensions.
type PathNode struct {
	ResourceType Resource `json:"resource_type"`
	Dimensions   Matcher  `json:"dimensions"`
}

// PathResourcesResult contains one complete source-to-target path at a timestamp.
type PathResourcesResult struct {
	Timestamp  int64      `json:"timestamp"`
	TargetType Resource   `json:"target_type"`
	Path       []PathNode `json:"path"`
}

// SharedTopologyQuery 描述一次 instant 或 range 完整局部拓扑查询。它与旧路径
// 请求分离：目标类型只过滤返回节点，遍历仍使用完整的候选关系图。
// 时间支持秒或整秒对应的毫秒格式；VM 协议仅支持整数秒，step 也必须是正整秒。
type SharedTopologyQuery struct {
	SpaceUID                 string     `json:"-"`
	Timestamp                int64      `json:"timestamp,omitempty"`
	StartTime                int64      `json:"start_time,omitempty"`
	EndTime                  int64      `json:"end_time,omitempty"`
	Step                     string     `json:"step,omitempty"`
	SourceType               Resource   `json:"source_type"`
	SourceInfo               Matcher    `json:"source_info,omitempty"`
	TargetTypes              []Resource `json:"target_types,omitempty"`
	MaxHops                  int        `json:"max_hops,omitempty"`
	AllowedCategories        []string   `json:"allowed_categories,omitempty"`
	AllowedRelationTypes     []string   `json:"allowed_relation_types,omitempty"`
	DynamicRelationDirection string     `json:"dynamic_relation_direction,omitempty"`
	LookBackDelta            string     `json:"look_back_delta,omitempty"`
}

type SharedTopologyRequest struct {
	QueryList []SharedTopologyQuery `json:"query_list"`
}

// SharedTopologyResult 是模型层返回的规范化拓扑结果。
type SharedTopologyResult struct {
	StartTime  int64
	EndTime    int64
	Step       string
	PointCount int
	Snapshots  []SharedTopologySnapshot
}

// SharedTopologyNode 是响应中的节点身份和维度信息。
type SharedTopologyNode struct {
	ID           uint64   `json:"id"`
	ResourceType Resource `json:"resource_type"`
	Dimensions   Matcher  `json:"dimensions"`
}

// SharedTopologyEdge 是响应中的关系身份和端点。
type SharedTopologyEdge struct {
	Source       uint64 `json:"source"`
	Target       uint64 `json:"target"`
	RelationType string `json:"relation_type"`
	MetricName   string `json:"metric_name"`
	Category     string `json:"category"`
	Direction    string `json:"direction"`
}

// SharedTopologySnapshot 是一个评估时间点的诱导子图。
type SharedTopologySnapshot struct {
	Timestamp     int64                `json:"timestamp"`
	Nodes         []SharedTopologyNode `json:"nodes"`
	Edges         []SharedTopologyEdge `json:"edges"`
	Partial       bool                 `json:"partial"`
	PartialReason string               `json:"partial_reason,omitempty"`
}

// SharedTopologyResponseData 是一个 query_list 项的 HTTP 响应。
type SharedTopologyResponseData struct {
	Code       int                      `json:"code"`
	StartTime  int64                    `json:"start_time"`
	EndTime    int64                    `json:"end_time"`
	Step       string                   `json:"step"`
	PointCount int                      `json:"point_count"`
	Snapshots  []SharedTopologySnapshot `json:"snapshots"`
	Message    string                   `json:"message,omitempty"`
}

// SharedTopologyResponse 是共享拓扑 HTTP 响应。
type SharedTopologyResponse struct {
	TraceID string                       `json:"trace_id"`
	Data    []SharedTopologyResponseData `json:"data"`
}

// RelationPathResourcesRequest queries resource paths at one timestamp.
type RelationPathResourcesRequest struct {
	QueryList []struct {
		Timestamp     int64        `json:"timestamp"`
		SourceType    Resource     `json:"source_type,omitempty"`
		TargetTypes   []Resource   `json:"target_types,omitempty"`
		PathResources [][]Resource `json:"path_resources,omitempty"`
		Matcher       Matcher      `json:"matcher,omitempty"`
		LookBackDelta string       `json:"look_back_delta,omitempty"`
	} `json:"query_list"`
}

type RelationPathResourcesResponseData struct {
	Code    int                   `json:"code"`
	Results []PathResourcesResult `json:"results"`
	Message string                `json:"message"`
}

type RelationPathResourcesResponse struct {
	TraceID string                              `json:"trace_id"`
	Data    []RelationPathResourcesResponseData `json:"data"`
}

// RelationPathResourcesRangeRequest queries resource paths over a time range.
type RelationPathResourcesRangeRequest struct {
	QueryList []struct {
		StartTs       int64        `json:"start_time"`
		EndTs         int64        `json:"end_time"`
		Step          string       `json:"step"`
		SourceType    Resource     `json:"source_type,omitempty"`
		TargetTypes   []Resource   `json:"target_types,omitempty"`
		PathResources [][]Resource `json:"path_resources,omitempty"`
		Matcher       Matcher      `json:"matcher,omitempty"`
		LookBackDelta string       `json:"look_back_delta,omitempty"`
	} `json:"query_list"`
}

type RelationPathResourcesRangeResponseData struct {
	Code    int                   `json:"code"`
	Results []PathResourcesResult `json:"results"`
	Message string                `json:"message"`
}

type RelationPathResourcesRangeResponse struct {
	TraceID string                                   `json:"trace_id"`
	Data    []RelationPathResourcesRangeResponseData `json:"data"`
}

// RelationMultiResourcePathData 描述一条静态关系路径及其查询结果。
// 该结构用于可选的多路径查询，不影响旧版 path/target_list 字段。
type RelationMultiResourcePathData struct {
	Path       []string `json:"path"`
	TargetList Matchers `json:"target_list"`
}

// RelationMultiResourceRangePathData 描述一条静态关系路径及其范围查询结果。
type RelationMultiResourceRangePathData struct {
	Path       []string                `json:"path"`
	TargetList []MatchersWithTimestamp `json:"target_list"`
}

// RelationMultiResourceRequest 请求参数
type RelationMultiResourceRequest struct {
	QueryList []struct {
		Timestamp int64 `json:"timestamp"`

		SourceType       Resource `json:"source_type,omitempty"`
		SourceInfo       Matcher  `json:"source_info,omitempty"`
		SourceExpandInfo Matcher  `json:"source_expand_info,omitempty"`

		TargetType     Resource `json:"target_type,omitempty"`
		TargetInfoShow bool     `json:"target_info_show,omitempty"`
		// ReturnAllPaths 开启后，legacy 接口会额外返回所有可执行的静态路径。
		// 未开启时保持原有的首条有效路径语义。
		ReturnAllPaths bool `json:"return_all_paths,omitempty"`

		PathResource  []Resource `json:"path_resource,omitempty"`
		LookBackDelta string     `json:"look_back_delta,omitempty"`
	} `json:"query_list"`
}

// RelationMultiResourceResponseData 响应数据
type RelationMultiResourceResponseData struct {
	Code int `json:"code"`

	SourceType Resource `json:"source_type"`
	SourceInfo Matcher  `json:"source_info"`
	TargetType Resource `json:"target_type"`

	TargetList Matchers                        `json:"target_list"`
	Path       []string                        `json:"path"`
	Paths      []RelationMultiResourcePathData `json:"paths,omitempty"`
	Message    string                          `json:"message"`

	// Truncated 表示响应是否因服务端安全上限而被截断。
	Truncated bool `json:"truncated,omitempty"`
	// TruncatedReason 标识触发截断的具体上限，便于调用方区分处理。
	TruncatedReason string `json:"truncated_reason,omitempty"`
}

// RelationMultiResourceResponse 请求返回
type RelationMultiResourceResponse struct {
	TraceID string                              `json:"trace_id"`
	Data    []RelationMultiResourceResponseData `json:"data"`
}

// RelationMultiResourceRangeRequest 请求参数
type RelationMultiResourceRangeRequest struct {
	QueryList []struct {
		StartTs int64  `json:"start_time"`
		EndTs   int64  `json:"end_time"`
		Step    string `json:"step"`

		SourceType       Resource `json:"source_type,omitempty"`
		SourceInfo       Matcher  `json:"source_info,omitempty"`
		SourceExpandInfo Matcher  `json:"source_expand_info,omitempty"`

		TargetType     Resource `json:"target_type,omitempty"`
		TargetInfoShow bool     `json:"target_info_show,omitempty"`
		ReturnAllPaths bool     `json:"return_all_paths,omitempty"`

		PathResource  []Resource `json:"path_resource,omitempty"`
		LookBackDelta string     `json:"look_back_delta,omitempty"`
	} `json:"query_list"`
}

// RelationMultiResourceRangeResponseData 响应数据
type RelationMultiResourceRangeResponseData struct {
	Code int `json:"code"`

	SourceType Resource `json:"source_type"`
	SourceInfo Matcher  `json:"source_info"`
	TargetType Resource `json:"target_type"`

	TargetList []MatchersWithTimestamp              `json:"target_list"`
	Path       []string                             `json:"path"`
	Paths      []RelationMultiResourceRangePathData `json:"paths,omitempty"`
	Message    string                               `json:"message"`

	// Truncated 表示响应是否因服务端安全上限而被截断。
	Truncated bool `json:"truncated,omitempty"`
	// TruncatedReason 标识触发截断的具体上限，便于调用方区分处理。
	TruncatedReason string `json:"truncated_reason,omitempty"`
}

// RelationMultiResourceRangeResponse 请求返回
type RelationMultiResourceRangeResponse struct {
	TraceID string                                   `json:"trace_id"`
	Data    []RelationMultiResourceRangeResponseData `json:"data"`
}
