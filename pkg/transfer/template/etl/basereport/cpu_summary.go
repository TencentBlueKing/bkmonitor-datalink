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

// NewCPUSummaryProcessor :
func NewCPUSummaryProcessor(ctx context.Context, name string) *template.RecordProcessor {
	return template.NewRecordProcessorWithContext(ctx, name, config.PipelineConfigFromContext(ctx), etl.NewTSSchemaRecord(name).AddDimensions(
		BaseDimensionBaseReportFieldsValue()...).AddDimensions(
		etl.NewSimpleField(
			"device_name",
			etl.ExtractByJMESPath("data.cpu.total_stat.cpu"), etl.TransformNilString,
		),
	).AddMetrics(
		etl.NewSimpleField(
			"user",
			etl.ExtractByJMESPath("data.cpu.total_stat.user"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"system",
			etl.ExtractByJMESPath("data.cpu.total_stat.system"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"nice",
			etl.ExtractByJMESPath("data.cpu.total_stat.nice"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"idle",
			etl.ExtractByJMESPath("data.cpu.total_stat.idle"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"iowait",
			etl.ExtractByJMESPath("data.cpu.total_stat.iowait"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"interrupt",
			etl.ExtractByJMESPath("data.cpu.total_stat.irq"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"softirq",
			etl.ExtractByJMESPath("data.cpu.total_stat.softirq"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"stolen",
			etl.ExtractByJMESPath("data.cpu.total_stat.stolen"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"usage",
			etl.ExtractByJMESPath("data.cpu.total_usage"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"guest",
			etl.ExtractByJMESPath("data.cpu.total_stat.guest"), etl.TransformNilFloat64,
		),
		etl.NewFutureField("pct", func(name string, from etl.Container, to etl.Container) error {
			// 由于上报的数据是非百分比的数值，所以需要通过转换计算
			names := []string{
				"user", "system", "nice", "idle", "iowait", "interrupt", "softirq", "stolen", "guest",
			}
			values := make(map[string]float64)
			sum := 0.0
			for _, name := range names {
				v, err := to.Get(name)
				if err != nil {
					continue
				}

				switch value := v.(type) {
				case float64:
					sum += value
					values[name] = value
				}
			}

			if sum == 0.0 {
				return nil
			}

			for name, value := range values {
				err := to.Put(name, value/sum)
				if err != nil {
					continue
				}
			}

			return nil
		}),
	).AddTime(etl.NewSimpleField(
		"time", etl.ExtractByJMESPath("data.utctime"),
		etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
	)))
}

func init() {
	define.RegisterDataProcessor("system.cpu_summary", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConf := config.PipelineConfigFromContext(ctx)
		if pipeConf == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewCPUSummaryProcessor(ctx, pipeConf.FormatName(name)), nil
	})
}
