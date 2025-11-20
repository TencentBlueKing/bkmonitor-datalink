// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdb

import (
	"fmt"
	"sort"
	"strings"
)

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
	V []Resource
}

func (r Relation) Info() (Resource, Resource, string) {
	if len(r.V) != 2 {
		return "", "", ""
	}

	source, target := r.V[0], r.V[1]
	resources := []string{string(source), string(target)}
	sort.Strings(resources)
	field := fmt.Sprintf("%s_relation", strings.Join(resources, "_with_"))
	return source, target, field
}

// Path 关联路径
type Path []Relation

// Paths 多组关联路径
type Paths []Path

// RelationMultiResourceRequest 请求参数
type RelationMultiResourceRequest struct {
	QueryList []struct {
		Timestamp int64 `json:"timestamp"`

		SourceType       Resource `json:"source_type,omitempty"`
		SourceInfo       Matcher  `json:"source_info,omitempty"`
		SourceExpandInfo Matcher  `json:"source_expand_info,omitempty"`

		TargetType     Resource `json:"target_type,omitempty"`
		TargetInfoShow bool     `json:"target_info_show,omitempty"`

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

	TargetList Matchers `json:"target_list"`
	Path       []string `json:"path"`
	Message    string   `json:"message"`
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

	TargetList []MatchersWithTimestamp `json:"target_list"`
	Path       []string                `json:"path"`
	Message    string                  `json:"message"`
}

// RelationMultiResourceRangeResponse 请求返回
type RelationMultiResourceRangeResponse struct {
	TraceID string                                   `json:"trace_id"`
	Data    []RelationMultiResourceRangeResponseData `json:"data"`
}
