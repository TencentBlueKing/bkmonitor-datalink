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

	"github.com/cstockton/go-conv"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// NewMemProcessor :
func NewMemProcessor(ctx context.Context, name string) *template.RecordProcessor {
	return template.NewRecordProcessorWithContext(ctx, name, config.PipelineConfigFromContext(ctx), etl.NewTSSchemaRecord(name).AddDimensions(
		BaseDimensionBaseReportFieldsValue()...).AddMetrics(
		etl.NewSimpleField(
			"buffer",
			etl.ExtractByJMESPath("data.mem.meminfo.buffers"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"cached",
			etl.ExtractByJMESPath("data.mem.meminfo.cached"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"free",
			etl.ExtractByJMESPath("data.mem.meminfo.free"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"total",
			etl.ExtractByJMESPath("data.mem.meminfo.total"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"usable",
			etl.ExtractByJMESPath("data.mem.meminfo.available"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"used",
			etl.ExtractByJMESPath("data.mem.meminfo.used"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"shared",
			etl.ExtractByJMESPath("data.mem.meminfo.shared"), etl.TransformNilFloat64,
		),
		etl.NewSimpleField(
			"pct_used",
			etl.ExtractByJMESPath("data.mem.meminfo.usedPercent"), etl.TransformNilFloat64,
		),
		etl.NewFutureFieldWithFn("pct_usable", func(name string, to etl.Container) (interface{}, error) {
			usable, err := to.Get("usable")
			if err != nil {
				return nil, err
			}
			total, err := to.Get("total")
			if err != nil {
				return nil, err
			}

			value, err := utils.DivNumber(usable, total)
			if err != nil {
				return nil, err
			}

			return value * 100, err
		}),
		etl.NewFutureFieldWithFn("psc_used", func(name string, to etl.Container) (interface{}, error) {
			total, err := to.Get("total")
			if err != nil {
				return nil, err
			}
			free, err := to.Get("free")
			if err != nil {
				return nil, err
			}
			totalValue, err := conv.DefaultConv.Float64(total)
			if err != nil {
				return nil, err
			}
			freeValue, err := conv.DefaultConv.Float64(free)
			if err != nil {
				return nil, err
			}
			return totalValue - freeValue, err
		}),
		etl.NewFutureFieldWithFn("psc_pct_used", func(name string, to etl.Container) (interface{}, error) {
			total, err := to.Get("total")
			if err != nil {
				return nil, err
			}
			pscUsed, err := to.Get("psc_used")
			if err != nil {
				return nil, err
			}

			value, err := utils.DivNumber(pscUsed, total)
			if err != nil {
				return nil, err
			}

			return value * 100, err
		}),
	).AddTime(etl.NewSimpleField(
		"time", etl.ExtractByJMESPath("data.utctime"),
		etl.TransformTimeStampWithUTCLayout("2006-01-02 15:04:05"),
	)))
}

func init() {
	define.RegisterDataProcessor("system.mem", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewMemProcessor(ctx, pipeConfig.FormatName(name)), nil
	})
}
