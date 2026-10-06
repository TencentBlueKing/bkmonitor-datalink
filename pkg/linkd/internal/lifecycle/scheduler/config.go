// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"fmt"
	"time"
)

// Config 定义 fingerprint lease 的时间边界和竞争退避。
type Config struct {
	LockKeyPrefix  string
	LockTTL        time.Duration
	RenewInterval  time.Duration
	LockRetryDelay time.Duration
	ReleaseTimeout time.Duration
	MaxDrainEvents int
}

// DefaultConfig 返回适合首版生命周期调度的保守默认值。
func DefaultConfig() Config {
	return Config{
		LockKeyPrefix:  "linkd:lifecycle:lock",
		LockTTL:        60 * time.Second,
		RenewInterval:  20 * time.Second,
		LockRetryDelay: 500 * time.Millisecond,
		ReleaseTimeout: 3 * time.Second,
		MaxDrainEvents: 512,
	}
}

// Validate 校验 lease 能在过期前至少获得两次续租机会。
func (c Config) Validate() error {
	if c.LockKeyPrefix == "" {
		return fmt.Errorf("lock_key_prefix must not be empty")
	}
	for name, value := range map[string]time.Duration{
		"lock_ttl":         c.LockTTL,
		"renew_interval":   c.RenewInterval,
		"lock_retry_delay": c.LockRetryDelay,
		"release_timeout":  c.ReleaseTimeout,
	} {
		if value <= 0 {
			return fmt.Errorf("%s must be positive: %s", name, value)
		}
	}
	if c.RenewInterval*2 >= c.LockTTL {
		return fmt.Errorf("renew_interval must be less than half of lock_ttl")
	}
	if c.MaxDrainEvents < 1 {
		return fmt.Errorf("max_drain_events must be positive: %d", c.MaxDrainEvents)
	}
	return nil
}
