// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fieldnormalizer

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/fields"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/mapstrings"
)

type Config struct {
	Fields []FieldConfig `config:"fields" mapstructure:"fields"`
}

type FieldConfig struct {
	Kind         string      `config:"kind" mapstructure:"kind"`
	PredicateKey string      `config:"predicate_key" mapstructure:"predicate_key"`
	Rules        []FieldRule `config:"rules" mapstructure:"rules"`
}

type FieldRule struct {
	Key    string   `config:"key" mapstructure:"key"`
	Values []string `config:"values" mapstructure:"values"`
	Op     string   `config:"op" mapstructure:"op"`
}

type ConfigHandler struct {
	predicateKeys *mapstrings.MapStrings // key:[kind]
	attributeKeys *mapstrings.MapStrings // key:[kind+predicateKey]
}

func NewConfigHandler(config Config) *ConfigHandler {
	predicateKeys := mapstrings.New(mapstrings.OrderDesc)
	attributeKeys := mapstrings.New(mapstrings.OrderNone)

	for i := 0; i < len(config.Fields); i++ {
		field := config.Fields[i]
		predicateKeys.Set(field.Kind, field.PredicateKey)
		for j := 0; j < len(field.Rules); j++ {
			rule := field.Rules[j]
			id := field.Kind + "/" + field.PredicateKey
			ff, v := fields.DecodeFieldFrom(rule.Key)
			switch ff {
			case fields.FieldFromAttributes:
				attributeKeys.Set(id, v)
			default:
			}
		}
	}

	return &ConfigHandler{
		predicateKeys: predicateKeys,
		attributeKeys: attributeKeys,
	}
}

func (ch *ConfigHandler) GetPredicateKeys(kind string) []string {
	keys := ch.predicateKeys.Get(kind)

	// 使用兜底配置
	if len(keys) == 0 {
		keys = ch.predicateKeys.Get("")
	}
	return keys
}

func (ch *ConfigHandler) GetAttributes(kind, predicateKey string) []string {
	keys := ch.attributeKeys.Get(kind + "/" + predicateKey)

	// 使用兜底配置
	if len(keys) == 0 && predicateKey == "" {
		return ch.attributeKeys.Get("/" + predicateKey)
	}
	return keys
}
