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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/processor/resourcefilter/k8scache"
)

type Config struct {
	Drop       DropAction       `config:"drop" mapstructure:"drop"`
	FromCache  FromCacheAction  `config:"from_cache" mapstructure:"from_cache"`
	Assemble   []AssembleAction `config:"assemble" mapstructure:"assemble"`
	Replace    []ReplaceAction  `config:"replace" mapstructure:"replace"`
	Add        []AddAction      `config:"add" mapstructure:"add"`
	FromRecord []FromRecord     `config:"from_record" mapstructure:"from_record"`
}

func (c *Config) Clean() {
	c.Drop.Keys = cleanResourcesPrefix(c.Drop.Keys)
	for i := 0; i < len(c.Assemble); i++ {
		c.Assemble[i].Keys = cleanResourcesPrefix(c.Assemble[i].Keys)
	}
	for i := 0; i < len(c.FromRecord); i++ {
		c.FromRecord[i].Destination = cleanResourcePrefix(c.FromRecord[i].Destination)
	}

	keys := strings.Split(c.FromCache.Key, "|")
	for i := 0; i < len(keys); i++ {
		keys[i] = cleanResourcePrefix(keys[i])
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
	Key   string          `config:"key" mapstructure:"key"`
	Cache k8scache.Config `config:"cache" mapstructure:"cache"`

	keys []string
}

func (a FromCacheAction) CombineKeys() []string {
	return a.keys
}

type FromRecord struct {
	Source      string `config:"source" mapstructure:"source"`
	Destination string `config:"destination" mapstructure:"destination"`
}

func cleanResourcesPrefix(keys []string) []string {
	const prefix = "resource."
	var ret []string
	for _, key := range keys {
		if strings.HasPrefix(key, prefix) {
			ret = append(ret, key[len(prefix):])
		}
	}
	return ret
}

func cleanResourcePrefix(key string) string {
	const prefix = "resource."
	if strings.HasPrefix(key, prefix) {
		return key[len(prefix):]
	}
	return key
}
