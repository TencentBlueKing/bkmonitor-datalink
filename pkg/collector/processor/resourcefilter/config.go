// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package resourcefilter

import (
	"strings"

	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/fields"
)

type Config struct {
	Drop         DropAction           `config:"drop" mapstructure:"drop"`
	FromCache    FromCacheAction      `config:"from_cache" mapstructure:"from_cache"`
	FromMetadata FromMetadataAction   `config:"from_metadata" mapstructure:"from_metadata"`
	FromToken    FromTokenAction      `config:"from_token" mapstructure:"from_token"`
	Assemble     []AssembleAction     `config:"assemble" mapstructure:"assemble"`
	Replace      []ReplaceAction      `config:"replace" mapstructure:"replace"`
	Add          []AddAction          `config:"add" mapstructure:"add"`
	FromRecord   []FromRecordAction   `config:"from_record" mapstructure:"from_record"`
	DefaultValue []DefaultValueAction `config:"default_value" mapstructure:"default_value"`
}

func (c *Config) Clean() {
	c.Drop.Keys = fields.TrimResourcePrefix(c.Drop.Keys...)
	for i := 0; i < len(c.Assemble); i++ {
		c.Assemble[i].Keys = fields.TrimResourcePrefix(c.Assemble[i].Keys...)
	}
	for i := 0; i < len(c.FromRecord); i++ {
		c.FromRecord[i].Destination = fields.TrimResourcePrefix(c.FromRecord[i].Destination).String()
	}
	for i := 0; i < len(c.DefaultValue); i++ {
		c.DefaultValue[i].Key = fields.TrimResourcePrefix(c.DefaultValue[i].Key).String()
	}

	keys := strings.Split(c.FromCache.Key, "|")
	for i := 0; i < len(keys); i++ {
		keys[i] = fields.TrimResourcePrefix(keys[i]).String()
	}
	c.FromCache.keys = keys
}

type DropAction struct {
	Keys []string `config:"keys" mapstructure:"keys"`
}

type ReplaceAction struct {
	Source      string `config:"source" mapstructure:"source"`
	Destination string `config:"destination" mapstructure:"destination"`
}

type AddAction struct {
	Label string `config:"label" mapstructure:"label"`
	Value string `config:"value" mapstructure:"value"`
}

type AssembleAction struct {
	Destination string   `config:"destination" mapstructure:"destination"`
	Separator   string   `config:"separator" mapstructure:"separator"`
	Keys        []string `config:"keys" mapstructure:"keys"`
}

type FromCacheAction struct {
	Key   string `config:"key" mapstructure:"key"`
	Cache string `config:"cache" mapstructure:"cache"`

	keys []string
}

func (a FromCacheAction) CombineKeys() []string {
	return a.keys
}

type FromRecordAction struct {
	Source      string `config:"source" mapstructure:"source"`
	Destination string `config:"destination" mapstructure:"destination"`
}

type FromMetadataAction struct {
	Keys []string `config:"keys" mapstructure:"keys"`
}

type FromTokenAction struct {
	Keys []string `config:"keys" mapstructure:"keys"`
}

type DefaultValueAction struct {
	Key   string `config:"key" mapstructure:"key"`
	Type  string `config:"type" mapstructure:"type"`
	Value any    `config:"value" mapstructure:"value"`
}

func (d DefaultValueAction) StringValue() string {
	return cast.ToString(d.Value)
}

func (d DefaultValueAction) IntValue() int {
	return cast.ToInt(d.Value)
}

func (d DefaultValueAction) BoolValue() bool {
	return cast.ToBool(d.Value)
}
