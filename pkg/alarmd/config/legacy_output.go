package config

import (
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/legacyoutput"
)

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
	allowed := false
	for _, topic := range c.AllowedOutputTopics {
		if topic == adapter.Topic {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Errorf(
			"legacy_adapter.topic %q is not in allowed_output_topics %v",
			adapter.Topic, c.AllowedOutputTopics,
		)
	}
	if adapter.SnapshotPrefix == "" {
		return errors.New("legacy_adapter.snapshot_prefix is required: it names the keys every converted event writes to the service Redis")
	}
	if len(adapter.ServiceRoutes) == 0 {
		return errors.New("legacy_adapter.service_routes is required: a converted event writes its snapshot before it is published")
	}
	var previous int64
	for index, route := range adapter.ServiceRoutes {
		if route.UpperBound <= previous {
			return fmt.Errorf("legacy_adapter.service_routes[%d] upper_bound %d does not increase", index, route.UpperBound)
		}
		previous = route.UpperBound
		if _, ok := adapter.ServiceNodes[route.NodeID]; !ok {
			return fmt.Errorf("legacy_adapter.service_routes[%d] names node %q, which service_nodes does not define", index, route.NodeID)
		}
	}
	for id, connection := range adapter.ServiceNodes {
		if err := connection.validate(fmt.Sprintf("legacy_adapter.service_nodes.%s", id)); err != nil {
			return err
		}
	}
	return nil
}
