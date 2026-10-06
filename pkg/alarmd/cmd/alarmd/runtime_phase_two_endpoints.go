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
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Shopify/sarama"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/platformsettings"
)

// endpointSharing is which platform-cache roles the bundle served off another
// role's connection, decided where the clients are opened. The endpoint list
// says so beside the address, and reads that connection's health for the
// role, because a role with no client of its own has no health of its own.
type endpointSharing struct {
	runtimeIsSource       bool
	linkdDedicated        bool
	cmdbSharedWith        string
	dynamicSharedWith     string
	targetGroupSharedWith string
	dynamicConfigured     bool
	compatOutputPresent   bool
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
	case fleet.EndpointTargetGroup:
		if sharing.targetGroupSharedWith != "" {
			return sharing.redisClientForRole(sharing.targetGroupSharedWith)
		}
		return "target_group"
	case fleet.EndpointOpenAlertSet:
		if sharing.linkdDedicated {
			return "linkd"
		}
		return sharing.redisClientForRole(fleet.EndpointStateRedis)
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
	if groups, configured := cfg.TargetGroupRedis(); configured {
		prefix, _ := cfg.DynamicGroupKeyPrefix()
		entry := redisEndpoint(fleet.EndpointTargetGroup, groups, prefix)
		entry.SharedWith = sharing.targetGroupSharedWith
		endpoints = append(endpoints, entry)
	} else {
		endpoints = append(endpoints, fleet.Endpoint{Role: fleet.EndpointTargetGroup, Kind: "redis"})
	}
	if dynamic, configured := cfg.DynamicConfigRedis(); configured {
		entry := redisEndpoint(fleet.EndpointDynamicConfig, dynamic, cfg.PhaseTwo.PlatformSettings.RedisKeyPrefix)
		entry.SharedWith = sharing.dynamicSharedWith
		endpoints = append(endpoints, entry)
	} else {
		endpoints = append(endpoints, fleet.Endpoint{Role: fleet.EndpointDynamicConfig, Kind: "redis"})
	}
	linkdConnection := cfg.RuntimeStoreRedis()
	if cfg.PhaseTwo.Linkd.Connection != nil {
		linkdConnection = *cfg.PhaseTwo.Linkd.Connection
	}
	openAlerts := redisEndpoint(fleet.EndpointOpenAlertSet, linkdConnection, cfg.PhaseTwo.Linkd.Prefix())
	if reflect.DeepEqual(linkdConnection, cfg.RuntimeStoreRedis()) {
		openAlerts.SharedWith = fleet.EndpointStateRedis
	}
	endpoints = append(endpoints, openAlerts, linkdConsoleEndpoint(cfg),
		fleet.Endpoint{Role: fleet.EndpointQueryBackend, Kind: "http", Address: cfg.PhaseTwo.Access.UQEndpoint,
			Configured: cfg.PhaseTwo.Access.UQEndpoint != ""},
		outputKafkaEndpoint(cfg),
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
	source func() *fleet.SourceFacts, outputSink func() outputSinkState, openAlerts func() *fleet.OpenAlertSetFacts,
	now func() time.Time,
) func() []fleet.Endpoint {
	sharing.linkdDedicated = cfg.PhaseTwo.Linkd.Connection != nil && !reflect.DeepEqual(*cfg.PhaseTwo.Linkd.Connection, cfg.RuntimeStoreRedis())
	static := resolveEndpoints(cfg, sharing)
	return func() []fleet.Endpoint {
		at := now()
		endpoints := make([]fleet.Endpoint, len(static))
		copy(endpoints, static)
		for index := range endpoints {
			entry := &endpoints[index]
			if entry.Role == fleet.EndpointOutputKafka {
				// The output sink's own record: open or not, since when, and
				// what the last attempt said. Before this the entry had an
				// address and nothing else, and a replica that could not
				// reach it exited instead of saying so here.
				var protocol *enginekafka.ProtocolNegotiation
				if outputSink != nil {
					state := outputSink()
					protocol = state.Protocol
					ready := state.Ready
					entry.Ready = &ready
					if state.Ready {
						// How long it has been open -- not a success record; the
						// sink does not report messages here.
						age := at.Sub(state.Since).Seconds()
						entry.ReadySinceAgeSeconds = &age
					}
					if !state.LastFailureAt.IsZero() {
						age := at.Sub(state.LastFailureAt).Seconds()
						entry.LastFailureAgeSeconds = &age
						entry.LastFailure = state.LastFailure
					}
					if state.Attempts > 0 {
						attempts := state.Attempts
						entry.Attempts = &attempts
					}
				}
				// What the brokers answered, and the checks decided from it;
				// with no answer yet the checks are present and say so.
				outputProtocolFacts(entry, protocol)
			}
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
					if health.ScriptCacheMisses > 0 {
						age := at.Sub(health.LastScriptCacheMissAt).Seconds()
						entry.ScriptCacheMisses = health.ScriptCacheMisses
						entry.LastScriptCacheMissAgeSeconds = &age
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
			case fleet.EndpointOpenAlertSet:
				if openAlerts != nil {
					if facts := openAlerts(); facts != nil {
						// The short form every reading role has: present once
						// a publication was read, the members as the count,
						// the publisher's own heartbeat as the age, the mode
						// -- or why it is unavailable -- as the state. The
						// full account rides beside it.
						writer := &fleet.WriterEvidence{Present: facts.AuthoritativeAgeSeconds != nil, Count: facts.Members,
							AgeSeconds: facts.HeartbeatAgeSeconds, State: facts.Mode}
						if facts.UnavailableReason != "" {
							writer.State = facts.Mode + ":" + facts.UnavailableReason
						}
						entry.Writer = writer
						// A successful read is not evidence of a recent writer.
						// The index protocol has no publisher heartbeat.
						if facts.IndexProtocol {
							entry.Writer = nil
						}
						entry.OpenAlertSet = facts
					}
				}
			}
		}
		return endpoints
	}
}

// readinessFactsSource reads this replica's readiness from the same tracker
// its readiness endpoint answers from, field for field, so the fleet snapshot
// and the probe say the same thing about one process. Nil health -- a
// publisher built without one -- publishes no fact rather than a made-up
// ready.
// outputKafkaEndpoint is the output role with what the client is configured
// to speak: the floor version, and the one check that is the configuration's
// alone -- that the version parses. What the client actually speaks, and
// whether that can carry the standard raw event's record header, is the
// brokers' to say and is written by outputProtocolFacts once the sink has
// asked them. Two checks used to be decided here from the configured
// version; on a live deployment they read ok for an hour while the brokers,
// three minor versions older than that configuration, closed the connection
// on every write.
func outputKafkaEndpoint(cfg config.Config) fleet.Endpoint {
	entry := fleet.Endpoint{Role: fleet.EndpointOutputKafka, Kind: "kafka", Address: strings.Join(cfg.Kafka.Brokers, ","),
		Prefix: cfg.Kafka.TriggerEvent.Topic, Configured: len(cfg.Kafka.Brokers) > 0, ProtocolVersion: cfg.Kafka.BrokerVersion}
	if _, err := sarama.ParseKafkaVersion(cfg.Kafka.BrokerVersion); err != nil {
		entry.Checks = append(entry.Checks, fleet.EndpointCheck{Name: fleet.EndpointCheckBrokerVersion,
			Detail: "broker_version " + cfg.Kafka.BrokerVersion + " does not parse: " + err.Error()})
		return entry
	}
	entry.Checks = append(entry.Checks, fleet.EndpointCheck{Name: fleet.EndpointCheckBrokerVersion, OK: true})
	return entry
}

// outputProtocolFacts writes onto the output entry what the brokers answered
// when the sink asked them, and the two checks decided from that answer. Nil
// is a sink that has asked nobody yet: both checks are then present and
// failing, saying so, because a check that is absent while the sink is not
// open reads the same as one that passed.
//
// record_headers is whether the version the client came away with can carry
// the header the standard raw event puts the tenant in; when it cannot, the
// sentence names each broker whose accepted Produce range stops short of the
// version that carries it. produce_version_accepted is whether every broker
// answered and accepts the version the client sends -- the fleet's reading
// of the same answers.
func outputProtocolFacts(entry *fleet.Endpoint, protocol *enginekafka.ProtocolNegotiation) {
	checks := append([]fleet.EndpointCheck(nil), entry.Checks...)
	if protocol == nil {
		checks = append(checks,
			fleet.EndpointCheck{Name: fleet.EndpointCheckRecordHeaders, Detail: "not decided yet: the brokers have not been asked which protocol they accept"},
			fleet.ProduceVersionCheck(0, nil, false))
		entry.Checks = checks
		return
	}
	entry.NegotiatedVersion = protocol.Negotiated
	produce := protocol.ProduceVersion
	entry.ProduceVersion = &produce
	headers := protocol.HeadersSupported
	entry.HeadersSupported = &headers
	entry.Brokers = make([]fleet.BrokerProtocol, 0, len(protocol.Brokers))
	for _, broker := range protocol.Brokers {
		entry.Brokers = append(entry.Brokers, fleet.BrokerProtocol{
			Address: broker.Address, ID: broker.ID, Answered: broker.Answered,
			ProduceMinVersion: broker.ProduceMinVersion, ProduceMaxVersion: broker.ProduceMaxVersion, Error: broker.Error,
		})
	}
	record := fleet.EndpointCheck{Name: fleet.EndpointCheckRecordHeaders, OK: headers}
	if !headers {
		record.Detail = recordHeadersRefusedDetail(protocol)
	}
	checks = append(checks, record, fleet.ProduceVersionCheck(protocol.ProduceVersion, entry.Brokers, true))
	entry.Checks = checks
}

// recordHeadersRefusedDetail names the brokers that keep the client below the
// record-header version, each with the range it accepts, then what that
// means for the standard raw event.
func recordHeadersRefusedDetail(protocol *enginekafka.ProtocolNegotiation) string {
	var capping []string
	for _, broker := range protocol.Brokers {
		if !broker.Answered {
			capping = append(capping, fmt.Sprintf("%s did not answer (%s)", broker.Address, broker.Error))
			continue
		}
		if broker.ProduceMaxVersion < protocol.WantedProduceVersion {
			capping = append(capping, fmt.Sprintf("%s accepts Produce v%d..v%d", broker.Address, broker.ProduceMinVersion, broker.ProduceMaxVersion))
		}
	}
	if len(capping) == 0 {
		// Headers refused with no broker to blame: the configured floor was
		// already above what the client asked for, or the answers say
		// something this sentence has no branch for. Say what is known.
		capping = append(capping, "no broker capped the version")
	}
	return fmt.Sprintf("%s; record headers need Produce v%d (%s), so the client speaks %s and no native event can leave",
		strings.Join(capping, "; "), protocol.WantedProduceVersion, protocol.Wanted, protocol.Negotiated)
}

func readinessFactsSource(health *phaseTwoApplicationHealth) func() *fleet.ReadinessFacts {
	if health == nil {
		return nil
	}
	return func() *fleet.ReadinessFacts {
		snapshot := health.HealthSnapshot()
		facts := &fleet.ReadinessFacts{
			State: string(snapshot.State), Ready: snapshot.Ready,
			ConfigLoaded: snapshot.ConfigLoaded, SchemaReady: snapshot.SchemaReady,
			AssignmentReady: snapshot.AssignmentReady, RuntimeStateReady: snapshot.RuntimeStateReady,
			OutputSinkReady: snapshot.OutputSinkReady, SnapshotReady: snapshot.SnapshotReady,
			Draining: snapshot.Draining,
		}
		for _, reason := range snapshot.Reasons {
			facts.Reasons = append(facts.Reasons, string(reason))
		}
		return facts
	}
}
