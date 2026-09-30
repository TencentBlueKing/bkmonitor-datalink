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
	"errors"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// rebindFixture is a process whose runtime Redis is one server and whose
// link writes its sets to another it already holds a connection to, and
// whose startup could not ask the Console: it reads the fallback location.
type rebindFixture struct {
	cfg         config.Config
	linkdClient *redis.Client
	index       linkdIndex
}

func newRebindFixture(t *testing.T) *rebindFixture {
	t.Helper()
	runtimeAddress, runtimeClient := startPhaseTwoRedis(t)
	linkdAddress, linkdClient := startPhaseTwoRedis(t)
	var cfg config.Config
	cfg.Redis.Mode, cfg.Redis.Address = config.RedisModeStandalone, runtimeAddress
	prefix := "platform"
	cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
	cfg.PlatformCache.TargetGroup = &config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: linkdAddress}
	cfg.PhaseTwo.Linkd.ConsoleURL = "http://console/base"
	cfg.PhaseTwo.Linkd.Username, cfg.PhaseTwo.Linkd.Password = "user", "secret"
	startup := &fleet.LinkdDiscoveryFacts{Outcome: fleet.LinkdDiscoveryFailed, Attempts: linkdDiscoveryAttempts, Error: "console unreachable"}
	open := func(connection config.RedisConnectionConfig) (redis.UniversalClient, bool) {
		if sameRedisConnection(connection, cfg.RuntimeStoreRedis()) {
			return runtimeClient, false
		}
		return redis.NewClient(&redis.Options{Addr: connection.Address, DB: connection.DB}), true
	}
	index, err := newLinkdIndex(cfg, runtimeClient, cfg.RuntimeStoreRedis(), startup, open, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if owned := index.Location.OwnedClient(); owned != nil {
			_ = owned.Close()
		}
	})
	return &rebindFixture{cfg: cfg, linkdClient: linkdClient, index: index}
}

func (f *rebindFixture) target() openalerts.TargetBinding {
	return openalerts.TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: "hook:open",
		Address: f.cfg.PlatformCache.TargetGroup.Address, Database: 0}
}

