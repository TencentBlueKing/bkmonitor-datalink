// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package formatter

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// Cutter :
type Cutter struct {
	*Processor
}

// NewCutter :
func NewCutter(ctx context.Context, name string) (*Cutter, error) {
	pipeConf := config.PipelineConfigFromContext(ctx)
	return &Cutter{
		Processor: NewProcessor(ctx, name, RecordHandlers{
			MetricsAsFloat64Creator(utils.NewMapHelper(pipeConf.Option).GetOrDefault(config.PipelineConfigOptAllowDynamicMetricsAsFloat, true).(bool)),
			MetricsCutterHandler,
		}),
	}, nil
}

func init() {
	define.RegisterDataProcessor("metric_cutter", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipe := config.PipelineConfigFromContext(ctx)
		if pipe == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipe config not found")
		}

		rt := config.ResultTableConfigFromContext(ctx)
		if rt == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table not found")
		}

		return NewCutter(ctx, pipe.FormatName(rt.FormatName(name)))
	})
}
