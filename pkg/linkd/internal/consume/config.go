// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import (
	"fmt"
	"time"
)

// Config 定义单个消费运行时的全部有界资源和时间预算。
type Config struct {
	WorkerCount          int
	MaxBatchMessages     int
	MaxBatchBytes        int
	MaxInflightMessages  int
	MaxInflightBytes     int
	MaxInflightPerLane   int
	ProcessTimeout       time.Duration
	RetryMaxAttempts     int
	RetryMaxElapsed      time.Duration
	RetryBackoffMin      time.Duration
	RetryBackoffMax      time.Duration
	MaxRetryMessages     int
	ConfirmRetryBackoff  time.Duration
	EmptyReceiveBackoff  time.Duration
	ShutdownDrainTimeout time.Duration
	SessionCloseTimeout  time.Duration
}

// DefaultConfig 返回适合功能验证的保守默认值。生产默认值仍需按真实载荷压测。
func DefaultConfig() Config {
	return Config{
		WorkerCount:          8,
		MaxBatchMessages:     128,
		MaxBatchBytes:        4 << 20,
		MaxInflightMessages:  512,
		MaxInflightBytes:     16 << 20,
		MaxInflightPerLane:   256,
		ProcessTimeout:       30 * time.Second,
		RetryMaxAttempts:     3,
		RetryMaxElapsed:      2 * time.Minute,
		RetryBackoffMin:      100 * time.Millisecond,
		RetryBackoffMax:      5 * time.Second,
		MaxRetryMessages:     256,
		ConfirmRetryBackoff:  500 * time.Millisecond,
		EmptyReceiveBackoff:  20 * time.Millisecond,
		ShutdownDrainTimeout: 30 * time.Second,
		SessionCloseTimeout:  5 * time.Second,
	}
}

// Validate 校验配置中的容量和时间边界。
func (c Config) Validate() error {
	positiveInts := []struct {
		name  string
		value int
	}{
		{"worker_count", c.WorkerCount},
		{"max_batch_messages", c.MaxBatchMessages},
		{"max_batch_bytes", c.MaxBatchBytes},
		{"max_inflight_messages", c.MaxInflightMessages},
		{"max_inflight_bytes", c.MaxInflightBytes},
		{"max_inflight_per_lane", c.MaxInflightPerLane},
		{"retry_max_attempts", c.RetryMaxAttempts},
		{"max_retry_messages", c.MaxRetryMessages},
	}
	for _, item := range positiveInts {
		if item.value <= 0 {
			return fmt.Errorf("%s must be positive: %d", item.name, item.value)
		}
	}
	if c.MaxBatchMessages > c.MaxInflightMessages {
		return fmt.Errorf("max_batch_messages must not exceed max_inflight_messages")
	}
	if c.MaxBatchMessages > c.MaxInflightPerLane {
		return fmt.Errorf("max_batch_messages must not exceed max_inflight_per_lane")
	}
	if c.MaxBatchBytes > c.MaxInflightBytes {
		return fmt.Errorf("max_batch_bytes must not exceed max_inflight_bytes")
	}
	if c.MaxRetryMessages > c.MaxInflightMessages {
		return fmt.Errorf("max_retry_messages must not exceed max_inflight_messages")
	}
	positiveDurations := []struct {
		name  string
		value time.Duration
	}{
		{"process_timeout", c.ProcessTimeout},
		{"retry_max_elapsed", c.RetryMaxElapsed},
		{"retry_backoff_min", c.RetryBackoffMin},
		{"retry_backoff_max", c.RetryBackoffMax},
		{"confirm_retry_backoff", c.ConfirmRetryBackoff},
		{"empty_receive_backoff", c.EmptyReceiveBackoff},
		{"shutdown_drain_timeout", c.ShutdownDrainTimeout},
		{"session_close_timeout", c.SessionCloseTimeout},
	}
	for _, item := range positiveDurations {
		if item.value <= 0 {
			return fmt.Errorf("%s must be positive: %s", item.name, item.value)
		}
	}
	if c.RetryBackoffMin > c.RetryBackoffMax {
		return fmt.Errorf("retry_backoff_min must not exceed retry_backoff_max")
	}
	return nil
}
