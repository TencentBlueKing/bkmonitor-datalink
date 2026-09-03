// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"fmt"
	"time"
)

const (
	defaultElasticsearchSchemaAndActiveReconcileIntervalSeconds = 3600
	defaultElasticsearchBucketReconcileIntervalSeconds          = 21600
	defaultElasticsearchArchiveIntervalSeconds                  = 30
	defaultElasticsearchArchiveBatchSize                        = 1000
	defaultElasticsearchArchiveWorkerCount                      = 4
	defaultRedisStreamReconcileIntervalSeconds                  = 60
	defaultRedisStreamOperationTimeoutSeconds                   = 10
	defaultRedisStreamMaxEntries                                = 100000
	defaultRedisStreamTrimBatchSize                             = 10000
)

// ControlPlaneConfig 描述控制面独占执行的低吞吐管理任务。
type ControlPlaneConfig struct {
	Elasticsearch *ElasticsearchControlPlaneConfig `yaml:"elasticsearch,omitempty"`
	RedisStream   *RedisStreamManagerConfig        `yaml:"redis_stream,omitempty"`
}

// ElasticsearchControlPlaneConfig 配置两个周期对账任务及连续 Alert 归档任务。
type ElasticsearchControlPlaneConfig struct {
	SchemaAndActiveReconcileIntervalSeconds int `yaml:"schema_and_active_reconcile_interval_seconds"`
	BucketReconcileIntervalSeconds          int `yaml:"bucket_reconcile_interval_seconds"`
	// ArchiveIntervalSeconds 是无数据、无进展或请求失败后的等待秒数，不限制有积压时的批次频率。
	ArchiveIntervalSeconds int `yaml:"archive_interval_seconds"`
	// ArchiveBatchSize 是一次终态 Alert 扫描的数量上限。
	ArchiveBatchSize int `yaml:"archive_batch_size"`
	// ArchiveWorkerCount 是一个扫描批次内并发执行 Bulk 归档的 Worker 上限。
	ArchiveWorkerCount int `yaml:"archive_worker_count"`
}

// RedisStreamManagerConfig 定义 Lifecycle Signal Stream 的采集周期和安全裁剪预算。
// MaxEntries 是软上限：控制面只裁剪所有 Consumer Group 都已确认的连续前缀，
// 未读或 Pending 消息可以使实际长度暂时超过该值。
type RedisStreamManagerConfig struct {
	// ReconcileIntervalSeconds 是指标采集和裁剪周期。
	ReconcileIntervalSeconds int `yaml:"reconcile_interval_seconds"`
	// OperationTimeoutSeconds 限制单轮 Redis 操作总时间。
	OperationTimeoutSeconds int `yaml:"operation_timeout_seconds"`
	// MaxEntries 是触发安全裁剪的软长度上限。
	MaxEntries int64 `yaml:"max_entries"`
	// TrimBatchSize 限制单轮裁剪检查和删除的条目数。
	TrimBatchSize int64 `yaml:"trim_batch_size"`
}

// WithDefaults 返回补齐控制面管理任务默认值且不共享嵌套配置的副本。
func (c ControlPlaneConfig) WithDefaults() ControlPlaneConfig {
	normalized := c
	if c.Elasticsearch != nil {
		elasticsearch := c.Elasticsearch.WithDefaults()
		normalized.Elasticsearch = &elasticsearch
	}
	if c.RedisStream != nil {
		stream := c.RedisStream.WithDefaults()
		normalized.RedisStream = &stream
	}
	return normalized
}

// WithDefaults 返回补齐 Elasticsearch 管理任务周期、批次和并发度后的配置。
func (c ElasticsearchControlPlaneConfig) WithDefaults() ElasticsearchControlPlaneConfig {
	if c.SchemaAndActiveReconcileIntervalSeconds == 0 {
		c.SchemaAndActiveReconcileIntervalSeconds = defaultElasticsearchSchemaAndActiveReconcileIntervalSeconds
	}
	if c.BucketReconcileIntervalSeconds == 0 {
		c.BucketReconcileIntervalSeconds = defaultElasticsearchBucketReconcileIntervalSeconds
	}
	if c.ArchiveIntervalSeconds == 0 {
		c.ArchiveIntervalSeconds = defaultElasticsearchArchiveIntervalSeconds
	}
	if c.ArchiveBatchSize == 0 {
		c.ArchiveBatchSize = defaultElasticsearchArchiveBatchSize
	}
	if c.ArchiveWorkerCount == 0 {
		c.ArchiveWorkerCount = defaultElasticsearchArchiveWorkerCount
	}
	return c
}

