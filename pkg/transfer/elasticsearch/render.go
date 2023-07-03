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
	"bytes"
	"strings"
	"text/template"
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

// TemplateRender
func TemplateRender(tmpl *template.Template, context interface{}) IndexRenderFn {
	return func(record *Record) (index string, err error) {
		buf := bytes.NewBuffer(nil)
		defer utils.RecoverError(func(e error) {
			err = e
			logging.Errorf("render index with context %#v error %v", context, e)
		})
		err = tmpl.Execute(buf, struct {
			Record  *Record
			Context interface{}
		}{
			Record:  record,
			Context: context,
		})
		return buf.String(), err
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
	timeTemplate := storageConf.GetOrDefault(
		"index_datetime_template",
		`{{ format_time ( index .Record.Document ( index .Context "field" ) ) ( index .Context "format" ) ( index .Context "timezone" ) }}`,
	).(string)
	stringTemplate := storageConf.GetOrDefault("index_template", strings.Join(
		[]string{timeTemplate, index}, separator,
	)).(string)

	tmpl, err := template.New(index).Funcs(template.FuncMap{
		"format_time": func(v interface{}, format string, timezone *time.Location) string {
			tm, err := utils.ParseTime(v)
			if err != nil {
				logging.Warnf("parse time %v error %v, use local time instead", v, err)
				tm = time.Now()
			}
			return tm.In(timezone).Format(format)
		},
	}).Parse(stringTemplate)
	if err != nil {
		return nil, err
	}

	return TemplateRender(tmpl, map[string]interface{}{
		"field":    field,
		"format":   format,
		"timezone": utils.ParseFixedTimeZone(timezone),
	}), nil
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
		values, ok := record.Document.(map[string]interface{})
		if !ok {
			return "", errors.Wrapf(define.ErrType, "document type %T", record.Document)
		}

		value, ok := values[field]
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
