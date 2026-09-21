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
	"errors"
)

// LegacyAdapterConfig carries the environment coordinates the built-in
// Python-compatible protocol needs. It is not an enable switch: the protocol is
// chosen per event from the strategy's frozen revision, so every deployment can
// reach it.
type LegacyAdapterConfig struct {
	Topic          string                `yaml:"topic"`
	PluginID       string                `yaml:"plugin_id"`
	SnapshotPrefix string                `yaml:"snapshot_prefix"`
	ServiceRedis   RedisConnectionConfig `yaml:"service_redis"`
	PodCache       *LegacyPodCacheConfig `yaml:"pod_cache"`
}

// defaultDjangoCacheVersion is Django's own default cache version, which the
// platform does not override; it becomes part of every key in that cache.
const defaultDjangoCacheVersion = 1

type LegacyPodCacheConfig struct {
	Connection RedisConnectionConfig `yaml:"connection"`
	KeyPrefix  string                `yaml:"key_prefix"`
	Version    int                   `yaml:"version"`
}

// validateCompatibilityOutput checks everything the built-in Python-compatible
// protocol needs, at configuration load rather than at bundle assembly. The
// protocol is selected per event from the strategy's frozen revision, so a
// deployment cannot declare that it will not be used, and discovering a gap at
// the first event is what took an earlier release to twenty-five minutes of
// failed emissions. Validating here also means --check-config covers it: the
// preflight previously stopped at decoding, and both incidents landed in that
// blind spot.
func (c KafkaConfig) validateCompatibilityOutput() error {
	adapter := c.LegacyAdapter
	if adapter.Topic == "" {
		return errors.New("legacy_adapter.topic is required: every strategy without a frozen revision publishes to it")
	}
	if err := validatePhaseTwoTopic("legacy_adapter.topic", adapter.Topic); err != nil {
		return err
	}
	if adapter.SnapshotPrefix == "" {
		return errors.New("legacy_adapter.snapshot_prefix is required: it names the keys every converted event writes to the service Redis")
	}
	return adapter.ServiceRedis.validate("legacy_adapter.service_redis")
}
