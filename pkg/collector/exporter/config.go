// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exporter

import "time"

// 不同类型的数据大小不同 所以队列大小要单独调整
const (
	defaultMetricsBatchSize      = 2000
	defaultTracesBatchSize       = 200
	defaultLogsBatchSize         = 100
	defaultFlushInterval         = 3 * time.Second
	defaultSlowSendThreshold     = 3 * time.Second
	defaultSlowSendCheckInterval = 30 * time.Minute
)

type Config struct {
	Queue    QueueConfig    `config:"queue"`
	SlowSend SlowSendConfig `config:"slow_send"`
}

// QueueConfig 不同类型的数据大小不同 因此要允许为每种类型单独设置队列批次
type QueueConfig struct {
	MetricsBatchSize int           `config:"metrics_batch_size"`
	LogsBatchSize    int           `config:"logs_batch_size"`
	TracesBatchSize  int           `config:"traces_batch_size"`
	FlushInterval    time.Duration `config:"flush_interval"`
}

type SlowSendConfig struct {
	Enabled       bool          `config:"enabled"`
	CheckInterval time.Duration `config:"check_interval"`
	Threshold     time.Duration `config:"threshold"`
}

func (c *Config) Validate() {
	if c.Queue.MetricsBatchSize <= 0 {
		c.Queue.MetricsBatchSize = defaultMetricsBatchSize
	}
	if c.Queue.LogsBatchSize <= 0 {
		c.Queue.LogsBatchSize = defaultLogsBatchSize
	}
	if c.Queue.TracesBatchSize <= 0 {
		c.Queue.TracesBatchSize = defaultTracesBatchSize
	}
	if c.Queue.FlushInterval <= 0 {
		c.Queue.FlushInterval = defaultFlushInterval
	}
	if c.SlowSend.Threshold <= 0 {
		c.SlowSend.Threshold = defaultSlowSendThreshold
	}
	if c.SlowSend.CheckInterval <= 0 {
		c.SlowSend.CheckInterval = defaultSlowSendCheckInterval
	}
}
