// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/utils"
)

// FixedIndexRender
func FixedIndexRender(name string) IndexRenderFn {
	return func(record *Record) (s string, e error) {
		return name, nil
	}
}

// ConfigTemplateRender
func ConfigTemplateRender(config *config.ElasticSearchMetaClusterInfo) (IndexRenderFn, error) {
	storageConf := utils.NewMapHelper(config.StorageConfig)
	index := config.GetIndex()

	separator := storageConf.GetOrDefault("index_template_separator", "_").(string)
	field := storageConf.GetOrDefault("index_datetime_field", "time").(string)
	timezone := int(storageConf.GetOrDefault("index_datetime_timezone", 0.0).(float64))
	format := storageConf.GetOrDefault("index_datetime_format", "20060102").(string)

	return func(record *Record) (string, error) {
		tm, err := utils.ParseTime(record.Document[field])
		if err != nil {
			logging.Warnf("parse time %v error %v, use local time instead", tm, err)
			tm = time.Now()
		}

		s := tm.In(utils.ParseFixedTimeZone(timezone)).Format(format) + separator + index
		return s, nil
	}, nil
}

// TimeBasedIndexAliasRender
func TimeBasedIndexAliasRender(config *config.ElasticSearchMetaClusterInfo) (IndexRenderFn, error) {
	storageConf := utils.NewMapHelper(config.StorageConfig)
	field := storageConf.GetOrDefault("index_datetime_field", "time").(string)
	timezone := utils.ParseFixedTimeZone(int(storageConf.GetOrDefault("index_datetime_timezone", 0.0).(float64)))
	alias, ok := storageConf.GetString("index_alias_template")
	if !ok {
		return nil, errors.Wrapf(define.ErrKey, "index_alias_format not found")
	}

	return func(record *Record) (s string, e error) {
		value, ok := record.Document[field]
		if !ok {
			return "", errors.Wrapf(define.ErrKey, "document field %s not found", field)
		}

		tm, err := utils.ParseTime(value)
		if err != nil {
			return "", errors.Wrapf(define.ErrValue, "parse %s(%v) to time error %v", field, tm, err)
		}

		return tm.In(timezone).Format(alias), nil
	}, nil
}
