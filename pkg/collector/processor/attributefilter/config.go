// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package attributefilter

import "strings"

type Config struct {
	AsString  AsStringAction   `config:"as_string" mapstructure:"as_string"`
	AsInt     AsIntAction      `config:"as_int" mapstructure:"as_int"`
	FromToken FromTokenAction  `config:"from_token" mapstructure:"from_token"`
	Assemble  []AssembleAction `config:"assemble" mapstructure:"assemble"`
}

type AsStringAction struct {
	Keys []string `config:"keys" mapstructure:"keys"`
}

type AsIntAction struct {
	Keys []string `config:"keys" mapstructure:"keys"`
}

type FromTokenAction struct {
	BizId   string `config:"biz_id" mapstructure:"biz_id"`
	AppName string `config:"app_name" mapstructure:"app_name"`
}

type AssembleAction struct {
	Destination  string `config:"destination" mapstructure:"destination"`     // 需要插入的字段
	PredicateKey string `config:"predicate_key" mapstructure:"predicate_key"` // 需要匹配的字段
	Rules        []Rule `config:"rules" mapstructure:"rules"`
}

type Rule struct {
	Kind       string   `config:"kind" mapstructure:"kind"`               // 所需 kind 的类型，不需要则为空
	Keys       []string `config:"keys" mapstructure:"keys"`               // 所需获取的源字段
	Separator  string   `config:"separator" mapstructure:"separator"`     // 分隔符
	FirstUpper []string `config:"first_upper" mapstructure:"first_upper"` // 需要首字母大写的属性
}

const (
	attrPrefix  = "attributes."
	constPrefix = "const."
)

func (c *Config) cleanAttributesPrefixes(keys []string) []string {
	var ret []string
	for _, key := range keys {
		if strings.HasPrefix(key, attrPrefix) {
			ret = append(ret, key[len(attrPrefix):])
		} else {
			ret = append(ret, key)
		}
	}
	return ret
}

func (c *Config) cleanAttributesPrefix(s string) string {
	if !strings.HasPrefix(s, attrPrefix) {
		return s
	}
	return s[len(attrPrefix):]
}

func (c *Config) Clean() {
	c.AsString.Keys = c.cleanAttributesPrefixes(c.AsString.Keys)
	c.AsInt.Keys = c.cleanAttributesPrefixes(c.AsInt.Keys)
	for i := 0; i < len(c.Assemble); i++ {
		match := c.Assemble[i].PredicateKey
		c.Assemble[i].PredicateKey = c.cleanAttributesPrefix(match)
		for j := 0; j < len(c.Assemble[i].Rules); j++ {
			keys := c.Assemble[i].Rules[j].Keys
			upper := c.Assemble[i].Rules[j].FirstUpper
			c.Assemble[i].Rules[j].Keys = c.cleanAttributesPrefixes(keys)
			c.Assemble[i].Rules[j].FirstUpper = c.cleanAttributesPrefixes(upper)
		}
	}
}
