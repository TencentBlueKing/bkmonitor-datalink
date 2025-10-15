// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package exporter

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/converter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/queue"
)

// 不同类型的数据大小不同 所以队列大小要单独调整
const (
	defaultMetricsBatchSize  = 2000
	defaultTracesBatchSize   = 200
	defaultLogsBatchSize     = 100
	defaultProxyBatchSize    = 2000
	defaultProfilesBatchSize = 50
	defaultFlushInterval     = 3 * time.Second
	defaultMaxMessageBytes   = 10 * 1024 * 1024 // 10MB
)

type Config struct {
	MaxMessageBytes int              `config:"max_message_bytes"`
	Queue           queue.Config     `config:"queue"`
	Converter       converter.Config `config:"converter"`
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
	if c.Queue.ProxyBatchSize <= 0 {
		c.Queue.ProxyBatchSize = defaultProxyBatchSize
	}
	if c.Queue.ProfilesBatchSize <= 0 {
		c.Queue.ProfilesBatchSize = defaultProfilesBatchSize
	}
	if c.Queue.FlushInterval <= 0 {
		c.Queue.FlushInterval = defaultFlushInterval
	}
	if c.MaxMessageBytes <= 0 {
		c.MaxMessageBytes = defaultMaxMessageBytes
	}
}

type SubConfig struct {
	Type     string `config:"type"`
	Token    string `config:"token"`
	Exporter Config `config:"exporter"`
}

// LoadConfigFrom 允许加载 exporter 子配置
func LoadConfigFrom(conf *confengine.Config) map[string]queue.Config {
	var apmConf define.ApmConfig
	var err error
	batches := make(map[string]queue.Config)

	if err = conf.UnpackChild(define.ConfigFieldApmConfig, &apmConf); err != nil {
		return batches
	}

	subConfigs := confengine.LoadConfigPatterns(apmConf.Patterns)
	for _, subConf := range subConfigs {
		var sub SubConfig
		if err := subConf.Unpack(&sub); err != nil {
			continue
		}
		if sub.Type != define.ConfigTypeSubConfig {
			continue
		}
		batches[sub.Token] = sub.Exporter.Queue
	}
	return batches
}
