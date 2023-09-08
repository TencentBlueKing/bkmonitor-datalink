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

// Resource 资源
type Resource string

// Relation 两点关联路径
type Relation struct {
	V []Resource
}

// Path 关联路径
type Path []Relation

// Paths 多组关联路径
type Paths []Path

// RelationMultiResourceRequest 请求参数
type RelationMultiResourceRequest struct {
	Timestamp int64 `json:"timestamp"`
	QueryList []struct {
		TargetType Resource `json:"target_type"`
		SourceInfo Matcher  `json:"source_info"`
	} `json:"query_list"`
}

type RelationMultiResourceResponseData struct {
	Code       int       `json:"code"`
	SourceType Resource  `json:"source_type,omitempty"`
	SourceInfo Matcher   `json:"source_info,omitempty"`
	TargetType Resource  `json:"target_type,omitempty"`
	TargetList []Matcher `json:"target_list,omitempty"`
	Message    string    `json:"message,omitempty"`
}

// RelationMultiResourceResponse 请求返回
type RelationMultiResourceResponse struct {
	Data []RelationMultiResourceResponseData `json:"data"`
}
