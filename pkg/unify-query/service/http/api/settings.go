// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package api

const (
	RelationMultiResourceConfigPath      = "api.relation.multi_resource"
	RelationMultiResourceRangeConfigPath = "api.relation.mutil_resource_range"
	RelationMaxRoutingConfigPath         = "api.relation.max_routing"

	// V1beta3 SurrealDB 图查询专用路由（与 v1beta1 并存）
	RelationV1Beta3MultiResourceConfigPath      = "api.relation.v1beta3.multi_resource"
	RelationV1Beta3MultiResourceRangeConfigPath = "api.relation.v1beta3.multi_resource_range"
)

var (
	RelationMultiResource      string
	RelationMultiResourceRange string
	RelationMaxRouting         int

	RelationV1Beta3MultiResource      string
	RelationV1Beta3MultiResourceRange string
)
