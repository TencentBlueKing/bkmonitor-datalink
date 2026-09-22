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

// ActiveIndexConfig 配置控制面策略缓存投影；有对应 Hook 时默认启用。
type ActiveIndexConfig struct {
	PollIntervalSeconds      int `yaml:"poll_interval_seconds"`
	ReconcileIntervalSeconds int `yaml:"reconcile_interval_seconds"`
	OperationTimeoutSeconds  int `yaml:"operation_timeout_seconds"`
	BatchSize                int `yaml:"batch_size"`
	MaxRows                  int `yaml:"max_rows"`
	MaxBytes                 int `yaml:"max_bytes"`
}

// WithDefaults 返回有界资源配置；全量校准恢复未成功提交的 Hook 提示。
func (c ActiveIndexConfig) WithDefaults() ActiveIndexConfig {
	if c.PollIntervalSeconds == 0 {
		c.PollIntervalSeconds = 1
	}
	if c.ReconcileIntervalSeconds == 0 {
		c.ReconcileIntervalSeconds = 60
	}
	if c.OperationTimeoutSeconds == 0 {
		c.OperationTimeoutSeconds = 10
	}
	if c.BatchSize == 0 {
		c.BatchSize = 16
	}
	if c.MaxRows == 0 {
		c.MaxRows = 100000
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 32 << 20
	}
	return c
}

// Validate 限制单目标吞吐与内存；不允许无限扫描或无限重试。
func (c ActiveIndexConfig) Validate() error {
	c = c.WithDefaults()
	if c.PollIntervalSeconds < 1 || c.PollIntervalSeconds > 60 || c.ReconcileIntervalSeconds < c.PollIntervalSeconds || c.ReconcileIntervalSeconds > 3600 || c.OperationTimeoutSeconds < 1 || c.OperationTimeoutSeconds > 60 || c.BatchSize < 1 || c.BatchSize > 100 || c.MaxRows < 1 || c.MaxRows > 1000000 || c.MaxBytes < 1024 || c.MaxBytes > 64<<20 {
		return fmt.Errorf("invalid active_index limits")
	}
	return nil
}
