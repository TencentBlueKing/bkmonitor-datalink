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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
)

// SchemaFormatter
type SchemaFormatter struct {
	metrics    map[string]etl.TransformFn
	dimensions map[string]etl.TransformFn
}

// TransformMetricsHandlerCreator
func (f *SchemaFormatter) TransformMetricsHandlerCreator(allowMissing bool) define.ETLRecordChainingHandler {
	return TransformMetricsHandlerCreator(f.metrics, allowMissing)
}

// TransformDimensionsHandlerCreator
func (f *SchemaFormatter) TransformDimensionsHandlerCreator(allowMissing bool) define.ETLRecordChainingHandler {
	return TransformDimensionsHandlerCreator(f.dimensions, allowMissing)
}

// NewSchemaFormatterByResultTable
func NewSchemaFormatterByResultTable(rt *config.MetaResultTableConfig) (*SchemaFormatter, error) {
	metrics := make(map[string]etl.TransformFn)
	dimensions := make(map[string]etl.TransformFn)
	err := rt.VisitUserSpecifiedFields(func(config *config.MetaFieldConfig) error {
		transform := etl.NewTransformByField(config, nil)
		switch config.Tag {
		case define.MetaFieldTagDimension, define.MetaFieldTagGroup:
			dimensions[config.Name()] = transform
		case define.MetaFieldTagMetric:
			metrics[config.Name()] = transform
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	return &SchemaFormatter{
		dimensions: dimensions,
		metrics:    metrics,
	}, nil
}
