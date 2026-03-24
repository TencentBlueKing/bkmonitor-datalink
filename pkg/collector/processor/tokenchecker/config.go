// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tokenchecker

import (
	"strings"
)

type Config struct {
	Type         string   `config:"type" mapstructure:"type"`
	ResourceKey  string   `config:"resource_key" mapstructure:"resource_key"`
	resourceKeys []string // 避免多次转换开销

	// type: aes256
	Salt       string `config:"salt" mapstructure:"salt"`
	Version    string `config:"version" mapstructure:"version"`
	DecodedKey string `config:"decoded_key" mapstructure:"decoded_key"`
	DecodedIv  string `config:"decoded_iv" mapstructure:"decoded_iv"`

	// type: fixed
	MustEmptyToken bool   `config:"must_empty_token" mapstructure:"must_empty_token"`
	FixedToken     string `config:"fixed_token" mapstructure:"fixed_token"`
	TracesDataId   int32  `config:"traces_dataid" mapstructure:"traces_dataid"`
	MetricsDataId  int32  `config:"metrics_dataid" mapstructure:"metrics_dataid"`
	LogsDataId     int32  `config:"logs_dataid" mapstructure:"logs_dataid"`
	ProfilesDataId int32  `config:"profiles_dataid" mapstructure:"profiles_dataid"`
	BizId          int32  `config:"bk_biz_id" mapstructure:"bk_biz_id"`
	AppName        string `config:"bk_app_name" mapstructure:"bk_app_name"`

	// type: proxy
	ProxyDataId int32  `config:"dataid" mapstructure:"proxy_dataid"`
	ProxyToken  string `config:"token" mapstructure:"proxy_token"`
}

func (c *Config) Clean() {
	var keys []string
	for _, key := range strings.Split(c.ResourceKey, ",") {
		keys = append(keys, strings.TrimSpace(key))
	}
	c.resourceKeys = keys
}

func (c *Config) GetType() []string {
	// 版本转换处理
	if c.Type == decoderTypeAes256 && c.Version == "v2" {
		return []string{decoderTypeAes256WithMeta, decoderTypeFixed}
	}
	return []string{c.Type}
}
