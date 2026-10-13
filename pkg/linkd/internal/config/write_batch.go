// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "fmt"

// ElasticsearchWriteBatchConfig 只允许配置启停与字节预算；调度参数从 Lifecycle 并发推导。
type ElasticsearchWriteBatchConfig struct {
	// Enabled 关闭时保留原同步接口，可用于诊断对照。
	Enabled *bool `yaml:"enabled"`
	// MaxBytes 是独立于并发的编码字节预算。
	MaxBytes int `yaml:"max_bytes"`
	// MaxOperations 是派生的单批文档操作上限，不接受 YAML 手动覆盖。
	MaxOperations int `yaml:"-"`
	// WaitMilliseconds 是首项最大聚合等待；达到数量或字节预算可提前发送。
	WaitMilliseconds int `yaml:"-"`
	// ReadWaitMilliseconds 为实时点读提供短收集窗口；满批提前发出，不等待写批次期限。
	ReadWaitMilliseconds int `yaml:"-"`
	// MaxConcurrentBatches 跟随 Lifecycle 并发，不叠加独立的固定批次封顶。
	MaxConcurrentBatches int `yaml:"-"`
}

// WithDefaults 返回独立副本，每次根据当前并发重新推导，防止并发变更后残留旧派生值。
// 这是保守的启动期策略，不代表已根据 ES 实际能力完成在线自适应调优。
func (c ElasticsearchWriteBatchConfig) WithDefaults(concurrency int) ElasticsearchWriteBatchConfig {
	enabled := true
	if c.Enabled != nil {
		enabled = *c.Enabled
	}
	c.Enabled = &enabled
	// 单批最多占一半调用方，并封顶 128 项。超过该值后继续放大批次会让
	// 更多依赖步骤等待同一个响应，并在高负载下放大解析、GC 和调度延迟。
	c.MaxOperations = min(128, max(1, concurrency/2))
	// 收集窗口固定为 10ms，不随处理并发扩大；单项批次无需等待新项。
	c.WaitMilliseconds = 10
	if c.MaxOperations == 1 {
		c.WaitMilliseconds = 0
	}
	// 读写独立收集，使用相同期限；每个聚合调用最多占一个批次执行名额。
	c.ReadWaitMilliseconds = c.WaitMilliseconds
	c.MaxConcurrentBatches = max(1, concurrency)
	if c.MaxBytes == 0 {
		c.MaxBytes = 4 << 20
	}
	return c
}

// Validate 校验物理批次上限；排队调用数由 Lifecycle concurrency 派生。
func (c ElasticsearchWriteBatchConfig) Validate() error {
	if c.MaxBytes != 0 && (c.MaxBytes < 1<<20 || c.MaxBytes > 16<<20) {
		return fmt.Errorf("lifecycle.elasticsearch_write_batch.max_bytes must be 1048576..16777216")
	}
	return nil
}
