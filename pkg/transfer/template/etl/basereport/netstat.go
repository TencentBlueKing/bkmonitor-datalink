// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package basereport

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
)

// NewNetStatProcessor :
func NewNetStatProcessor(ctx context.Context, name string) *template.RecordProcessor {
	return template.NewRecordProcessorWithContext(ctx, name, config.PipelineConfigFromContext(ctx), etl.NewTSSchemaRecord(name).AddDimensions(
		BaseDimensionBaseReportFieldsValue()...).AddMetrics(
		etl.NewSimpleField(
			"cur_tcp_closed",
			etl.ExtractByJMESPath("data.net.netstat.close"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_closewait",
			etl.ExtractByJMESPath("data.net.netstat.closeWait"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_closing",
			etl.ExtractByJMESPath("data.net.netstat.closing"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_estab",
			etl.ExtractByJMESPath("data.net.netstat.established"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_finwait1",
			etl.ExtractByJMESPath("data.net.netstat.finWait1"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_finwait2",
			etl.ExtractByJMESPath("data.net.netstat.finWait2"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_lastack",
			etl.ExtractByJMESPath("data.net.netstat.lastAck"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_listen",
			etl.ExtractByJMESPath("data.net.netstat.listen"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_syn_recv",
			etl.ExtractByJMESPath("data.net.netstat.synRecv"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_syn_sent",
			etl.ExtractByJMESPath("data.net.netstat.syncSent"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_timewait",
			etl.ExtractByJMESPath("data.net.netstat.timeWait"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_udp_indatagrams",
			etl.ExtractByJMESPath("data.net.protocolstat.udp.inDatagrams"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_udp_outdatagrams",
			etl.ExtractByJMESPath("data.net.protocolstat.udp.outDatagrams"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_activeopens",
			etl.ExtractByJMESPath("data.net.protocolstat.tcp.activeOpens"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_passiveopens",
			etl.ExtractByJMESPath("data.net.protocolstat.tcp.passiveOpens"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cur_tcp_retranssegs",
			etl.ExtractByJMESPath("data.net.protocolstat.tcp.retransSegs"), etl.TransformNilFloat64,
		),
	).AddTime(etl.NewSimpleField(
		"time", etl.ExtractByJMESPath("data.utctime"),
		etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
	)))
}

func init() {
	define.RegisterDataProcessor("system.netstat", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewNetStatProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
