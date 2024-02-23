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

// Formatter :
type Formatter struct {
	*Processor
}

// NewTSFormatter :
func NewTSFormatter(ctx context.Context, name string) (*Formatter, error) {
	rt := config.ResultTableConfigFromContext(ctx)
	schema, err := NewSchemaFormatterByResultTable(rt)
	if err != nil {
		return nil, err
	}
	pipeConf := config.PipelineConfigFromContext(ctx)
	store := define.StoreFromContext(ctx)
	option := utils.NewMapHelper(pipeConf.Option)
	rtOption := utils.NewMapHelper(rt.Option)

	enableDbmMeta, _ := rtOption.GetBool(config.ResultTableListConfigOptEnableDbmMeta)
	enableDevxMeta, _ := rtOption.GetBool(config.ResultTableListConfigOptEnableDevxMeta)

	return &Formatter{
		Processor: NewProcessor(ctx, name, RecordHandlers{
			CheckRecordHandler(option.GetOrDefault(config.PipelineConfigOptionIsLogData, false).(bool)),
			LocalTimeInjectHandlerCreator(define.LocalTimeFieldName, option.GetOrDefault(config.PipelineConfigOptInjectLocalTime, true).(bool)),
			FillBizIDHandlerCreator(store, rt),
			FillCmdbLevelHandlerCreator(rtOption.GetOrDefault(config.ResultTableListConfigOptMetricSplitLevel, []interface{}{}).([]interface{}), store, option.GetOrDefault(config.ResultTableListConfigOptEnableTopo, true).(bool)),
			FillSupplierIDHandler,
			FillDefaultValueCreator(rtOption.GetOrDefault(config.ResultTableListConfigOptEnableFillDefault, true).(bool), rt),
			TransferRecordCutterByDbmMetaCreator(store, enableDbmMeta),
			TransferRecordCutterByDevxMetaCreator(store, enableDevxMeta),
			TransferRecordCutterByCmdbLevelCreator(rtOption.GetOrDefault(config.ResultTableListConfigOptMetricSplitLevel, []interface{}{}).([]interface{}), rtOption.GetOrDefault(config.ResultTableListConfigOptEnableKeepCmdbLevel, true).(bool)),
			TransformAliasNameHandlerCreator(rt, option.GetOrDefault(config.PipelineConfigOptTransformEnableFieldAlias, false).(bool)),
			schema.TransformMetricsHandlerCreator(option.GetOrDefault(config.PipelineConfigOptAllowMetricsMissing, true).(bool)),
			schema.TransformDimensionsHandlerCreator(option.GetOrDefault(config.PipelineConfigOptAllowDimensionsMissing, true).(bool)),
			RoundingTimeHandlerCreator(option.GetOrDefault(config.PipelineConfigOptTimePrecision, "").(string)),
			AlignTimeUnitHandler(option.GetOrDefault(config.PipelineConfigOptAlignTimeUnit, "").(string)),
		}),
	}, nil
}

func init() {
	define.RegisterDataProcessor("ts_format", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipe := config.PipelineConfigFromContext(ctx)
		if pipe == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipe config not found")
		}

		rt := config.ResultTableConfigFromContext(ctx)
		if rt == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table not found")
		}

		if define.StoreFromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "store not found")
		}

		if config.FromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "config not found")
		}

		return NewTSFormatter(ctx, pipe.FormatName(rt.FormatName(name)))
	})
}
