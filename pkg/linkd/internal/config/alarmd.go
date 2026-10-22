// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
	"net/url"
	"strings"
)

// AlarmdResource 绑定 alarmd 来源和 control 内部关闭接口，不进入 EventSource Release。
type AlarmdResource struct {
	// EventSourceID 精确匹配原始 Alert 的来源，缺省为 KAC 内置监控来源 built_in_bk。
	EventSourceID string `yaml:"event_source_id,omitempty" json:"event_source_id,omitempty"`
	// NoDataCloseEndpoint 是完整 POST 地址；空值关闭联动，接口无需凭据。
	NoDataCloseEndpoint string `yaml:"no_data_close_endpoint,omitempty" json:"no_data_close_endpoint,omitempty"`
	// TimeoutMilliseconds 限制单次调用，缺省 2000，仍服从关闭请求的剩余预算。
	TimeoutMilliseconds int64 `yaml:"timeout_milliseconds,omitempty" json:"timeout_milliseconds,omitempty"`
}

// WithDefaults 返回默认渠道及请求超时，不修改原配置。
func (c AlarmdResource) WithDefaults() AlarmdResource {
	if c.EventSourceID == "" {
		c.EventSourceID = "built_in_bk"
	}
	if c.TimeoutMilliseconds == 0 {
		c.TimeoutMilliseconds = 2000
	}
	return c
}

// Validate 只校验已启用联动的配置；地址为空时其他字段不参与运行。
func (c AlarmdResource) Validate() error {
	if c.NoDataCloseEndpoint == "" {
		return nil
	}
	c = c.WithDefaults()
	endpoint, err := url.Parse(c.NoDataCloseEndpoint)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return fmt.Errorf("no_data_close_endpoint must be an HTTP(S) URL without credentials, query or fragment")
	}
	if strings.TrimSpace(c.EventSourceID) != c.EventSourceID {
		return fmt.Errorf("event_source_id must not contain surrounding whitespace")
	}
	if c.TimeoutMilliseconds < 1 || c.TimeoutMilliseconds > 10000 {
		return fmt.Errorf("timeout_milliseconds must be between 1 and 10000")
	}
	return nil
}
