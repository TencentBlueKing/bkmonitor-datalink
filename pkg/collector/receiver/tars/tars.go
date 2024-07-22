// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package tars 实现 Tars 上报协议
package tars

import (
	"context"
	"time"

	"github.com/TarsCloud/TarsGo/tars/protocol/res/propertyf"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/statf"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func init() {
	receiver.RegisterReadyFunc(define.SourceTars, Ready)
}

// Ready 注册 Tars 服务
func Ready(config receiver.ComponentConfig) {
	if !config.Tars.Enabled {
		return
	}
	receiver.RegisterRecvTarsRoute("StatObj", "tarsstat", NewStatImpl(), new(statf.StatF))
	receiver.RegisterRecvTarsRoute("PropertyObj", "tarsproperty", NewPropertyImpl(), new(propertyf.PropertyF))
}

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceTars)

// StatImpl 服务统计上报服务
type StatImpl struct {
	receiver.Publisher
	pipeline.Validator
}

func getIpTokenFromCtxOrDefault(ctx context.Context, ip, token string) (string, string) {
	// 尝试从 ctx 中取 token
	tokenFromCtx := define.TokenFromTarsCtx(ctx)
	if len(tokenFromCtx) != 0 {
		token = tokenFromCtx
	}

	// 尝试从 ctx 中取 ip
	ipFromContext := utils.GetTarsIpFromContext(ctx)
	if len(ipFromContext) != 0 {
		ip = ipFromContext
	}
	return ip, token
}

func getIpTokenFromStats(stats map[statf.StatMicMsgHead]statf.StatMicMsgBody) (string, string) {
	var ip, token string
	// 尝试从 MsgHead 中读取 ip、token
	for head := range stats {
		switch head.MasterName {
		case "one_way_client", "stat_from_server":
			// 被调
			ip = head.SlaveIp
			_, token = define.TokenFromString(head.SlaveName)
			return ip, token
		default:
			// 主调
			ip = head.MasterIp
			_, token = define.TokenFromString(head.MasterName)
			return ip, token
		}
	}
	return ip, token
}

func getIpTokenFromProps(props map[propertyf.StatPropMsgHead]propertyf.StatPropMsgBody) (string, string) {
	var ip, token string
	// 尝试从 MsgHead 中读取 ip、token
	for head := range props {
		_, token = define.TokenFromString(head.ModuleName)
		ip = head.Ip
		break
	}
	return ip, token
}

// NewStatImpl 创建并返回一个 StatImpl 实例
func NewStatImpl() *StatImpl {
	return &StatImpl{}
}

// ReportMicMsg 接收统计指标，推送到处理队列
func (imp *StatImpl) ReportMicMsg(tarsCtx context.Context, stats map[statf.StatMicMsgHead]statf.StatMicMsgBody, bFromClient bool) (int32, error) {
	defer utils.HandleCrash()
	if len(stats) == 0 {
		return 0, nil
	}

	start := time.Now()
	ip, token := getIpTokenFromStats(stats)
	ip, token = getIpTokenFromCtxOrDefault(tarsCtx, ip, token)

	r := &define.Record{
		RequestType:   define.RequestTars,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordTars,
		Data: &define.TarsData{
			Type: define.TarsStatType,
			// 上报不带时间，这里以请求时间作为时间戳
			Timestamp: start.UnixMilli(),
			Data:      &define.TarsStatData{Stats: stats, FromClient: bFromClient},
		},
		Token: define.Token{Original: token},
	}

	code, processorName, err := imp.Validate(r)
	if err != nil {
		logger.Warnf("run pre-check failed, rtype=%s, code=%d, ip=%v, error: %s", define.RecordTars.S(), code, ip, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestTars, define.RecordTars, processorName, r.Token.Original, code)
		return -1, err
	}

	imp.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestTars, define.RecordTars, 0, start)
	return 0, nil
}

// ReportSampleMsg 未实现
func (imp *StatImpl) ReportSampleMsg(tarsCtx context.Context, msg []statf.StatSampleMsg) (int32, error) {
	// 没有需求，仅仅是实现一个接口避免报错
	return 0, nil
}

// PropertyImpl 业务特性上报服务
type PropertyImpl struct {
	receiver.Publisher
	pipeline.Validator
}

// NewPropertyImpl 创建并返回一个 PropertyImpl 实例
func NewPropertyImpl() *PropertyImpl {
	return &PropertyImpl{}
}

// ReportPropMsg 接收业务特性指标，推送到处理队列
func (imp *PropertyImpl) ReportPropMsg(tarsCtx context.Context, props map[propertyf.StatPropMsgHead]propertyf.StatPropMsgBody) (int32, error) {
	defer utils.HandleCrash()
	if len(props) == 0 {
		return 0, nil
	}

	start := time.Now()
	ip, token := getIpTokenFromProps(props)
	ip, token = getIpTokenFromCtxOrDefault(tarsCtx, ip, token)

	r := &define.Record{
		RequestType:   define.RequestTars,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordTars,
		Data: &define.TarsData{
			Type:      define.TarsPropertyType,
			Timestamp: start.UnixMilli(),
			Data:      &define.TarsPropertyData{Props: props},
		},
		Token: define.Token{Original: token},
	}

	code, processorName, err := imp.Validate(r)
	if err != nil {
		logger.Warnf("run pre-check failed, rtype=%s, code=%d, ip=%v, error: %s", define.RecordTars.S(), code, ip, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestTars, define.RecordTars, processorName, r.Token.Original, code)
		return -1, err
	}

	imp.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestTars, define.RecordTars, 0, start)
	return 0, nil
}
