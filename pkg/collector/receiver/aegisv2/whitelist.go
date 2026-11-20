// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegisv2

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

// 全量上报的采样率。
const sampleAll = 100

// 使用结构体而非 map，避免 JSON 输出字段顺序不稳定。
var whitelistSample = whitelistSampleMap{
	API:                sampleAll,
	AssetSpeed:         sampleAll,
	PagePerformance:    sampleAll,
	PV:                 sampleAll,
	Custom:             sampleAll,
	Session:            sampleAll,
	Error:              sampleAll,
	BridgeSpeed:        sampleAll,
	LoadPackageSpeed:   sampleAll,
	Websocket:          sampleAll,
	Replay:             sampleAll,
	ProcessPerformance: sampleAll,
}

var fallbackMsg = []byte(`{"code": 13, "message": "failed to marshal error message"}`)

type whitelistResponse struct {
	Code          int                `json:"code"`
	Msg           string             `json:"msg"`
	IsInWhiteList int                `json:"is_in_white_list"`
	SampleMap     whitelistSampleMap `json:"sample_map"`
	// ServerTime 为服务端收到本次 whitelist 请求的时间（毫秒时间戳，数字格式）。
	ServerTime int64 `json:"server_time"`
	// StartServerTime 为客户端发起请求的时间（毫秒时间戳，数字格式）。
	StartServerTime int64 `json:"start_server_time"`
}

// whitelistSampleMap 各上报类型的采样率，100 表示全量上报，0 表示不上报。
// 这是 Aegis 客户端的采样开关，与 BK-RUM 转换规则独立。
type whitelistSampleMap struct {
	API                int `json:"api"`                 // API 请求
	AssetSpeed         int `json:"asset_speed"`         // 静态资源加载速度
	PagePerformance    int `json:"page_performance"`    // 页面性能
	PV                 int `json:"pv"`                  // 页面访问量
	Custom             int `json:"custom"`              // 自定义事件
	Session            int `json:"session"`             // 用户会话
	Error              int `json:"error"`               // 错误事件
	BridgeSpeed        int `json:"bridge_speed"`        // JSBridge 调用速度
	LoadPackageSpeed   int `json:"load_package_speed"`  // 包加载速度
	Websocket          int `json:"websocket"`           // WebSocket 事件
	Replay             int `json:"replay"`              // 会话回放
	ProcessPerformance int `json:"process_performance"` // 进程性能
}

func (s *httpService) Whitelist(w http.ResponseWriter, req *http.Request) {
	serverTime := time.Now().UnixMilli()

	resp := whitelistResponse{
		Code:            whitelistCodeSuccess,
		Msg:             whitelistMsgSuccess,
		IsInWhiteList:   0,
		SampleMap:       whitelistSample,
		ServerTime:      serverTime,
		StartServerTime: serverTime,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, fallbackMsg)
		return
	}
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, b)
}
