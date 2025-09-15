// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"fmt"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	TokenAppName = "app_name"
)

// Token 描述了 Record 校验的必要信息
type Token struct {
	Type           string `config:"type"`
	Original       string `config:"token"`
	BizId          int32  `config:"bk_biz_id"`
	AppName        string `config:"bk_app_name"`
	MetricsDataId  int32  `config:"metrics_dataid"`
	TracesDataId   int32  `config:"traces_dataid"`
	ProfilesDataId int32  `config:"profiles_dataid"`
	LogsDataId     int32  `config:"logs_dataid"`
	ProxyDataId    int32  `config:"proxy_dataid"`
	BeatDataId     int32  `config:"beat_dataid"`
}

func (t Token) BizApp() string {
	return fmt.Sprintf("%d-%s", t.BizId, t.AppName)
}

func (t Token) GetDataID(rtype RecordType) int32 {
	switch rtype {
	case RecordTraces, RecordTracesDerived:
		return t.TracesDataId
	case RecordMetrics, RecordMetricsDerived, RecordPushGateway, RecordRemoteWrite, RecordPingserver, RecordFta, RecordTars:
		return t.MetricsDataId
	case RecordLogs, RecordLogsDerived:
		return t.LogsDataId
	case RecordProfiles:
		return t.ProfilesDataId
	case RecordProxy:
		return t.ProxyDataId
	case RecordBeat:
		return t.BeatDataId
	}
	return -1
}

var tokenInfo = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: MonitoringNamespace,
		Name:      "receiver_token_info",
		Help:      "Receiver decoded token info",
	},
	[]string{"token", "metrics_id", "traces_id", "logs_id", "profiles_id", "proxy_id", "app_name", "biz_id"},
)

func SetTokenInfo(token Token) {
	tokenInfo.WithLabelValues(
		token.Original,
		fmt.Sprintf("%d", token.MetricsDataId),
		fmt.Sprintf("%d", token.TracesDataId),
		fmt.Sprintf("%d", token.LogsDataId),
		fmt.Sprintf("%d", token.ProfilesDataId),
		fmt.Sprintf("%d", token.ProxyDataId),
		token.AppName,
		fmt.Sprintf("%d", token.BizId),
	).Set(1)
}
