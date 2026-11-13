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
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/converter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/exporter/queue"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/metricmeta"
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
	MetricMetadata  MetricMetadataConfig `config:"metric_metadata"`   // 独立元信息输出目标与配额。
	MaxMessageBytes int                  `config:"max_message_bytes"` // 单个 GSE 消息允许的最大字节数。
	Queue           queue.Config         `config:"queue"`             // 各信号的批次与刷新参数。
	Converter       converter.Config     `config:"converter"`         // 输入协议到事件的转换参数。
}

// MetricMetadataConfig 将 collector 的上报目标与元信息 SDK 配置组合。
type MetricMetadataConfig struct {
	Enabled      bool              `config:"enabled"` // 开启后将元信息作为时序指标上报。
	OutputDataID int32             `config:"dataid"`  // 后台分配的目标指标 DataID，开启时必须大于零。
	Config       metricmeta.Config `config:",inline"`
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

type subExporterConfig struct {
	Type     string `config:"type"`
	Token    string `config:"token"`
	Exporter struct {
		Queue queue.Config `config:"queue"`
	} `config:"exporter"`
}

// loadedConfig 一次读取主文件和子配置，全部校验成功后才替换运行状态。
type loadedConfig struct {
	Config                                 // 当前进程的导出设置。
	batches        map[string]queue.Config // 按来源 Token 生效的数值队列设置。
	platformLoaded bool                    // 一个进程只允许一份平台元信息设置。
}

func loadExporterConfig(conf *confengine.Config) (*loadedConfig, error) {
	c := &loadedConfig{batches: make(map[string]queue.Config)}
	c.MetricMetadata.Config = metricmeta.DefaultConfig()
	if err := conf.UnpackChild(define.ConfigFieldExporter, &c.Config); err != nil {
		return nil, err
	}
	c.Validate()
	if err := validateMetadataFields(conf); err != nil {
		return nil, err
	}
	var apm define.ApmConfig
	if conf.Has(define.ConfigFieldApmConfig) {
		if err := conf.UnpackChild(define.ConfigFieldApmConfig, &apm); err != nil {
			return nil, err
		}
	}
	seen := make(map[string]bool)
	for _, pattern := range apm.Patterns {
		paths, err := filepath.Glob(pattern)
		if err != nil {
			return nil, err
		}
		for _, path := range paths {
			if seen[path] {
				continue
			}
			seen[path] = true
			sub, err := confengine.LoadConfigPath(path)
			if err != nil {
				return nil, err
			}
			if err := c.loadSub(sub); err != nil {
				return nil, fmt.Errorf("exporter subconfig %s: %w", path, err)
			}
		}
	}
	m := c.MetricMetadata
	if m.Enabled && m.OutputDataID <= 0 {
		return nil, fmt.Errorf("metric_metadata requires positive dataid")
	}
	return c, nil
}

func (c *loadedConfig) loadSub(sub *confengine.Config) error {
	var sc subExporterConfig
	if err := sub.Unpack(&sc); err != nil {
		return err
	}
	switch sc.Type {
	case define.ConfigTypePlatform:
		if !sub.Has("exporter.metric_metadata") {
			return nil
		}
		if c.platformLoaded {
			return fmt.Errorf("multiple platform metric_metadata configurations")
		}
		if err := validateMetadataFields(sub); err != nil {
			return err
		}
		c.platformLoaded = true
		return sub.UnpackChild("exporter.metric_metadata", &c.MetricMetadata)
	case define.ConfigTypeSubConfig:
		c.batches[sc.Token] = sc.Exporter.Queue
	}
	return nil
}

// 拒绝旧出口或拼错的额度字段，不能静默按默认值发送。
func validateMetadataFields(c *confengine.Config) error {
	const path = "exporter.metric_metadata"
	if !c.Has(path) {
		return nil
	}
	m, err := c.Child(path)
	if err != nil {
		return err
	}
	keys := []string{"enabled", "dataid", "rate_limit", "exporter"}
	for _, field := range m.RawConfig().GetFields() {
		if !slices.Contains(keys, field) {
			return fmt.Errorf("unsupported %s.%s", path, field)
		}
	}
	if m.Has("exporter") {
		sender, err := m.Child("exporter")
		if err != nil {
			return err
		}
		for _, field := range sender.RawConfig().GetFields() {
			if !slices.Contains([]string{"interval", "timeout", "batch_size", "queue_size"}, field) {
				return fmt.Errorf("unsupported %s.exporter.%s", path, field)
			}
		}
	}
	if m.Has("rate_limit") {
		l, err := m.Child("rate_limit")
		if err != nil {
			return err
		}
		for _, field := range l.RawConfig().GetFields() {
			if field != "dataid_per_minute" && field != "instance_per_minute" {
				return fmt.Errorf("unsupported %s.rate_limit.%s", path, field)
			}
		}
	}
	return nil
}
