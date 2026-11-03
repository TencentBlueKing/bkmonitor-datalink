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
	"context"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

// writersTestConfig configures every role, each on a connection of its own.
func writersTestConfig() config.Config {
	cfg := config.Default()
	cfg.Redis.Address = "192.0.2.10:6379"
	cfg.PhaseTwo.Access.UQEndpoint = "http://198.51.100.7:10205"
	cfg.Kafka.Brokers = []string{"203.0.113.5:9092"}
	dynamic := config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: "192.0.2.11:6379"}
	groups := config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: "192.0.2.12:6379"}
	prefix := "bkmonitor.dynamic_group"
	cfg.PlatformCache.DynamicConfig, cfg.PlatformCache.TargetGroup, cfg.PlatformCache.DynamicGroupKeyPrefix = &dynamic, &groups, &prefix
	return cfg
}

func noteRedisSuccess(recorder *metric.Recorder, client string) {
	hook := recorder.RedisHook(client)
	ctx, _ := hook.BeforeProcess(context.Background(), nil)
	_ = hook.AfterProcess(ctx, redis.NewStringCmd(ctx, "get", "a"))
}

// Every role the page shows has something that writes its health: with each
// role's writer having done its work once, no configured role is left
// without a last success or an open state. A role added to EndpointRoles
// with no writer -- as the query backend was, reading "unknown" beside a
// pull that succeeded -- fails here.
func TestEveryEndpointRoleHasAWriter(t *testing.T) {
	cfg := writersTestConfig()
	recorder := metric.NewRecorder(metric.BuildInfo{})
	for _, client := range []string{"source", "runtime", "cmdb", "dynamic_config", "target_group", "legacy_output"} {
		noteRedisSuccess(recorder, client)
	}
	recorder.RecordPhysicalQuery(20*time.Millisecond, "")
	sink := outputSinkState{Ready: true, Since: time.Now().Add(-time.Minute), Attempts: 1}
	sharing := endpointSharing{compatOutputPresent: true}
	endpoints := endpointFactsSource(cfg, sharing, recorder, nil, nil, func() *fleet.SourceFacts { return nil },
		func() outputSinkState { return sink }, time.Now)
	byRole := map[string]fleet.Endpoint{}
	for _, entry := range endpoints() {
		byRole[entry.Role] = entry
	}
	for _, role := range fleet.EndpointRoles {
		entry, listed := byRole[role]
		if !listed || !entry.Configured {
			t.Errorf("%s: listed %v, configured %v; the test configures every role", role, listed, entry.Configured)
			continue
		}
		if entry.LastSuccessAgeSeconds == nil && (entry.Ready == nil || !*entry.Ready) {
			t.Errorf("%s has no writer: no last success and not open after its writer ran: %+v", role, entry)
		}
	}
}

// The query backend's health is every physical query's outcome: the latest
// answer and the latest failure by its closed word, an unknown word read as
// other, as the failure counter reads it.
func TestTheQueryBackendEntryReadsThePhysicalQueries(t *testing.T) {
	cfg := writersTestConfig()
	recorder := metric.NewRecorder(metric.BuildInfo{})
	entryOf := func() fleet.Endpoint {
		for _, entry := range endpointFactsSource(cfg, endpointSharing{}, recorder, nil, nil, func() *fleet.SourceFacts { return nil }, nil, time.Now)() {
			if entry.Role == fleet.EndpointQueryBackend {
				return entry
			}
		}
		t.Fatal("no query backend entry")
		return fleet.Endpoint{}
	}
	if entry := entryOf(); entry.LastSuccessAgeSeconds != nil || entry.LastFailureAgeSeconds != nil {
		t.Fatalf("before any query: %+v, want no health", entry)
	}
	recorder.RecordPhysicalQuery(time.Millisecond, "")
	if entry := entryOf(); entry.LastSuccessAgeSeconds == nil || *entry.LastSuccessAgeSeconds > 5 || entry.LastFailureAgeSeconds != nil {
		t.Fatalf("after an answer: %+v, want a recent success and no failure", entry)
	}
	recorder.RecordPhysicalQuery(time.Millisecond, "timeout")
	if entry := entryOf(); entry.LastFailureAgeSeconds == nil || entry.LastFailure != "timeout" || entry.LastSuccessAgeSeconds == nil {
		t.Fatalf("after a timeout: %+v, want the failure named timeout beside the earlier success", entry)
	}
	recorder.RecordPhysicalQuery(time.Millisecond, "a word nobody closed")
	if entry := entryOf(); entry.LastFailure != "other" {
		t.Fatalf("after an unknown failure: %+v, want it read as other", entry)
	}
}
