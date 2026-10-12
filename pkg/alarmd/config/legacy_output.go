package config

import (
	"errors"
	"fmt"
	"strings"
)

// LegacyTopicPrefix is what the compatibility topic has to start with. The
// output checks the same when it opens (kafka.TriggerEventSink
// .ConfigureLegacyOutput); checked here too, a topic without it fails
// --check-config instead of a replica that never becomes ready.
const LegacyTopicPrefix = "alarmd_"

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
	if !strings.HasPrefix(adapter.Topic, LegacyTopicPrefix) {
		return fmt.Errorf("legacy_adapter.topic must start with %q: the output refuses any other when it opens, so the replica would never be ready", LegacyTopicPrefix)
	}
	if adapter.SnapshotPrefix == "" {
		return errors.New("legacy_adapter.snapshot_prefix is required: it names the keys every converted event writes to the service Redis")
	}
	return adapter.ServiceRedis.validate("legacy_adapter.service_redis")
}
