// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package methodfilter

import (
	"strings"
)

type Config struct {
	DropSpan DropSpanAction `config:"drop_span" mapstructure:"drop_span"`
}

type DropSpanAction struct {
	Rules []*Rule `config:"rules" mapstructure:"rules"`
}

type Rule struct {
	PredicateKey string      `config:"predicate_key" mapstructure:"predicate_key"`
	Kind         string      `config:"kind" mapstructure:"kind"`
	MatchConfig  MatchConfig `config:"match" mapstructure:"match"`
}

type MatchConfig struct {
	Op    string `config:"op" mapstructure:"op"`
	Value string `config:"value" mapstructure:"value"`
}

type ConfigHandler struct {
	dropSpanRules map[string][]*Rule
}

func NewConfigHandler(c *Config) *ConfigHandler {
	dropSpanRules := make(map[string][]*Rule)
	if c == nil || c.DropSpan.Rules == nil {
		return &ConfigHandler{dropSpanRules: dropSpanRules}
	}
	for _, r := range c.DropSpan.Rules {
		kinds := strings.Split(r.Kind, ",")
		for _, kind := range kinds {
			dropSpanRules[kind] = append(dropSpanRules[kind], r)
		}
	}

	return &ConfigHandler{dropSpanRules: dropSpanRules}
}

func (ch *ConfigHandler) Get(kind string) []*Rule {
	return ch.dropSpanRules[kind]
}
