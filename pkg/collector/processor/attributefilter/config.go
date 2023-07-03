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
	AsString  AsStringAction  `config:"as_string" mapstructure:"as_string"`
	FromToken FromTokenAction `config:"from_token" mapstructure:"from_token"`
}

type AsStringAction struct {
	Keys []string `config:"keys" mapstructure:"keys"`
}

type FromTokenAction struct {
	BizId   string `config:"biz_id" mapstructure:"biz_id"`
	AppName string `config:"app_name" mapstructure:"app_name"`
}

func (c *Config) cleanAttributesPrefix(keys []string) []string {
	const prefix = "attributes."
	var ret []string
	for _, key := range keys {
		if strings.HasPrefix(key, prefix) {
			ret = append(ret, key[len(prefix):])
		}
	}
	return ret
}

func (c *Config) Clean() {
	c.AsString.Keys = c.cleanAttributesPrefix(c.AsString.Keys)
}
