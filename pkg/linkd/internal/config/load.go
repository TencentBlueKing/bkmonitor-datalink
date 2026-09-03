// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.yaml.in/yaml/v3"
	"linkd/internal/logging"
	"linkd/internal/telemetry"
)

const (
	envLogLevel  = "LINKD_LOG_LEVEL"
	envLogFormat = "LINKD_LOG_FORMAT"
)

type fileConfig struct {
	Logging      logging.Config       `yaml:"logging"`
	Storage      *StorageConfig       `yaml:"storage"`
	Lifecycle    *LifecycleConfig     `yaml:"lifecycle"`
	ControlPlane *ControlPlaneConfig  `yaml:"control_plane"`
	Telemetry    *telemetry.Config    `yaml:"telemetry"`
	Cleaner      CleanerRuntimeConfig `yaml:"cleaner"`
	Severity     SeverityConfig       `yaml:"severity"`
	EventSources []fileEventSource    `yaml:"event_sources"`
}

type fileEventSource struct {
	EventSourceID     string             `yaml:"event_source_id"`
	RelatedTenantID   string             `yaml:"related_tenant_id"`
	Enabled           *bool              `yaml:"enabled"`
	Cleaner           *CleanerConfig     `yaml:"cleaner"`
	FingerprintMode   string             `yaml:"fingerprint_mode"`
	FingerprintField  string             `yaml:"fingerprint_field"`
	FingerprintFields []string           `yaml:"fingerprint_fields"`
	SeverityMapping   map[string]string  `yaml:"severity_mapping"`
	DefaultSeverity   string             `yaml:"default_severity"`
	Storage           *fileStorageConfig `yaml:"storage"`
}

type fileStorageConfig struct {
	Type  string              `yaml:"type"`
	Kafka *KafkaStorageConfig `yaml:"kafka"`
}

// Load 从 path 严格加载单个 YAML 文档，依次应用环境变量和显式命令行覆盖。
func Load(path string, overrides Overrides) (Config, error) {
	return load(path, overrides, os.LookupEnv)
}

// MarshalRedacted 将可安全展示的最终配置编码为 YAML。
func MarshalRedacted(cfg Config) ([]byte, error) {
	data, err := yaml.Marshal(cfg.Redacted())
	if err != nil {
		return nil, fmt.Errorf("marshal redacted config: %w", err)
	}
	return data, nil
}

func load(path string, overrides Overrides, lookupEnv func(string) (string, bool)) (Config, error) {
	data, err := readFile(path)
	if err != nil {
		return Config{}, err
	}

	defaults := Default()
	decoded := fileConfig{Logging: defaults.Logging, Cleaner: defaults.Cleaner, Severity: defaults.Severity}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&decoded); err != nil {
		return Config{}, fmt.Errorf("decode config %q: %w", path, err)
	}
	var extra yaml.Node
	err = decoder.Decode(&extra)
	switch {
	case errors.Is(err, io.EOF):
	case err == nil:
		return Config{}, fmt.Errorf("decode config %q: multiple YAML documents are not supported", path)
	default:
		return Config{}, fmt.Errorf("decode config %q: %w", path, err)
	}

	eventSources, err := decodeEventSources(decoded.EventSources)
	if err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}
	var lifecycle *LifecycleConfig
	if decoded.Lifecycle != nil {
		normalized := decoded.Lifecycle.WithDefaults()
		lifecycle = &normalized
	}

	var storage *StorageConfig
	if decoded.Storage != nil {
		normalized := decoded.Storage.WithDefaults()
		storage = &normalized
	}
	var controlPlane *ControlPlaneConfig
	if decoded.ControlPlane != nil {
		normalized := decoded.ControlPlane.WithDefaults()
		controlPlane = &normalized
	}
	cfg := Config{
		Logging:      decoded.Logging,
		Storage:      storage,
		Lifecycle:    lifecycle,
		ControlPlane: controlPlane,
		Telemetry:    decoded.Telemetry,
		Cleaner:      decoded.Cleaner.WithDefaults(),
		Severity:     decoded.Severity.WithDefaults(),
		EventSources: eventSources,
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return Config{}, fmt.Errorf("resolve config path %q: %w", path, err)
	}
	if err := cfg.resolveKafkaTLSPaths(filepath.Dir(absPath)); err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}
	applyEnvironment(&cfg, lookupEnv)
	applyOverrides(&cfg, overrides)

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config %q: %w", path, err)
	}
	return cfg, nil
}

func decodeEventSources(decoded []fileEventSource) ([]EventSource, error) {
	if decoded == nil {
		return []EventSource{}, nil
	}

	sources := make([]EventSource, len(decoded))
	for index, source := range decoded {
		if source.Enabled == nil {
			return nil, fmt.Errorf("event_sources[%d].enabled is required", index)
		}
		if source.Storage == nil {
			return nil, fmt.Errorf("event_sources[%d].storage is required", index)
		}
		if source.Storage.Kafka == nil {
			return nil, fmt.Errorf("event_sources[%d].storage.kafka is required", index)
		}

		cleaner := CleanerConfig{}
		if source.Cleaner != nil {
			cleaner = *source.Cleaner
		}
		sources[index] = EventSource{
			EventSourceID:     source.EventSourceID,
			RelatedTenantID:   source.RelatedTenantID,
			Enabled:           *source.Enabled,
			Cleaner:           cleaner,
			FingerprintMode:   source.FingerprintMode,
			FingerprintField:  source.FingerprintField,
			FingerprintFields: append([]string(nil), source.FingerprintFields...),
			SeverityMapping:   source.SeverityMapping,
			DefaultSeverity:   source.DefaultSeverity,
			Storage: EventSourceStorageConfig{
				Type:  source.Storage.Type,
				Kafka: *source.Storage.Kafka,
			},
		}.WithDefaults()
	}
	return sources, nil
}

func readFile(path string) ([]byte, error) {
	// 配置路径来自受信任的进程启动参数，读取该明确路径正是此函数的职责。
	//nolint:gosec // G304: Linkd 必须支持用户通过 --config 选择配置文件。
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config %q: %w", path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	data, err := io.ReadAll(io.LimitReader(file, MaxFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	if len(data) > MaxFileSize {
		return nil, fmt.Errorf("read config %q: file exceeds %d bytes", path, MaxFileSize)
	}
	return data, nil
}

func applyEnvironment(cfg *Config, lookupEnv func(string) (string, bool)) {
	if value, exists := lookupEnv(envLogLevel); exists {
		cfg.Logging.Level = value
	}
	if value, exists := lookupEnv(envLogFormat); exists {
		cfg.Logging.Format = value
	}
}

func applyOverrides(cfg *Config, overrides Overrides) {
	if overrides.LogLevel != nil {
		cfg.Logging.Level = *overrides.LogLevel
	}
	if overrides.LogFormat != nil {
		cfg.Logging.Format = *overrides.LogFormat
	}
}