func waitUntil(t *testing.T, within time.Duration, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("not within %s: %s", within, what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// The production shape of A3: the Console did not answer at startup, so the
// sets were read from the fallback location, where they are not. Before this
// change that held until someone restarted the pod. Now the background
// discovery finds the link, the reads move, and the copy sees the alert the
// link holds - in the same process, with nothing restarted.
func TestAProcessThatCouldNotAskAtStartupMovesItsReadsWhenTheConsoleAnswers(t *testing.T) {
	f := newRebindFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	key := openalerts.StrategyKey{TenantID: "tenant", StrategyID: "1001"}
	if err := f.linkdClient.SAdd(ctx, "hook:open:tenant:1001", "fp-open").Err(); err != nil {
		t.Fatal(err)
	}
	cache := f.index.Cache
	if err := cache.SetTracked([]openalerts.StrategyKey{key}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cache.Run(ctx) }()
	waitUntil(t, 5*time.Second, "the fallback location is read", func() bool { return cache.Stats().Loaded == 1 })
	if cache.Contains(key.TenantID, key.StrategyID, "fp-open") {
		t.Fatal("setup: the fallback location already holds the link's set")
	}

	failures := 2
	discover := func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		if failures > 0 {
			failures--
			return openalerts.TargetBinding{}, errors.New("console unreachable")
		}
		return f.target(), nil
	}
	go f.index.Location.retry(ctx, f.cfg, discover, 10*time.Millisecond, 20*time.Millisecond)

	waitUntil(t, 10*time.Second, "the copy reads the link's set after the move", func() bool {
		return cache.Contains(key.TenantID, key.StrategyID, "fp-open")
	})
	facts := f.index.Location.Discovery()
	if facts.Outcome != fleet.LinkdDiscoveryAdopted || facts.Attempts != linkdDiscoveryAttempts+3 || facts.Error != "" ||
		facts.Target == nil || facts.Target.KeyPrefix != "hook:open" {
		t.Fatalf("discovery facts = %+v, want adopted after two more failures and one answer", facts)
	}
	connection, prefix, moved := f.index.Location.Location()
	if !moved || prefix != "hook:open" || connection.Address != f.cfg.PlatformCache.TargetGroup.Address {
		t.Fatalf("location = %+v %q moved %v", connection, prefix, moved)
	}
}

// Only a failed startup discovery is asked again. An adopted location and a
// stated connection are answers, and a Console that answers with a Redis this
// process holds no connection to is an answer too: it is recorded and the
// retry stops, rather than asking the same question forever.
func TestOnlyAFailedDiscoveryIsAskedAgainAndAnAnswerEndsIt(t *testing.T) {
	for _, outcome := range []string{fleet.LinkdDiscoveryAdopted, fleet.LinkdDiscoveryConnectionStated, fleet.LinkdDiscoveryNoHeldConnection} {
		calls := 0
		location := &linkdLocationSwitch{discovery: fleet.LinkdDiscoveryFacts{Outcome: outcome}, replaced: make(chan struct{})}
		location.retry(context.Background(), linkdLocationConfig(), func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
			calls++
			return openalerts.TargetBinding{}, nil
		}, time.Millisecond, time.Millisecond)
		if calls != 0 {
			t.Fatalf("%s was asked again %d times", outcome, calls)
		}
	}
	calls := 0
	location := &linkdLocationSwitch{discovery: fleet.LinkdDiscoveryFacts{Outcome: fleet.LinkdDiscoveryFailed}, replaced: make(chan struct{})}
	location.retry(context.Background(), linkdLocationConfig(), func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		calls++
		return openalerts.TargetBinding{KeyPrefix: "hook:open", Address: "elsewhere:6379", Database: 2}, nil
	}, time.Millisecond, time.Millisecond)
	if facts := location.Discovery(); calls != 1 || facts.Outcome != fleet.LinkdDiscoveryNoHeldConnection {
		t.Fatalf("calls %d facts %+v, want one call ending in no_held_connection", calls, facts)
	}
	if _, _, moved := location.Location(); moved {
		t.Fatal("a location this process holds no connection to was moved to")
	}
}

// The retry ends with the process: a cancelled context stops it between
// attempts instead of leaving a goroutine asking a Console after shutdown.
func TestTheRetryStopsWithTheProcess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	location := &linkdLocationSwitch{discovery: fleet.LinkdDiscoveryFacts{Outcome: fleet.LinkdDiscoveryFailed}, replaced: make(chan struct{})}
	done := make(chan struct{})
	go func() {
		location.retry(ctx, linkdLocationConfig(), func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
			return openalerts.TargetBinding{}, errors.New("console unreachable")
		}, time.Millisecond, time.Millisecond)
		close(done)
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the retry did not stop with its context")
	}
}

// The page names where the sets are read now: after a move the open alert
// set's entry carries the discovered location, not the fallback it was
// resolved with at startup.
func TestTheEndpointListFollowsAMove(t *testing.T) {
	console := consoleTestServer(t, browsePage(nil, ""))
	fallback := redisEndpoint(fleet.EndpointOpenAlertSet, config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: "runtime:6379", DB: 8}, "alarmd:open_alerts")
	fallback.SharedWith = fleet.EndpointStateRedis
	endpoints := func() []fleet.Endpoint { return []fleet.Endpoint{fallback} }
	location := &linkdLocationSwitch{replaced: make(chan struct{})}
	entry := withLinkdConsole(endpoints, console, location, time.Now)()[0]
	if entry.Address != "runtime:6379" || entry.SharedWith != fleet.EndpointStateRedis {
		t.Fatalf("before a move: %+v", entry)
	}
	location.current = linkdBinding{connection: config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: "platform-redis:6379", DB: 3}, prefix: "hook:open"}
	location.moved = true
	entry = withLinkdConsole(endpoints, console, location, time.Now)()[0]
	if entry.Address != "platform-redis:6379" || entry.DB == nil || *entry.DB != 3 || entry.Prefix != "hook:open" || entry.SharedWith != "" {
		t.Fatalf("after a move: %+v", entry)
	}
}
