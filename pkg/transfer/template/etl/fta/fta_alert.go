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
	"slices"
	"sort"
	"time"

	"github.com/jmespath/go-jmespath"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	template "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/template/etl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

const (
	// fieldIngestTime 事件数据采集时间字段
	fieldIngestTime = "bk_ingest_time"
	// fieldCleanTime 事件数据清洗时间字段
	fieldCleanTime = "bk_clean_time"
	// fieldTags 事件标签字段
	fieldTags = "tags"
	// fieldDimensions 事件维度字段
	fieldDimensions = "dimensions"
	// fieldDedupeKeys 事件去重字段
	fieldDedupeKeys = "dedupe_keys"
	// fieldDefaultEventID 默认事件ID字段
	fieldDefaultEventID = "__bk_event_id__"
	// fieldEventID 事件ID字段
	fieldEventID = "event_id"
	// fieldAlertName 告警名称字段
	fieldAlertName = "alert_name"
	// fieldDefaultPluginID 默认插件ID字段
	fieldDefaultPluginID = "bk_plugin_id"
	// fieldPluginID 插件ID字段
	fieldPluginID = "plugin_id"
	// fieldCleanConfig 清洗配置字段
	fieldCleanConfig = "clean_configs"
)

// extractTags 从data中提取tags字段
func extractTags(
	name string,
	exprMap map[string]*jmespath.JMESPath,
	data map[string]interface{},
	to etl.Container,
) error {
	var dimensions map[string]interface{}
	var dedupeKeys []string

	if expr, ok := exprMap[fieldDimensions]; ok {
		value, err := expr.Search(data)
		if err != nil {
			return errors.Wrapf(err, "search dimension expr %v failed", expr)
		}

		// 将dimensions的key作为dedupe_keys
		switch t := value.(type) {
		case map[string]interface{}:
			for key := range t {
				dedupeKeys = append(dedupeKeys, key)
			}
			dimensions = t
		default:
			logging.Errorf("%s dimensions type %T not supported", name, dimensions)
		}
		slices.Sort(dedupeKeys)
	}

	if tagExpr, ok := exprMap[fieldTags]; ok {
		// 提取tags字段
		tags, err := tagExpr.Search(data)
		if err != nil {
			return errors.Wrapf(err, "search tag expr %v failed", tagExpr)
		}

		// 转换为统一格式 [{"key": "a", "value": "b"}]
		var tagsList []map[string]interface{}
		switch t := tags.(type) {
		case map[string]interface{}:
			// 针对 tags 为 {"a": "b"} 格式的转换
			for key, value := range t {
				tagsList = append(tagsList, map[string]interface{}{"key": key, "value": value})
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
				tagsList = append(tagsList, map[string]interface{}{"key": key, "value": value})
			}
		default:
			logging.Errorf("%s tags type %T not supported", name, tags)
		}

		// 将dimensions补充到tags中
		if dimensions != nil {
			for key, value := range dimensions {
				tagsList = append(tagsList, map[string]interface{}{"key": key, "value": value})
			}
		}

		// 排序
		sort.Slice(tagsList, func(i, j int) bool {
			return tagsList[i]["key"].(string) < tagsList[j]["key"].(string)
		})

		// 推送数据
		_ = to.Put(fieldTags, tagsList)
		if len(dedupeKeys) > 0 {
			_ = to.Put(fieldDedupeKeys, dedupeKeys)
		}
	}
	return nil
}

// extractDefaultFields 从data中提取默认字段
func extractDefaultFields(to etl.Container, from etl.Container) error {
	// 数据接收时间
	ingestTime, _ := from.Get(fieldIngestTime)
	stamp, err := etl.TransformAutoTimeStamp(ingestTime)
	if err != nil {
		return errors.Wrapf(err, "transform ingest_time failed")
	}
	_ = to.Put(fieldIngestTime, stamp)

	// 插件ID
	pluginID, err := from.Get(fieldDefaultPluginID)
	if err != nil {
		return errors.Wrapf(err, "get plugin_id failed")
	}
	_ = to.Put(fieldPluginID, pluginID)

	// 清洗时间
	newTimeStamp, err := etl.TransformAutoTimeStamp(time.Now().UTC())
	if err != nil {
		return errors.Wrapf(err, "transform clean_time failed")
	}
	_ = to.Put(fieldCleanTime, newTimeStamp)

	// 如果没有设置event_id，则使用默认event_id
	eventID, _ := to.Get(fieldEventID)
	if eventID == nil || eventID == "" {
		eventID, _ = from.Get(fieldDefaultEventID)
		if eventID == nil || eventID == "" {
			return errors.Wrapf(define.ErrValue, "event_id is empty")
		}
		_ = to.Put(fieldEventID, eventID)
	}

	return nil
}

