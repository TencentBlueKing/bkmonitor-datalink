// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package dbfilter

import "time"

type Config struct {
	SlowQueryRules []SlowQueryRule `config:"slow_query" mapstructure:"slow_query"`

	slowQueryRules map[string]time.Duration
}

type SlowQueryRule struct {
	Match     string        `config:"match" mapstructure:"match"`
	Threshold time.Duration `config:"threshold" mapstructure:"threshold"`
}

func (c *Config) Setup() {
	c.slowQueryRules = make(map[string]time.Duration)
	for _, rule := range c.SlowQueryRules {
		c.slowQueryRules[rule.Match] = rule.Threshold
	}
}

func (c *Config) GetSlowQueryConf(s string) (time.Duration, bool) {
	v, ok := c.slowQueryRules[s]
	if ok {
		return v, true
	}
	// 空值兜底
	v, ok = c.slowQueryRules[""]
	if ok {
		return v, true
	}
	return 0, false
}
