// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkgse

// APICommonResp api通用返回结构体
type APICommonResp struct {
	Message string      `json:"message"`
	Result  bool        `json:"result"`
	Code    int         `json:"code"`
	Data    interface{} `json:"data"`
}

// QueryRouteDataResp query_route返回结构体
type QueryRouteDataResp struct {
	Metadata      GSEMetadata   `json:"metadata"`
	Route         []GSERoute    `json:"route"`
	StreamFilters []interface{} `json:"stream_filters"`
}

// GSEMetadata query_route返回的gse metadata结构体
type GSEMetadata struct {
	Version   string `json:"version"`
	ChannelId int    `json:"channel_id"`
	PlatName  string `json:"plat_name"`
	Label     struct {
		Odm       string `json:"odm"`
		BkBizId   int    `json:"bk_biz_id"`
		BkBizName string `json:"bk_biz_name"`
	} `json:"label"`
}

// GSERoute query_route返回的gse route结构体
type GSERoute struct {
	Name          string                 `json:"name"`
	StreamTo      map[string]interface{} `json:"stream_to"`
	FilterNameAnd []interface{}          `json:"filter_name_and"`
	FilterNameOr  []interface{}          `json:"filter_name_or"`
}
