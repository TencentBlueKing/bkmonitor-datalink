// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package spanprocessor

const (
	LinkAnd = "and"
	LinkOr  = "or"
)

type Config struct {
	Drop         []DropAction         `config:"drop" mapstructure:"drop"`
	ReplaceValue []ReplaceValueAction `config:"replace_value" mapstructure:"replace_value"`
}

type DropAction struct {
	MatchRules []MatchRule `config:"match_rules" mapstructure:"match_rules"`
}

type MatchRule struct {
	Key   string   `config:"key" mapstructure:"key"`
	Op    string   `config:"op" mapstructure:"op"`
	Value []string `config:"value" mapstructure:"value"`
	Link  string   `config:"link" mapstructure:"link"`
}

type ReplaceValueAction struct {
	PredicateKey string         `config:"predicate_key" mapstructure:"predicate_key"`
	Rules        []ReplaceRules `config:"rules" mapstructure:"rules"`
}

type ReplaceRules struct {
	Filters []MatchRule `config:"filters" mapstructure:"filters"`
	From    ReplaceFrom `config:"replace_from" mapstructure:"replace_from"`
}

type ReplaceFrom struct {
	Source    []string `config:"source" mapstructure:"source"`
	Separator string   `config:"separator" mapstructure:"separator"`
	ConstVal  string   `config:"const_val" mapstructure:"const_val"`
}
