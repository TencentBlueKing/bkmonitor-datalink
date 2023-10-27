// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package etl

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

// SchemaSimpleFieldPlugin
func SchemaFieldsPlugin(fields ...Field) ContainerSchemaBuilderPlugin {
	return func(builder *ContainerSchemaBuilder) error {
		builder.AddRecords(NewSimpleRecord(fields))
		return nil
	}
}

// SchemaSimpleFieldPlugin
func SchemaSimpleFieldPlugin(name string, extract ExtractFn, transform TransformFn) ContainerSchemaBuilderPlugin {
	return SchemaFieldsPlugin(NewSimpleField(name, extract, transform))
}

// StandardTimeFieldPlugin : 添加标准时间字段
func StandardTimeFieldPlugin(extract ExtractFn) ContainerSchemaBuilderPlugin {
	return SchemaFieldsPlugin(NewSimpleFieldWithValue(define.TimeFieldName, func() interface{} {
		return time.Now().UTC()
	}, extract, TransformAutoTimeStamp))
}

// DefaultTimeAliasFieldPlugin
func DefaultTimeAliasFieldPlugin(field string) ContainerSchemaBuilderPlugin {
	return SchemaPreparePlugin(NewSimpleRecord([]Field{
		NewInitialField(define.TimeFieldName, ExtractByJMESPath(field), TransformAsIs),
	}))
}

// StandardBeatFieldsPlugin : 添加采集器默认字段
func StandardBeatFieldsPlugin(builder *ContainerSchemaBuilder) error {
	builder.AddRecords(NewPrepareRecord([]Record{NewNamedSimpleRecord(define.RecordDimensionsFieldName, []Field{
		NewInitialField(define.RecordIPFieldName, ExtractByJMESPath("ip"), TransformNilString),
		NewInitialField(define.RecordSupplierIDFieldName, ExtractByJMESPath("bizid"), TransformNilString),
		NewInitialField(define.RecordCloudIDFieldName, ExtractByJMESPath("cloudid"), TransformNilString),
	})}))
	return nil
}

// SchemaRecordsPlugin
func SchemaRecordsPlugin(records ...Record) ContainerSchemaBuilderPlugin {
	return func(builder *ContainerSchemaBuilder) error {
		builder.AddRecords(records...)
		return nil
	}
}

// RecordPlugin
func SchemaSimpleRecordPlugin(name string, fields ...Field) ContainerSchemaBuilderPlugin {
	return SchemaRecordsPlugin(NewNamedSimpleRecord(name, fields))
}

// SchemaDimensionsPlugin
func SchemaDimensionsPlugin(fields ...Field) ContainerSchemaBuilderPlugin {
	return SchemaSimpleRecordPlugin(define.RecordDimensionsFieldName, fields...)
}

// SchemaMetricsPlugin
func SchemaMetricsPlugin(fields ...Field) ContainerSchemaBuilderPlugin {
	return SchemaSimpleRecordPlugin(define.RecordMetricsFieldName, fields...)
}

// SchemaGroupPlugin
func SchemaGroupPlugin(fields ...Field) ContainerSchemaBuilderPlugin {
	return SchemaDimensionsPlugin(fields...)
}

// SchemaPreparePlugin
func SchemaPreparePlugin(records ...Record) ContainerSchemaBuilderPlugin {
	return SchemaRecordsPlugin(NewPrepareRecord(records))
}

// SchemaReprocessPlugin
func SchemaReprocessPlugin(records ...Record) ContainerSchemaBuilderPlugin {
	return SchemaRecordsPlugin(NewReprocessRecord(records))
}
