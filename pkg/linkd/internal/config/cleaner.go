// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import "fmt"

const (
	defaultCleanerWorkerCount            = 8
	defaultCleanerMaxBatchMessages       = 128
	defaultCleanerMaxBatchBytes          = 4 << 20
	defaultCleanerBatchWaitMilliseconds  = 20
	defaultCleanerMaxConcurrentBatches   = 2
	defaultCleanerMaxInflightMessages    = 512
	defaultCleanerMaxInflightBytes       = 16 << 20
	defaultCleanerMaxInflightPerLane     = 256
	defaultCleanerResumeInflightPerLane  = 128
	defaultCleanerProcessTimeoutSeconds  = 30
	defaultCleanerRetryMaxAttempts       = 3
	defaultCleanerRetryMaxElapsedSeconds = 120
	defaultCleanerShutdownDrainSeconds   = 30
)

// CleanerRuntimeConfig 定义一条 EventSource Flow 的并发、批量和资源预算。
// EventSource 中的零值字段继承顶层 cleaner 配置。
type CleanerRuntimeConfig struct {
	WorkerCount                 int `yaml:"worker_count"`
	MaxBatchMessages            int `yaml:"max_batch_messages"`
	MaxBatchBytes               int `yaml:"max_batch_bytes"`
	BatchWaitMilliseconds       int `yaml:"batch_wait_milliseconds"`
	MaxConcurrentBatches        int `yaml:"max_concurrent_batches"`
	MaxInflightMessages         int `yaml:"max_inflight_messages"`
	MaxInflightBytes            int `yaml:"max_inflight_bytes"`
	MaxInflightPerLane          int `yaml:"max_inflight_per_lane"`
	ResumeInflightPerLane       int `yaml:"resume_inflight_per_lane"`
	ProcessTimeoutSeconds       int `yaml:"process_timeout_seconds"`
	RetryMaxAttempts            int `yaml:"retry_max_attempts"`
	RetryMaxElapsedSeconds      int `yaml:"retry_max_elapsed_seconds"`
	ShutdownDrainTimeoutSeconds int `yaml:"shutdown_drain_timeout_seconds"`
}

func DefaultCleanerRuntimeConfig() CleanerRuntimeConfig {
	return CleanerRuntimeConfig{
		WorkerCount: defaultCleanerWorkerCount, MaxBatchMessages: defaultCleanerMaxBatchMessages,
		MaxBatchBytes: defaultCleanerMaxBatchBytes, BatchWaitMilliseconds: defaultCleanerBatchWaitMilliseconds,
		MaxConcurrentBatches: defaultCleanerMaxConcurrentBatches, MaxInflightMessages: defaultCleanerMaxInflightMessages,
		MaxInflightBytes: defaultCleanerMaxInflightBytes, MaxInflightPerLane: defaultCleanerMaxInflightPerLane,
		ResumeInflightPerLane: defaultCleanerResumeInflightPerLane, ProcessTimeoutSeconds: defaultCleanerProcessTimeoutSeconds,
		RetryMaxAttempts: defaultCleanerRetryMaxAttempts, RetryMaxElapsedSeconds: defaultCleanerRetryMaxElapsedSeconds,
		ShutdownDrainTimeoutSeconds: defaultCleanerShutdownDrainSeconds,
	}
}

func (c CleanerRuntimeConfig) WithDefaults() CleanerRuntimeConfig {
	return mergeCleanerRuntime(DefaultCleanerRuntimeConfig(), c)
}

// MergeCleanerRuntime 把来源级非零覆盖合并到已经完整的全局配置。
func MergeCleanerRuntime(base, override CleanerRuntimeConfig) CleanerRuntimeConfig {
	return mergeCleanerRuntime(base.WithDefaults(), override)
}

func mergeCleanerRuntime(base, override CleanerRuntimeConfig) CleanerRuntimeConfig {
	targets := []*int{
		&base.WorkerCount, &base.MaxBatchMessages, &base.MaxBatchBytes, &base.BatchWaitMilliseconds,
		&base.MaxConcurrentBatches, &base.MaxInflightMessages, &base.MaxInflightBytes,
		&base.MaxInflightPerLane, &base.ResumeInflightPerLane, &base.ProcessTimeoutSeconds,
		&base.RetryMaxAttempts, &base.RetryMaxElapsedSeconds, &base.ShutdownDrainTimeoutSeconds,
	}
	values := []int{
		override.WorkerCount, override.MaxBatchMessages, override.MaxBatchBytes, override.BatchWaitMilliseconds,
		override.MaxConcurrentBatches, override.MaxInflightMessages, override.MaxInflightBytes,
		override.MaxInflightPerLane, override.ResumeInflightPerLane, override.ProcessTimeoutSeconds,
		override.RetryMaxAttempts, override.RetryMaxElapsedSeconds, override.ShutdownDrainTimeoutSeconds,
	}
	for index, value := range values {
		if value != 0 {
			*targets[index] = value
		}
	}
	return base
}

func (c CleanerRuntimeConfig) Validate() error {
	c = c.WithDefaults()
	for name, value := range map[string]int{
		"worker_count": c.WorkerCount, "max_batch_messages": c.MaxBatchMessages,
		"max_batch_bytes": c.MaxBatchBytes, "batch_wait_milliseconds": c.BatchWaitMilliseconds,
		"max_concurrent_batches": c.MaxConcurrentBatches, "max_inflight_messages": c.MaxInflightMessages,
		"max_inflight_bytes": c.MaxInflightBytes, "max_inflight_per_lane": c.MaxInflightPerLane,
		"resume_inflight_per_lane": c.ResumeInflightPerLane, "process_timeout_seconds": c.ProcessTimeoutSeconds,
		"retry_max_attempts": c.RetryMaxAttempts, "retry_max_elapsed_seconds": c.RetryMaxElapsedSeconds,
		"shutdown_drain_timeout_seconds": c.ShutdownDrainTimeoutSeconds,
	} {
		if value < 1 {
			return fmt.Errorf("cleaner.%s must be positive", name)
		}
	}
	if c.WorkerCount > 1024 || c.MaxBatchMessages > 4096 || c.MaxConcurrentBatches > 64 {
		return fmt.Errorf("cleaner concurrency or batch count exceeds hard limit")
	}
	if c.MaxBatchMessages > c.MaxInflightPerLane || c.MaxInflightPerLane > c.MaxInflightMessages {
		return fmt.Errorf("cleaner message capacities must satisfy batch <= lane <= global")
	}
	if c.ResumeInflightPerLane >= c.MaxInflightPerLane {
		return fmt.Errorf("cleaner.resume_inflight_per_lane must be less than max_inflight_per_lane")
	}
	if c.MaxBatchBytes > c.MaxInflightBytes || c.MaxBatchBytes > 64<<20 || c.MaxInflightBytes > 256<<20 {
		return fmt.Errorf("cleaner byte capacities are invalid")
	}
	return nil
}
