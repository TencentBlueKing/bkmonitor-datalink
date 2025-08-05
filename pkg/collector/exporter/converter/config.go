// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"time"
)

const defaultAggregateInterval = 200 * time.Millisecond

type Config struct {
	Tars TarsConfig `config:"tars"`
}

func (c *Config) Validate() {
	if c.Tars.AggregateInterval <= 0 {
		c.Tars.AggregateInterval = defaultAggregateInterval
	}
	if len(c.Tars.TagIgnores) == 0 {
		c.Tars.TagIgnores = []TagIgnore{
			{"server_metrics", []string{"caller_ip"}},
			{"client_metrics", []string{"callee_ip"}},
		}
	}
}

type TarsConfig struct {
	IsDropOriginal    bool          `config:"is_drop_original"`
	AggregateInterval time.Duration `config:"aggregate_interval"`
	TagIgnores        []TagIgnore   `config:"tag_ignores" mapstructure:"tag_ignores"`
}

type TagIgnore struct {
	ScopeName string   `json:"scope_name"`
	Tags      []string `json:"tags"`
}
