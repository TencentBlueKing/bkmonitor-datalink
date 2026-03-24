// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkgse

// Operation 操作人配置
type Operation struct {
	OperatorName string `json:"operator_name"`
}

// RouteMetadata 路由数据
type RouteMetadata struct {
	ChannelId uint    `json:"channel_id"`
	PlatName  string  `json:"plat_name"`
	Label     *string `json:"label,omitempty"`
}

// QueryRouteParams QueryRoute 参数
type QueryRouteParams struct {
	Condition RouteMetadata `json:"condition"`
	Operation Operation     `json:"operation"`
}

// AddRouteParams AddRoute 参数
type AddRouteParams struct {
	Metadata      RouteMetadata `json:"metadata"`
	Route         []any         `json:"route,omitempty"`
	StreamFilters []any         `json:"stream_filters,omitempty"`
	Operation     Operation     `json:"operation"`
}

// UpdateRouteParams UpdateRoute 参数
type UpdateRouteParams struct {
	Condition     RouteMetadata  `json:"condition"`
	Specification map[string]any `json:"specification"`
	Operation     Operation      `json:"operation"`
}
