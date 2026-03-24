// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkdata

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"

// CommonBkdataRespMeta for bkdata api, code为字符串类型
type CommonBkdataRespMeta struct {
	define.ApiCommonRespMeta
	Code   string `json:"code"`
	Errors any    `json:"errors"`
}

// CommonResp api bkdata通用返回结构体
type CommonResp struct {
	CommonBkdataRespMeta
	Data any `json:"data"`
}

// CommonMapResp api bkdata通用返回结构体Map
type CommonMapResp struct {
	CommonBkdataRespMeta
	Data map[string]any `json:"data"`
}

// CommonListResp bkdata通用返回结构体List
type CommonListResp struct {
	CommonBkdataRespMeta
	Data []any `json:"data"`
}

// CommonListMapResp api bkdata通用返回结构体List-Map
type CommonListMapResp struct {
	CommonBkdataRespMeta
	Data []map[string]any `json:"data"`
}

type CreateDataHubResp struct {
	CommonBkdataRespMeta
	Data CreateDataHubData `json:"data"`
}

type CreateDataHubData struct {
	RawDataId uint     `json:"raw_data_id"`
	CleanRtId []string `json:"clean_rt_id"`
}

type AccessDeployPlanResp struct {
	CommonBkdataRespMeta
	Data struct {
		RawDataId int `json:"raw_data_id"`
	} `json:"data"`
}

type GetDataFlowGraphResp struct {
	CommonBkdataRespMeta
	Data *GetDataFlowGraphRespData `json:"data"`
}

type GetDataFlowGraphRespData struct {
	Nodes   []map[string]any `json:"nodes"`
	Links   []any            `json:"links"`
	Version string           `json:"version"`
}
