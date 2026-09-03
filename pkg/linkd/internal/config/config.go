// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package config 负责加载、覆盖和校验 Linkd 本地进程配置。
package config

import (
	"fmt"

	"linkd/internal/logging"
	"linkd/internal/telemetry"
)

// MaxFileSize 是 Linkd 配置文件允许的最大字节数。
const MaxFileSize = 1 << 20

// Config 是 Linkd 本地进程配置的聚合根对象。
type Config struct {
	Logging      logging.Config       `yaml:"logging"`
	Storage      *StorageConfig       `yaml:"storage,omitempty"`
	Lifecycle    *LifecycleConfig     `yaml:"lifecycle,omitempty"`
	ControlPlane *ControlPlaneConfig  `yaml:"control_plane,omitempty"`
	Telemetry    *telemetry.Config    `yaml:"telemetry,omitempty"`
	Cleaner      CleanerRuntimeConfig `yaml:"cleaner"`
	Severity     SeverityConfig       `yaml:"severity"`
	EventSources []EventSource        `yaml:"event_sources"`
}

// Overrides 描述用户在命令行中显式设置的配置覆盖。
// nil 表示未设置，指向空字符串表示用户显式要求使用空值。
type Overrides struct {
	LogLevel  *string
	LogFormat *string
}

// Default 返回 Linkd 配置的代码默认值。
func Default() Config {
	return Config{
		Logging:      logging.DefaultConfig(),
		Cleaner:      DefaultCleanerRuntimeConfig(),
		Severity:     DefaultSeverityConfig(),
		EventSources: []EventSource{},
	}
}

// Validate 校验完整的 Linkd 本地进程配置。
func (c Config) Validate() error {
	if err := c.Logging.Validate(); err != nil {
		return err
	}
	if c.Storage != nil {
		if err := c.Storage.Validate(); err != nil {
			return err
		}
	}
	if c.Lifecycle != nil {
		if err := c.Lifecycle.Validate(); err != nil {
			return err
		}
	}
	if c.ControlPlane != nil {
		if err := c.ControlPlane.Validate(); err != nil {
			return err
		}
		if c.ControlPlane.Elasticsearch != nil {
			if c.Storage == nil || c.Storage.Repository != RepositoryTypeElasticsearch || c.Storage.Elasticsearch == nil {
				return fmt.Errorf("storage.elasticsearch repository is required when control_plane.elasticsearch is configured")
			}
		}
		if c.ControlPlane.RedisStream != nil {
			if c.Storage == nil || c.Storage.Redis == nil {
				return fmt.Errorf("storage.redis is required when control_plane.redis_stream is configured")
			}
			if c.Lifecycle == nil {
				return fmt.Errorf("lifecycle is required when control_plane.redis_stream is configured")
			}
		}
	}
	if c.Telemetry != nil {
		if err := c.Telemetry.Validate(); err != nil {
			return err
		}
	}
	if err := c.Cleaner.Validate(); err != nil {
		return err
	}
	if err := c.Severity.Validate(); err != nil {
		return err
	}
	if err := ValidateEventSources(c.EventSources, c.Severity); err != nil {
		return err
	}
	for index, source := range c.EventSources {
		if err := source.Cleaner.RuntimeConfig(c.Cleaner).Validate(); err != nil {
			return fmt.Errorf("event_sources[%d].cleaner.runtime: %w", index, err)
		}
	}
	return nil
}

// Redacted 返回可安全展示的配置副本。
func (c Config) Redacted() Config {
	redacted := c
	if c.Storage != nil {
		storage := c.Storage.Redacted()
		redacted.Storage = &storage
	}
	if c.Lifecycle != nil {
		lifecycle := c.Lifecycle.Redacted()
		redacted.Lifecycle = &lifecycle
	}
	if c.ControlPlane != nil {
		controlPlane := c.ControlPlane.WithDefaults()
		redacted.ControlPlane = &controlPlane
	}
	if c.Telemetry != nil {
		telemetryConfig := *c.Telemetry
		redacted.Telemetry = &telemetryConfig
	}
	redacted.EventSources = make([]EventSource, len(c.EventSources))
	redacted.Severity = c.Severity.WithDefaults()
	for index, source := range c.EventSources {
		redacted.EventSources[index] = source.Redacted()
	}
	return redacted
}
