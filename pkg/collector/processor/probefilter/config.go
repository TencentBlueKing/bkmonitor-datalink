// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package probefilter

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/fields"
)

type Config struct {
	AddAttrs []AddAttrsAction `config:"add_attributes" mapstructure:"add_attributes"`
}

func (c *Config) Clean() {
	for i := 0; i < len(c.AddAttrs); i++ {
		for j := 0; j < len(c.AddAttrs[i].Rules); j++ {
			for k := 0; k < len(c.AddAttrs[i].Rules[j].Filters); k++ {
				c.AddAttrs[i].Rules[j].Filters[k].Clean()
			}
		}
	}
}

type AddAttrsAction struct {
	Rules []Rule `config:"rules" mapstructure:"rules"`
}

type Rule struct {
	Type    string   `config:"type" mapstructure:"type"`
	Enabled bool     `config:"enabled" mapstructure:"enabled"`
	Target  string   `config:"target" mapstructure:"target"`
	Field   string   `config:"field" mapstructure:"field"`
	Prefix  string   `config:"prefix" mapstructure:"prefix"`
	Filters []Filter `config:"filters" mapstructure:"filters"`
}

type Filter struct {
	Field string `config:"field" mapstructure:"field"`
	Value string `config:"value" mapstructure:"value"`
	Type  string `config:"type" mapstructure:"type"`
}

func (f *Filter) Clean() {
	f.Field = fields.TrimPrefix(f.Field)
}
