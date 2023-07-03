// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package flat

import (
	"context"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// BaseDimensionFieldsValue : 这部分字段的转换规则是规定好的,不需要根据用户行为修改
func BaseDimensionFieldsValue() []etl.Field {
	return []etl.Field{
		etl.NewSimpleField(
			define.RecordIPFieldName,
			etl.ExtractByJMESPath("ip"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordSupplierIDFieldName,
			etl.ExtractByJMESPath("bizid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordCloudIDFieldName,
			etl.ExtractByJMESPath("cloudid"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordBKAgentID,
			etl.ExtractByJMESPath("bk_agent_id"), etl.TransformNilString,
		),
		etl.NewSimpleFieldWithCheck(
			define.RecordBKBizID,
			etl.ExtractByJMESPath("bk_biz_id"), etl.TransformNilString, func(v interface{}) bool {
				return !etl.IfEmptyStringField(v)
			},
		),
		etl.NewSimpleField(
			define.RecordBKHostID,
			etl.ExtractByJMESPath("bk_host_id"), etl.TransformNilString,
		),
		etl.NewSimpleField(
			define.RecordTargetHostIDFieldName,
			etl.ExtractByJMESPath("bk_host_id"), etl.TransformNilString,
		),
	}
}

// IsBaseDimensionField : 判断是否是基本维度字段
func IsBaseDimensionField(fieldName string) bool {
	switch fieldName {
	case
		define.RecordIPFieldName,
		define.RecordSupplierIDFieldName,
		define.RecordCloudIDFieldName:
		return true
	}
	return false
}

// NewFlatProcessor :
func NewFlatProcessor(ctx context.Context, name string) (*template.RecordProcessor, error) {
	pipe := config.PipelineConfigFromContext(ctx)
	options := utils.NewMapHelper(pipe.Option)
	groupInfoName := options.GetOrDefault(config.PipelineConfigOptDimensionGroupAlias, define.RecordGroupFieldName).(string)

	record := etl.NewTSSchemaRecord(name).AddDimensions(BaseDimensionFieldsValue()...).
		AddTime(etl.NewSimpleFieldWithValue(define.TimeFieldName, time.Now().UTC(), etl.ExtractByJMESPath(define.TimeStampFieldName), etl.TransformAutoTimeStamp)).
		AddGroup(etl.NewSimpleField(define.RecordGroupFieldName, etl.ExtractByJMESPath(groupInfoName), etl.TransformAsIs))

	rt := config.ResultTableConfigFromContext(ctx)
	err := rt.VisitUserSpecifiedFields(func(config *config.MetaFieldConfig) error {
		field := etl.NewNewSimpleFieldWith(
			config.Name(), config.DefaultValue, config.HasDefaultValue(),
			etl.ExtractByJMESPath(config.FieldName), etl.NewTransformByField(config),
		)
		switch config.Tag {
		case define.MetaFieldTagDimension, define.MetaFieldTagGroup:
			// 去掉基本维度字段的添加。因为前面已经添加进去了
			if !IsBaseDimensionField(config.Name()) {
				record.AddDimensions(field)
			}
		case define.MetaFieldTagMetric:
			record.AddMetrics(field)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return template.NewRecordProcessor(name, pipe, record), nil
}

func init() {
	define.RegisterDataProcessor("flatten", func(ctx context.Context, name string) (define.DataProcessor, error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		if config.ResultTableConfigFromContext(ctx) == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "result table is empty")
		}
		return NewFlatProcessor(ctx, pipeConfig.FormatName(name))
	})
}
