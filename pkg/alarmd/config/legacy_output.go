package config

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"

// LegacyAdapterConfig pins the existing Python protocol's service routes.
// Updating Python CacheRouter requires updating this deployment snapshot too.
type LegacyAdapterConfig struct {
	Topic          string                           `yaml:"topic"`
	PluginID       string                           `yaml:"plugin_id"`
	SnapshotPrefix string                           `yaml:"snapshot_prefix"`
	ServiceNodes   map[string]RedisConnectionConfig `yaml:"service_nodes"`
	ServiceRoutes  []legacyoutput.ServiceRoute      `yaml:"service_routes"`
	PodCache       *LegacyPodCacheConfig            `yaml:"pod_cache"`
}

type LegacyPodCacheConfig struct {
	Connection RedisConnectionConfig `yaml:"connection"`
	KeyPrefix  string                `yaml:"key_prefix"`
	Version    int                   `yaml:"version"`
}
