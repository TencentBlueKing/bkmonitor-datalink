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
)

type Config struct {
	Drop     DropAction       `config:"drop" mapstructure:"drop"`
	Assemble []AssembleAction `config:"assemble" mapstructure:"assemble"`
	Replace  []ReplaceAction  `config:"replace" mapstructure:"replace"`
	Add      []AddAction      `config:"add" mapstructure:"add"`
}

func (c *Config) cleanResourcePrefix(keys []string) []string {
	const prefix = "resource."
	var ret []string
	for _, key := range keys {
		if strings.HasPrefix(key, prefix) {
			ret = append(ret, key[len(prefix):])
		}
	}
	return ret
}

func (c *Config) Clean() {
	c.Drop.Keys = c.cleanResourcePrefix(c.Drop.Keys)
	for i := 0; i < len(c.Assemble); i++ {
		c.Assemble[i].Keys = c.cleanResourcePrefix(c.Assemble[i].Keys)
	}
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
