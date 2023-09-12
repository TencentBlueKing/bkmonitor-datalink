// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package auto

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// SchemaByResultTablePlugin
func SchemaByResultTablePlugin(table *config.MetaResultTableConfig) etl.ContainerSchemaBuilderPlugin {
	return func(builder *etl.ContainerSchemaBuilder) error {
		var recordName string
		fields := make([]etl.Field, 0)
		err := table.VisitUserSpecifiedFields(func(config *config.MetaFieldConfig) error {
			name := GetRecordRootByTag(config.Tag)
			if name != recordName && len(fields) > 0 {
				err := builder.Apply(etl.SchemaSimpleRecordPlugin(recordName, fields...))
				if err != nil {
					return err
				}
				fields = make([]etl.Field, 0)
			}
			recordName = name
			fields = append(fields, etl.NewNewSimpleFieldWith(
				config.Name(), config.DefaultValue, config.HasDefaultValue(),
				etl.ExtractByJMESPath(config.Path()), etl.NewTransformByField(config),
			))
			return nil
		})
		if err != nil {
			return err
		}

		if len(fields) > 0 {
			err := builder.Apply(etl.SchemaSimpleRecordPlugin(recordName, fields...))
			if err != nil {
				return err
			}
		}
		return nil
	}
}

// GetSeparatorFieldByOption
func GetSeparatorFieldByOption(table *config.MetaResultTableConfig) (etl.Field, error) {
	helper := utils.NewMapHelper(table.Option)
	action, ok := helper.GetString(config.ResultTableOptSeparatorAction)
	if !ok {
		return nil, nil
	}

	source, ok := helper.GetString(config.ResultTableOptSeparatorNodeSource)
	if !ok {
		return nil, nil
	}

	node, ok := helper.GetString(config.ResultTableOptSeparatorNode)
	if !ok {
		return nil, nil
	}

	switch action {
	case "regexp":
		return etl.NewSimpleField(
			node, etl.ExtractByPath(source),
			etl.TransformMapByRegexp(helper.MustGetString(config.ResultTableOptLogSeparatorRegexp)),
		), nil
	case "json":
		return etl.NewSimpleField(node, etl.ExtractByPath(source), etl.TransformMapByJsonWithRetainExtraJSON(table)), nil
	case "delimiter":
		fieldList := make([]string, 0)
		for _, name := range helper.MustGet(config.PipelineConfigOptLogSeparatedFields).([]interface{}) {
			fieldList = append(fieldList, name.(string))
		}
		return etl.NewSimpleField(
			node, etl.ExtractByPath(source),
			etl.TransformMapBySeparator(helper.MustGetString(config.PipelineConfigOptLogSeparator), fieldList),
		), nil
	default:
		return nil, nil
	}
}

// PrepareByResultTablePlugin: 根据字段提取方法[json|regexp|delimiter]，解析上报的日志数据内容
func PrepareByResultTablePlugin(table *config.MetaResultTableConfig) etl.ContainerSchemaBuilderPlugin {
	return func(builder *etl.ContainerSchemaBuilder) error {
		fields := make([]etl.Field, 0)
		field, err := GetSeparatorFieldByOption(table)
		if err != nil {
			return err
		}
		if field != nil {
			fields = append(fields, field)
		}

		if len(fields) > 0 {
			err := builder.Apply(etl.SchemaPreparePlugin(etl.NewSimpleRecord(fields)))
			if err != nil {
				return err
			}
		}
		return nil
	}
}
