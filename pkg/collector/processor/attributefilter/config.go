// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package attributefilter

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/fields"
)

type Config struct {
	AsString  AsStringAction   `config:"as_string" mapstructure:"as_string"`
	AsInt     AsIntAction      `config:"as_int" mapstructure:"as_int"`
	FromToken FromTokenAction  `config:"from_token" mapstructure:"from_token"`
	Assemble  []AssembleAction `config:"assemble" mapstructure:"assemble"`
	Drop      []DropAction     `config:"drop" mapstructure:"drop"`
	Cut       []CutAction      `config:"cut" mapstructure:"cut"`
}

func (c *Config) Clean() {
	c.AsString.Clean()
	c.AsInt.Clean()
	for i := 0; i < len(c.Assemble); i++ {
		c.Assemble[i].Clean()
	}

	for i := 0; i < len(c.Drop); i++ {
		c.Drop[i].Clean()
	}

	for i := 0; i < len(c.Cut); i++ {
		c.Cut[i].Clean()
	}
}

type AsStringAction struct {
	Keys []string `config:"keys" mapstructure:"keys"`
}

func (c *AsStringAction) Clean() {
	c.Keys = fields.TrimAttributesPrefix(c.Keys...)
}

type AsIntAction struct {
	Keys []string `config:"keys" mapstructure:"keys"`
}

func (c *AsIntAction) Clean() {
	c.Keys = fields.TrimAttributesPrefix(c.Keys...)
}

type FromTokenAction struct {
	BizId   string `config:"biz_id" mapstructure:"biz_id"`
	AppName string `config:"app_name" mapstructure:"app_name"`
}

type AssembleRule struct {
	Kind        string   `config:"kind" mapstructure:"kind"`               // 所需 kind 的类型，不需要则为空
	Keys        []string `config:"keys" mapstructure:"keys"`               // 所需获取的源字段
	Separator   string   `config:"separator" mapstructure:"separator"`     // 分隔符
	FirstUpper  []string `config:"first_upper" mapstructure:"first_upper"` // 需要首字母大写的属性
	Placeholder string   `config:"placeholder" mapstructure:"placeholder"` // 占位符

	upper map[string]struct{}
}

func (c *AssembleRule) Clean() {
	c.Keys = fields.TrimAttributesPrefix(c.Keys...)
	c.FirstUpper = fields.TrimAttributesPrefix(c.FirstUpper...)

	c.upper = make(map[string]struct{})
	for _, s := range c.FirstUpper {
		c.upper[s] = struct{}{}
	}
}

type AssembleAction struct {
	Destination  string         `config:"destination" mapstructure:"destination"`     // 需要插入的字段
	PredicateKey string         `config:"predicate_key" mapstructure:"predicate_key"` // 需要匹配的字段
	Rules        []AssembleRule `config:"rules" mapstructure:"rules"`
	DefaultFrom  string         `config:"default_from" mapstructure:"default_from"` // 默认值字段
}

func (c *AssembleAction) Clean() {
	c.PredicateKey = fields.TrimAttributesPrefix(c.PredicateKey).String()
	c.DefaultFrom = fields.TrimAttributesPrefix(c.DefaultFrom).String()
	for i := 0; i < len(c.Rules); i++ {
		c.Rules[i].Clean()
	}
}

type DropAction struct {
	PredicateKey string   `config:"predicate_key" mapstructure:"predicate_key"`
	Match        []string `config:"match" mapstructure:"match"`
	Keys         []string `config:"keys" mapstructure:"keys"`

	match map[string]struct{}
}

func (c *DropAction) Clean() {
	c.PredicateKey = fields.TrimAttributesPrefix(c.PredicateKey).String()
	c.Match = fields.TrimAttributesPrefix(c.Match...)
	c.Keys = fields.TrimAttributesPrefix(c.Keys...)

	c.match = make(map[string]struct{})
	for _, s := range c.Match {
		c.match[s] = struct{}{}
	}
}

type CutAction struct {
	PredicateKey string   `config:"predicate_key" mapstructure:"predicate_key"`
	Match        []string `config:"match" mapstructure:"match"`
	MaxLength    int      `config:"max_length" mapstructure:"max_length"`
	Keys         []string `config:"keys" mapstructure:"keys"`

	match map[string]struct{}
}

func (c *CutAction) Clean() {
	c.PredicateKey = fields.TrimAttributesPrefix(c.PredicateKey).String()
	c.Match = fields.TrimAttributesPrefix(c.Match...)
	c.Keys = fields.TrimAttributesPrefix(c.Keys...)

	c.match = make(map[string]struct{})
	for _, s := range c.Match {
		c.match[s] = struct{}{}
	}
}