// NewAlertFTAProcessor 创建FTA告警处理器
func NewAlertFTAProcessor(ctx context.Context, name string) (*template.RecordProcessor, error) {
	pipeConfig := config.PipelineConfigFromContext(ctx)
	helper := utils.NewMapHelper(pipeConfig.Option)

	// 获取清洗配置
	configFieldKeys := map[string]string{
		"clean_configs":        fieldCleanConfig,
		"normalization_config": config.PipelineConfigOptFTAFieldMappingKey,
		"alert_config":         config.PipelineConfigOptFTAAlertsKey,
	}
	originCleanConfig := map[string]interface{}{}
	var ok bool
	for key, field := range configFieldKeys {
		originCleanConfig[key], ok = helper.Get(field)
		if !ok {
			return nil, errors.Errorf("%s %s is empty", name, field)
		}
	}

	// 初始化清洗配置
	cleanConfig, err := NewCleanConfig(originCleanConfig)
	if err != nil {
		return nil, errors.Wrapf(err, "%s create clean config failed", name)
	}

	decoder := etl.NewPayloadDecoder()
	return template.NewRecordProcessorWithDecoderFn(
		name, config.PipelineConfigFromContext(ctx),
		etl.NewFunctionalRecord("", func(from etl.Container, to etl.Container) error {
			// 捕获panic，避免不合理的配置导致程序崩溃
			defer func() {
				if err := recover(); err != nil {
					logging.Errorf("%s panic: %+v", name, err)
				}
			}()

			// 为了避免获取到外层gse补全的默认字段，因此仅获取data字段，部分内置字段后续会单独处理
			result, _ := from.Get("data")
			if result == nil {
				return nil
			}

			// 将数据转换为map格式
			var data map[string]interface{}
			switch result.(type) {
			case map[string]interface{}:
				data = result.(map[string]interface{})
			case etl.Container:
				data = etl.ContainerToMap(result.(etl.Container))
			default:
				return nil
			}

			// 获取匹配的配置
			alerts, exprMap, err := cleanConfig.GetMatchConfig(data)
			if err != nil {
				logging.Errorf("%s get match config failed: %+v", name, err)
				return nil
			}

			// 按照配置的字段表达式，提取字段，忽略字段提取错误
			rt := config.ResultTableConfigFromContext(ctx)
			_ = rt.VisitUserSpecifiedFields(func(config *config.MetaFieldConfig) error {
				// tags/dedupe_keys字段不做处理
				if config.FieldName == fieldTags || config.FieldName == fieldDedupeKeys {
					return nil
				}

				// 读取字段表达式
				expr, ok := exprMap[config.FieldName]
				if !ok {
					return nil
				}

				// 提取字段
				field, err := expr.Search(data)
				if err != nil {
					logging.Errorf("%s search expr %v failed: %+v", name, expr, err)
					return nil
				}

				// 字段类型转换
				fieldTypeTransformFn := etl.NewTransformByField(config)
				field, err = fieldTypeTransformFn(field)
				if err != nil {
					logging.Errorf("%s transform field %s failed: %+v", name, config.FieldName, err)
					return nil
				}

				_ = to.Put(config.FieldName, field)
				return nil
			})

			// 告警名称匹配
			alertName, _ := to.Get(fieldAlertName)
			if alertName == nil || alertName == "" {
				alertName, err := getMatchAlertName(alerts, data)
				if err != nil {
					logging.Errorf("%s get match alert name failed: %+v", name, err)
					return nil
				}
				if alertName == "" {
					logging.Errorf("%s alert name is empty, data->(%+v)", name, data)
					return nil
				}
				_ = to.Put(fieldAlertName, alertName)
			}

			// 默认字段处理
			if err := extractDefaultFields(to, from); err != nil {
				logging.Errorf("%s extract default fields failed: %+v", name, err)
				return nil
			}

			// 提取tags字段
			if err := extractTags(name, exprMap, data, to); err != nil {
				logging.Errorf("%s extract tags failed: %+v", name, err)
			}

			return nil
		}), decoder.Decode,
	), nil
}

func init() {
	define.RegisterDataProcessor("fta-alert", func(ctx context.Context, name string) (processor define.DataProcessor, e error) {
		pipeConfig := config.PipelineConfigFromContext(ctx)
		if pipeConfig == nil {
			return nil, errors.Wrapf(define.ErrOperationForbidden, "pipeline config is empty")
		}
		return NewAlertFTAProcessor(ctx, pipeConfig.FormatName(name))
	})
}
