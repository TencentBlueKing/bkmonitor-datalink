// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package taskgroup

import (
	"fmt"
	"runtime"
)

// WorkConfig 是后台持久待办的执行预算，不限制数据库中的待办总量。
// 补建与发送各自使用此预算，避免慢发送占满新任务入队名额。
type WorkConfig struct {
	// Concurrency 为每个循环的并发，0 按有效 CPU 数的四倍推导，至少 16。
	Concurrency int `yaml:"concurrency" json:"concurrency"`
	// BatchSize 是单页扫描大小，0 使用并发数的两倍；不是积压上限。
	BatchSize int `yaml:"batch_size" json:"batch_size"`
}

// WithDefaults 依据 Go 的容器 CPU 配额感知结果生成默认预算。
func (c WorkConfig) WithDefaults() WorkConfig {
	if c.Concurrency == 0 {
		c.Concurrency = max(16, runtime.GOMAXPROCS(0)*4)
	}
	if c.BatchSize == 0 {
		c.BatchSize = max(64, c.Concurrency*2)
	}
	return c
}

// Validate 保留单页内存预算；超过单页数量的并发不会增益，调用方应调大批次。
func (c WorkConfig) Validate() error {
	c = c.WithDefaults()
	if c.Concurrency < 1 || c.BatchSize < 1 || c.Concurrency > c.BatchSize {
		return fmt.Errorf("work budget requires 0 < concurrency <= batch_size")
	}
	return nil
}