// WithDefaults 返回补齐采集、超时和裁剪预算的配置。
func (c RedisStreamManagerConfig) WithDefaults() RedisStreamManagerConfig {
	if c.ReconcileIntervalSeconds == 0 {
		c.ReconcileIntervalSeconds = defaultRedisStreamReconcileIntervalSeconds
	}
	if c.OperationTimeoutSeconds == 0 {
		c.OperationTimeoutSeconds = defaultRedisStreamOperationTimeoutSeconds
	}
	if c.MaxEntries == 0 {
		c.MaxEntries = defaultRedisStreamMaxEntries
	}
	if c.TrimBatchSize == 0 {
		c.TrimBatchSize = defaultRedisStreamTrimBatchSize
	}
	return c
}

// Validate 校验已声明的控制面任务。
func (c ControlPlaneConfig) Validate() error {
	if c.Elasticsearch == nil && c.RedisStream == nil {
		return fmt.Errorf("control_plane must configure at least one management task")
	}
	if c.Elasticsearch != nil {
		if err := c.Elasticsearch.Validate(); err != nil {
			return fmt.Errorf("control_plane.elasticsearch.%w", err)
		}
	}
	if c.RedisStream != nil {
		if err := c.RedisStream.Validate(); err != nil {
			return fmt.Errorf("control_plane.redis_stream.%w", err)
		}
	}
	return nil
}

// Validate 校验三个 Elasticsearch 管理任务的周期和批次上限。
func (c ElasticsearchControlPlaneConfig) Validate() error {
	c = c.WithDefaults()
	for name, value := range map[string]int{
		"schema_and_active_reconcile_interval_seconds": c.SchemaAndActiveReconcileIntervalSeconds,
		"bucket_reconcile_interval_seconds":            c.BucketReconcileIntervalSeconds,
	} {
		if value < 5 || value > 86400 {
			return fmt.Errorf("%s must be between 5 and 86400", name)
		}
	}
	if c.ArchiveIntervalSeconds < 1 || c.ArchiveIntervalSeconds > 3600 {
		return fmt.Errorf("archive_interval_seconds must be between 1 and 3600")
	}
	if c.ArchiveBatchSize < 1 || c.ArchiveBatchSize > 10000 {
		return fmt.Errorf("archive_batch_size must be between 1 and 10000")
	}
	if c.ArchiveWorkerCount < 1 || c.ArchiveWorkerCount > 64 {
		return fmt.Errorf("archive_worker_count must be between 1 and 64")
	}
	if c.ArchiveWorkerCount > c.ArchiveBatchSize {
		return fmt.Errorf("archive_worker_count must not exceed archive_batch_size")
	}
	return nil
}

// SchemaAndActiveReconcileInterval 返回 Elasticsearch Schema 与 Active Alert 资源对账周期。
func (c ElasticsearchControlPlaneConfig) SchemaAndActiveReconcileInterval() time.Duration {
	return time.Duration(c.SchemaAndActiveReconcileIntervalSeconds) * time.Second
}

// BucketReconcileInterval 返回 Elasticsearch 时间桶维护周期。
func (c ElasticsearchControlPlaneConfig) BucketReconcileInterval() time.Duration {
	return time.Duration(c.BucketReconcileIntervalSeconds) * time.Second
}

// ArchiveInterval 返回 Elasticsearch 终态 Alert 归档的空闲轮询和失败重试间隔。
func (c ElasticsearchControlPlaneConfig) ArchiveInterval() time.Duration {
	return time.Duration(c.ArchiveIntervalSeconds) * time.Second
}

// Validate 校验 Redis Stream 管理任务的硬边界。
func (c RedisStreamManagerConfig) Validate() error {
	c = c.WithDefaults()
	if c.ReconcileIntervalSeconds < 5 || c.ReconcileIntervalSeconds > 3600 {
		return fmt.Errorf("reconcile_interval_seconds must be between 5 and 3600")
	}
	if c.OperationTimeoutSeconds < 1 || c.OperationTimeoutSeconds > 60 {
		return fmt.Errorf("operation_timeout_seconds must be between 1 and 60")
	}
	if c.OperationTimeoutSeconds >= c.ReconcileIntervalSeconds {
		return fmt.Errorf("operation_timeout_seconds must be less than reconcile_interval_seconds")
	}
	if c.MaxEntries < 1 || c.MaxEntries > 1_000_000_000 {
		return fmt.Errorf("max_entries must be between 1 and 1000000000")
	}
	if c.TrimBatchSize < 1 || c.TrimBatchSize > 1_000_000 {
		return fmt.Errorf("trim_batch_size must be between 1 and 1000000")
	}
	return nil
}

// ReconcileInterval 返回 Stream 指标采集和裁剪周期。
func (c RedisStreamManagerConfig) ReconcileInterval() time.Duration {
	return time.Duration(c.ReconcileIntervalSeconds) * time.Second
}

// OperationTimeout 返回单轮 Redis 管理操作的最大时间。
func (c RedisStreamManagerConfig) OperationTimeout() time.Duration {
	return time.Duration(c.OperationTimeoutSeconds) * time.Second
}
