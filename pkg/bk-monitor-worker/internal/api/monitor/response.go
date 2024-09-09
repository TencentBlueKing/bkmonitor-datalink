// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package monitor

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
)

type SearchAlertResp struct {
	define.ApiCommonRespMeta
	Data SearchAlertData `json:"data"`
}

type SearchAlertData struct {
	Total  int                   `json:"total"`
	Alerts []SearchAlertDataInfo `json:"alerts"`
}

type SearchAlertDataInfo struct {
	BkBizID          int32  `json:"bk_biz_id"`
	BkBizName        string `json:"bk_biz_name"`
	StrategyID       int32  `json:"strategy_id"`
	StrategyName     string `json:"strategy_name"`
	FirstAnomalyTime int64  `json:"first_anomaly_time"`
	LatestTime       int64  `json:"latest_time"`
	EventID          string `json:"event_id"`
	Status           string `json:"status"`
}
