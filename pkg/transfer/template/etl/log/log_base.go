// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package log

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// FieldValue
const (
	FieldValue           = "_value_"
	FieldIterationIndex  = "_iteration_idx"
	FieldName            = "log"
	FieldSeparatorValues = "_separator_values_"
)

// NewLogProcessor
func NewLogProcessor(ctx context.Context, name string, update func(record *etl.TSSchemaRecord, decoder *etl.PayloadDecoder)) (*template.RecordProcessor, error) {
	pipe := config.PipelineConfigFromContext(ctx)
	options := utils.NewMapHelper(pipe.Option)
	groupInfoName := options.GetOrDefault(config.PipelineConfigOptDimensionGroupAlias, define.RecordGroupFieldName).(string)
	record := etl.NewTSSchemaRecord(name).AddTime(etl.NewSimpleFieldWithValue(
		define.TimeFieldName, func() interface{} { return time.Now().UTC() },
		etl.ExtractByPath(define.TimeFieldName), etl.TransformAutoTimeStamp,
	)).AddMetrics(
		etl.NewSimpleField(FieldIterationIndex, etl.ExtractByPath(FieldIterationIndex), etl.TransformAsIs),
	).AddGroup(
		etl.NewSimpleField(define.RecordGroupFieldName, etl.ExtractByPath(groupInfoName), etl.TransformAsIs),
	)

	decoder := etl.NewPayloadDecoder().FissionSplitHandler(
		true, etl.ExtractByPath(FieldValue), FieldIterationIndex, FieldName,
	)

	if update != nil {
		update(record, decoder)
	}

	rt := config.ResultTableConfigFromContext(ctx)
	err := rt.VisitUserSpecifiedFields(func(config *config.MetaFieldConfig) error {
		field := etl.NewNewSimpleFieldWith(
			config.Name(), config.DefaultValue, config.HasDefaultValue(),
			etl.ExtractByPath(config.FieldName), etl.NewTransformByField(config),
		)
		switch config.Tag {
		case define.MetaFieldTagDimension, define.MetaFieldTagGroup:
			record.AddDimensions(field)
		case define.MetaFieldTagMetric:
			record.AddMetrics(field)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return template.NewRecordProcessorWithDecoderFn(
		name, config.PipelineConfigFromContext(ctx), record, decoder.Decode,
	), nil
}
