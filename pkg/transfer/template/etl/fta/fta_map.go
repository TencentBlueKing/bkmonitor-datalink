// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fta

import (
	"context"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	// IngestTimeField: 事件数据采集时间字段
	IngestTimeField = "bk_ingest_time"
	// CleanTimeField: 事件数据清洗时间字段
	CleanTimeField = "bk_clean_time"
	// TagsField: 事件标签字段
	TagsField = "tags"
	// DefaultEventIDField: 默认事件ID字段
	DefaultEventIDField = "__bk_event_id__"
	// EventIDField: 事件ID字段
	EventIDField = "event_id"
	// AlertNameField: 告警名称字段
	AlertNameField = "alert_name"
	// DefaultPluginIDField: 默认插件ID字段
	DefaultPluginIDField = "bk_plugin_id"
	// PluginIDField: 插件ID字段
	PluginIDField = "plugin_id"
)

// convertToExprMap 尝试将JSON配置转换成结构化数据
func convertToExprMap(c interface{}) (map[string]string, error) {
	cfgJSON, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	var fields []struct {
		Field string `json:"field"`
		Expr  string `json:"expr"`
	}
	err = json.Unmarshal(cfgJSON, &fields)
	if err != nil {
		return nil, err
	}

	fieldExpr := make(map[string]string)

	for _, cfg := range fields {
		if cfg.Expr == "" {
			continue
		}
		fieldExpr[cfg.Field] = cfg.Expr
	}
	return fieldExpr, nil
}

func nowTimeDefaultValue() interface{} {
	result, err := etl.TransformAutoTimeStamp(time.Now().UTC())
	if err != nil {
		return nil
	}
	return result
}

// NewMapFTAProcessor
func NewMapFTAProcessor(ctx context.Context, name string) (*template.RecordProcessor, error) {
	pipeConfig := config.PipelineConfigFromContext(ctx)
	helper := utils.NewMapHelper(pipeConfig.Option)
	fieldsCfg, _ := helper.Get(config.PipelineConfigOptFTAFieldMappingKey)

	exprMap, err := convertToExprMap(fieldsCfg)
	if err != nil {
		return nil, err
	}

	decoder := etl.NewPayloadDecoder()

	// 设置默认字段
	fields := []etl.Field{
		etl.NewSimpleField(IngestTimeField, etl.ExtractByJMESPathWithCustomFn(IngestTimeField), etl.TransformAutoTimeStamp),
		etl.NewSimpleField(PluginIDField, etl.ExtractByJMESPathWithCustomFn(DefaultPluginIDField), etl.TransformString),
		etl.NewDefaultsField(CleanTimeField, nowTimeDefaultValue),
	}

	record := etl.NewNamedSimpleRecord(name, fields)

	rt := config.ResultTableConfigFromContext(ctx)
	err = rt.VisitUserSpecifiedFields(func(config *config.MetaFieldConfig) error {
		if config.FieldName == TagsField {
			// tags后面再处理
			return nil
		}
		expr, ok := exprMap[config.FieldName]
		if !ok {
			return nil
		}
		field := etl.NewSimpleFieldWithValue(
			config.FieldName, nil, etl.ExtractByJMESPathWithCustomFn(expr), etl.NewTransformByField(config),
		)
		record.AddFields(field)
		return nil
	})

	if err != nil {
		return nil, err
	}

	// 告警名称处理
	record.AddFields(
		etl.NewFunctionField(AlertNameField, func(name string, from etl.Container, to etl.Container) error {
			alertName, _ := from.Get(config.PipelineConfigOptFTAAlertNameKey)

			if alertName != nil && alertName != "" {
				logging.Debugf("using alert_name->(%s) from origin data: %+v", alertName, from)
				return to.Put(name, alertName)
			}

			alertName, _ = to.Get(name)

			if alertName == nil || alertName == "" {
				return errors.Errorf("alert_name is empty, origin data: %+v", from)
			}

			logging.Debugf("using alert_name->(%s) from origin data: %+v", alertName, from)
			return nil
		}),
	)

	// 告警名称处理
	record.AddFields(
		etl.NewFunctionField(EventIDField, func(name string, from etl.Container, to etl.Container) error {
			eventID, _ := to.Get(name)
			if eventID != nil && eventID != "" {
				logging.Debugf("using event_id->(%s) from origin data: %+v", eventID, from)
				return nil
			}

			eventID, err = from.Get(DefaultEventIDField)
			if err != nil {
				return errors.Errorf("get key->(%s) from data failed: %+v, origin data: %+v",
					DefaultEventIDField, err, from)
			}
			logging.Debugf("using event_id->(%s) from origin data: %+v", eventID, from)
			return to.Put(name, eventID)
		}),
	)

	// tags 格式转换，统一格式 [{"key": "a", "value": "b"}]
	if expr, ok := exprMap[TagsField]; ok {
		tagsCompiled, err := utils.CompileJMESPathCustom(expr)
		if err != nil {
			return nil, errors.Errorf("tags expr(%s) compile error: %+v", expr, err)
		}
		record.AddFields(etl.NewFunctionField(TagsField, func(name string, from etl.Container, to etl.Container) error {
			data := etl.ContainerToMap(from)
			tags, err := tagsCompiled.Search(data)
			if err != nil {
				// 提取失败则直接忽略
				logging.Debugf("extract tags field failed: %+v, origin data: %+v", err, data)
				return nil
			}
			logging.Debugf("tags data process start: %+v", tags)
			var tagsList []map[string]interface{}
			switch t := tags.(type) {
			case map[string]interface{}:
				// 针对 tags 为 {"a": "b"} 格式的转换
				for key, value := range t {
					tagsList = append(tagsList, map[string]interface{}{
						"key":   key,
						"value": value,
					})
				}
			case []interface{}:
				// 针对 tags 为 [{"key": "a", "value": "b"}] 的转换
				for _, item := range t {
					mapItem, ok := item.(map[string]interface{})
					if !ok {
						continue
					}
					key := mapItem["key"]
					value := mapItem["value"]
					if key == nil || value == nil {
						continue
					}
					tagsList = append(tagsList, map[string]interface{}{
						"key":   key,
						"value": value,
					})
				}
			}
			logging.Debugf("tags data process end: %+v", tagsList)
			return to.Put(name, tagsList)
		}))
	}

	return template.NewRecordProcessorWithDecoderFn(
		name, config.PipelineConfigFromContext(ctx), record, decoder.Decode,
	), nil
}

func init() {
	define.RegisterDataProcessor("fta-map", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewMapFTAProcessor(ctx, pipeConfig.FormatName(name))
	})
}
