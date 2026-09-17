// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
)

// endpointSharing is which platform-cache roles the bundle served off another
// role's connection, decided where the clients are opened. The endpoint list
// says so beside the address, and reads that connection's health for the
// role, because a role with no client of its own has no health of its own.
type endpointSharing struct {
	runtimeIsSource     bool
	cmdbSharedWith      string
	dynamicSharedWith   string
	dynamicConfigured   bool
	compatOutputPresent bool
}

// redisClientForRole is the hook client name whose health a role reads.
func (sharing endpointSharing) redisClientForRole(role string) string {
	switch role {
	case fleet.EndpointStrategyCache:
		return "source"
	case fleet.EndpointStateRedis:
		if sharing.runtimeIsSource {
			return "source"
		}
		return "runtime"
	case fleet.EndpointCMDBCache:
		if sharing.cmdbSharedWith != "" {
			return sharing.redisClientForRole(sharing.cmdbSharedWith)
		}
		return "cmdb"
	case fleet.EndpointDynamicConfig:
		if sharing.dynamicSharedWith != "" {
			return sharing.redisClientForRole(sharing.dynamicSharedWith)
		}
		return "dynamic_config"
	case fleet.EndpointCompatOutput:
		return "legacy_output"
	}
	return ""
}

// redisAddress is a connection's address without its credentials: the
// address for a standalone instance; the master name and the sentinel
// addresses for a sentinel deployment.
func redisAddress(connection config.RedisConnectionConfig) string {
	if connection.Mode == config.RedisModeSentinel {
		return connection.MasterName + "@" + strings.Join(connection.SentinelAddress, ",")
	}
	return connection.Address
}

func redisEndpoint(role string, connection config.RedisConnectionConfig, prefix string) fleet.Endpoint {
	db := connection.DB
	return fleet.Endpoint{Role: role, Kind: "redis", Address: redisAddress(connection), Mode: connection.Mode,
		DB: &db, Prefix: prefix, Configured: true}
}

// resolveEndpoints writes down every external system the configuration
// names, as this process resolved it. The list is static for the process;
// what changes is the health beside each entry, which endpointFactsSource
// fills at every publish.
func resolveEndpoints(cfg config.Config, sharing endpointSharing) []fleet.Endpoint {
	endpoints := []fleet.Endpoint{
		redisEndpoint(fleet.EndpointStateRedis, cfg.RuntimeStoreRedis(), cfg.Redis.StatePrefix),
		redisEndpoint(fleet.EndpointStrategyCache, cfg.StrategySourceRedis(), cfg.PlatformKeyPrefix()),
		redisEndpoint(fleet.EndpointCMDBCache, cfg.CMDBCacheRedis(), cfg.PlatformKeyPrefix()),
	}
	if sharing.runtimeIsSource {
		endpoints[0].SharedWith = fleet.EndpointStrategyCache
	}
	endpoints[2].SharedWith = sharing.cmdbSharedWith
	if dynamic, configured := cfg.DynamicConfigRedis(); configured {
		entry := redisEndpoint(fleet.EndpointDynamicConfig, dynamic, cfg.PhaseTwo.PlatformSettings.RedisKeyPrefix)
		entry.SharedWith = sharing.dynamicSharedWith
		endpoints = append(endpoints, entry)
	} else {
		endpoints = append(endpoints, fleet.Endpoint{Role: fleet.EndpointDynamicConfig, Kind: "redis"})
	}
	endpoints = append(endpoints,
		fleet.Endpoint{Role: fleet.EndpointQueryBackend, Kind: "http", Address: cfg.PhaseTwo.Access.UQEndpoint,
			Configured: cfg.PhaseTwo.Access.UQEndpoint != ""},
		fleet.Endpoint{Role: fleet.EndpointOutputKafka, Kind: "kafka", Address: strings.Join(cfg.Kafka.Brokers, ","),
			Prefix: cfg.Kafka.TriggerEvent.Topic, Configured: len(cfg.Kafka.Brokers) > 0},
	)
	if sharing.compatOutputPresent {
		endpoints = append(endpoints, redisEndpoint(fleet.EndpointCompatOutput, cfg.Kafka.LegacyAdapter.ServiceRedis,
			cfg.Kafka.LegacyAdapter.SnapshotPrefix))
	} else {
		endpoints = append(endpoints, fleet.Endpoint{Role: fleet.EndpointCompatOutput, Kind: "redis"})
	}
	return endpoints
}

// endpointFactsSource returns the endpoint list with what this process has
// seen of each: the Redis hooks' last success and failure per connection,
// and for the platform caches what the reader found there. The source facts
// are the leader's; a follower reads no source and says nothing about the
// writer of the strategy cache.
func endpointFactsSource(
	cfg config.Config, sharing endpointSharing, recorder *metric.Recorder,
	cmdb *cmdbcache.Store, settings *platformsettings.Cache,
	source func() *fleet.SourceFacts, now func() time.Time,
) func() []fleet.Endpoint {
	static := resolveEndpoints(cfg, sharing)
	return func() []fleet.Endpoint {
		at := now()
		endpoints := make([]fleet.Endpoint, len(static))
		copy(endpoints, static)
		for index := range endpoints {
			entry := &endpoints[index]
			if entry.Kind == "redis" && entry.Configured && recorder != nil {
				if health, known := recorder.RedisClientHealth(sharing.redisClientForRole(entry.Role)); known {
					if !health.LastSuccessAt.IsZero() {
						age := at.Sub(health.LastSuccessAt).Seconds()
						entry.LastSuccessAgeSeconds = &age
					}
					if !health.LastFailureAt.IsZero() {
						age := at.Sub(health.LastFailureAt).Seconds()
						entry.LastFailureAgeSeconds = &age
						entry.LastFailure = health.LastFailure
					}
				}
			}
			switch entry.Role {
			case fleet.EndpointStrategyCache:
				if facts := source(); facts != nil {
					writer := &fleet.WriterEvidence{Present: facts.Listed > 0, Count: facts.Listed}
					if facts.ChangeSignalAgeSeconds != nil {
						age := float64(*facts.ChangeSignalAgeSeconds)
						writer.AgeSeconds = &age
					}
					if facts.ChangeSignalPresent {
						writer.State = "marker_present"
					} else {
						writer.State = "marker_absent"
					}
					entry.Writer = writer
				}
			case fleet.EndpointCMDBCache:
				if cmdb != nil {
					health := cmdb.Health()
					writer := &fleet.WriterEvidence{Present: health.Loaded, Count: health.Hosts, State: health.DegradedReason}
					if health.Loaded && health.SourceAge > 0 {
						age := health.SourceAge.Seconds()
						writer.AgeSeconds = &age
					}
					if writer.State == "" && health.Loaded {
						writer.State = "loaded"
					}
					entry.Writer = writer
				}
			case fleet.EndpointDynamicConfig:
				if settings != nil {
					stats := settings.Stats()
					writer := &fleet.WriterEvidence{Present: !stats.LoadedAt.IsZero(), State: string(stats.Mode)}
					if !stats.LoadedAt.IsZero() {
						age := at.Sub(stats.LoadedAt).Seconds()
						writer.AgeSeconds = &age
					}
					entry.Writer = writer
				}
			}
		}
		return endpoints
	}
}
