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
	Tars *TarsConfig `config:"tars" mapstructure:"tars"`
}

type TarsConfig struct {
	DisableAggregate  bool          `config:"disable_aggregate" mapstructure:"disable_aggregate"`
	IsDropOriginal    bool          `config:"is_drop_original" mapstructure:"is_drop_original"`
	AggregateInterval time.Duration `config:"aggregate_interval" mapstructure:"aggregate_interval"`
	TagIgnores        []TagIgnore   `config:"tag_ignores" mapstructure:"tag_ignores"`
}

func (c *TarsConfig) Validate() {
	if c.AggregateInterval <= 0 {
		c.AggregateInterval = defaultAggregateInterval
	}
	if len(c.TagIgnores) == 0 {
		c.TagIgnores = []TagIgnore{
			{ScopeName: "server_metrics", Tags: []string{"caller_ip"}},
			{ScopeName: "client_metrics", Tags: []string{"callee_ip"}},
		}
	}
}

type TagIgnore struct {
	ScopeName string   `config:"scope_name" mapstructure:"scope_name"`
	Tags      []string `config:"tags" mapstructure:"tags"`
}
