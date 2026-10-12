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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// A process whose startup discovery failed read the open alert sets from a
// fallback location, its runtime Redis under alarmd:open_alerts, and the
// gate answered from it as the link's word. Where the link writes elsewhere
// - on one deployment the link writes a standalone Redis and the process
// holds a sentinel one - that answer was an empty set, and the recovery of
// every alert this process had not opened was held. Now nothing is read
// until the Console names the location, the copy says why, and the gate
// answers from what this process sent. Wired as production wires it: the
// bundle's own discovery, against a Console that refuses.
func TestAnUnconfirmedSetLocationIsNotRead(t *testing.T) {
	defer func(pause time.Duration) { linkdDiscoveryPause = pause }(linkdDiscoveryPause)
	linkdDiscoveryPause = 0
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusBadGateway)
	}))
	defer console.Close()
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd:phase-two:unconfirmed-location"
	cfg.PhaseTwo.Linkd.ConsoleURL = console.URL
	cfg.PhaseTwo.Linkd.Username, cfg.PhaseTwo.Linkd.Password = "reader", "secret"
	key := openalerts.StrategyKey{TenantID: "tenant-a", StrategyID: "123"}
	// The fallback location holds a set for the strategy: a process reading
	// it answers from it.
	if err := client.SAdd(ctx, "alarmd:open_alerts:tenant-a:123", "theirs").Err(); err != nil {
		t.Fatal(err)
	}
	fallbackCommands := redisCommandsOn(t, address, "alarmd:open_alerts")
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: http.DefaultClient,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		},
	)
	if err != nil {
		t.Fatalf("open production bundle: %v", err)
	}
	defer func() { _ = bundle.Shutdown(ctx) }()
	port, ok := bundle.workerPorts.OpenAlerts.(*openAlertCopyPort)
	if !ok || port.cache == nil {
		t.Fatalf("fixture: the bundle's open alert port is %T", bundle.workerPorts.OpenAlerts)
	}
	copy := port.cache
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = copy.Run(runCtx) }()
	defer func() { cancel(); <-done }()
	if err := copy.TrackOwned(key); err != nil {
		t.Fatal(err)
	}
	// Give the copy every chance to read: rounds until its subscription has
	// had time to come up and a read of a reachable location would land.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		copy.Refresh(ctx)
		time.Sleep(20 * time.Millisecond)
	}
	if copy.Contains(key.TenantID, key.StrategyID, "theirs") {
		t.Fatal("the gate answered from the fallback location's set, which the Console never named")
	}
	stats := copy.Stats()
	if stats.Loaded != 0 || !stats.IndexReadAt.IsZero() {
		t.Fatalf("loaded %d read at %s: the fallback location was read", stats.Loaded, stats.IndexReadAt)
	}
	if stats.Available || string(stats.UnavailableReason) != "location_unconfirmed" {
		t.Fatalf("available %v reason %q, want unavailable because the location is unconfirmed", stats.Available, stats.UnavailableReason)
	}
	if got := stats.Lookups[openalerts.AnswerSelfMaintained]; got != 1 {
		t.Fatalf("self_maintained lookups %d (all %v), want the gate's answer to be the copy's own", got, stats.Lookups)
	}
	if commands := fallbackCommands(); len(commands) != 0 {
		t.Fatalf("the Redis ran %d commands at the fallback prefix, want none: %v", len(commands), commands)
	}
}

// consoleAt is a link Console answering with one target, its event source
// keyed by the alert id, and a reconciliation listing fp-open as matched.
func consoleAt(t *testing.T, target openalerts.TargetBinding) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(consoleFor(target))
	t.Cleanup(server.Close)
	return server
}

// consoleFor is consoleAt's handler.
func consoleFor(target openalerts.TargetBinding) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/local-api/strategy-index/targets":
			_ = json.NewEncoder(w).Encode([]openalerts.TargetBinding{target})
		case "/local-api/event-sources/" + target.EventSourceID:
			_ = json.NewEncoder(w).Encode(map[string]any{"id": target.EventSourceID, "revision": 1, "published": 1,
				"spec": map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id"}})
		default:
			q := r.URL.Query()
			tenant, strategy := q.Get("bk_tenant_id"), q.Get("strategy_id")
			_ = json.NewEncoder(w).Encode(map[string]any{"target": target, "tenantId": tenant, "strategyId": strategy,
				"key": target.KeyPrefix + ":" + tenant + ":" + strategy, "complete": true,
				"redis": map[string]any{"complete": true}, "alerts": map[string]any{"complete": true},
				"rows": []any{map[string]any{"fingerprint": "fp-open", "status": "matched",
					"alerts": []openalerts.Alert{{AlertID: "a", EventSourceID: target.EventSourceID, Fingerprint: "fp-open", Severity: "critical"}}}}})
		}
	}
}

// The process reads where its startup discovery said, and later the link
// writes elsewhere - a Redis this process also holds. The first
// reconciliation that sees it withdraws the confirmation and moves the reads
// to the place the Console now names; the calibration that follows is asked
// for at once, not after the 30-minute interval, and confirms it. Until
// then the gate answers from what this process sent, and the move is
// counted as an entry into location_unconfirmed.
func TestAReconciliationThatFindsTheLinkElsewhereMovesTheReads(t *testing.T) {
	runtimeAddress, runtimeClient := startPhaseTwoRedis(t)
	linkdAddress, linkdClient := startPhaseTwoRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	target := openalerts.TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: "hook:open", Address: linkdAddress, Database: 0,
		Sources: []string{"source"}}
	console := consoleAt(t, target)
	var cfg config.Config
	cfg.Redis.Mode, cfg.Redis.Address = config.RedisModeStandalone, runtimeAddress
	prefix := "platform"
	cfg.PlatformCache.DynamicGroupKeyPrefix = &prefix
	cfg.PlatformCache.TargetGroup = &config.RedisConnectionConfig{Mode: config.RedisModeStandalone, Address: linkdAddress}
	cfg.PhaseTwo.Linkd.ConsoleURL = console.URL
	cfg.PhaseTwo.Linkd.Username, cfg.PhaseTwo.Linkd.Password = "user", "secret"
	// Adopted at startup: the runtime Redis under the old prefix.
	adopted := cfg.RuntimeStoreRedis()
	cfg.PhaseTwo.Linkd.Connection, cfg.PhaseTwo.Linkd.KeyPrefix = &adopted, "old:open"
	key := openalerts.StrategyKey{TenantID: "tenant", StrategyID: "1001"}
	if err := linkdClient.SAdd(ctx, "hook:open:tenant:1001", "fp-open").Err(); err != nil {
		t.Fatal(err)
	}
	linkCommands := redisCommandsOn(t, linkdAddress, "hook:open")
	open := func(connection config.RedisConnectionConfig) (redis.UniversalClient, bool) {
		if sameRedisConnection(connection, cfg.RuntimeStoreRedis()) {
			return runtimeClient, false
		}
		return redis.NewClient(&redis.Options{Addr: connection.Address, DB: connection.DB}), true
	}
	startup := &fleet.LinkdDiscoveryFacts{Outcome: fleet.LinkdDiscoveryAdopted, Attempts: 1}
	index, err := newLinkdIndex(cfg, runtimeClient, adopted, startup, open, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if owned := index.Location.OwnedClient(); owned != nil {
			_ = owned.Close()
		}
	})
	cache := index.Cache
	if err := cache.SetTracked([]openalerts.StrategyKey{key}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cache.Run(ctx) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		stats := cache.Stats()
		if stats.LocationConfirmed && stats.Available && stats.Members == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not moved and confirmed: confirmed %v available %v members %d loaded %d reason %q discovery %+v console %+v",
				stats.LocationConfirmed, stats.Available, stats.Members, stats.Loaded, stats.UnavailableReason, index.Location.Discovery(), index.Console.Record())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !cache.Contains(key.TenantID, key.StrategyID, "fp-open") {
		t.Fatal("after the move the gate did not answer from the link's set")
	}
	if got := cache.Stats().Unavailable[openalerts.UnavailableLocationUnconfirmed]; got != 1 {
		t.Fatalf("location_unconfirmed counted %d, want the move counted once", got)
	}
	if commands := linkCommands(); len(commands) == 0 {
		t.Fatal("the link's Redis ran no command at its prefix: the reads did not move, or the count sees nothing")
	}
	// From here on the old place is read no more, however many rounds run.
	oldCommands := redisCommandsOn(t, runtimeAddress, "old:open")
	cache.RequestCalibration()
	for round := 0; round < 5; round++ {
		cache.Refresh(ctx)
	}
	if commands := oldCommands(); len(commands) != 0 {
		t.Fatalf("the old place ran %d commands after the move, want none: %v", len(commands), commands)
	}
	facts := index.Location.Discovery()
	if facts.Outcome != fleet.LinkdDiscoveryAdopted || facts.Target == nil || facts.Target.KeyPrefix != "hook:open" {
		t.Fatalf("discovery facts = %+v, want adopted at the place the reconciliation found", facts)
	}
}

// A deployment without the link's Console is not configured: nothing is
// read, not even from where a fallback would have read, nothing is counted
// as unavailable, and the gate answers from what this process sent.
func TestACopyWithoutAConsoleReadsNothingAndCountsNothing(t *testing.T) {
	runtimeAddress, runtimeClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	var cfg config.Config
	cfg.Redis.Mode, cfg.Redis.Address = config.RedisModeStandalone, runtimeAddress
	if err := runtimeClient.SAdd(ctx, "alarmd:open_alerts:tenant:1001", "theirs").Err(); err != nil {
		t.Fatal(err)
	}
	setCommands := redisCommandsOn(t, runtimeAddress, "alarmd:open_alerts")
	open := func(config.RedisConnectionConfig) (redis.UniversalClient, bool) { return runtimeClient, false }
	index, err := newLinkdIndex(cfg, runtimeClient, cfg.RuntimeStoreRedis(), nil, open, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	cache := index.Cache
	key := openalerts.StrategyKey{TenantID: "tenant", StrategyID: "1001"}
	if err := cache.SetTracked([]openalerts.StrategyKey{key}); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = cache.Run(runCtx) }()
	defer func() { cancel(); <-done }()
	for round := 0; round < 5; round++ {
		cache.Refresh(ctx)
		time.Sleep(20 * time.Millisecond)
	}
	stats := cache.Stats()
	if stats.Configured || stats.Loaded != 0 || stats.UnavailableReason != "" {
		t.Fatalf("configured %v loaded %d reason %q, want not configured and nothing read", stats.Configured, stats.Loaded, stats.UnavailableReason)
	}
	for reason, count := range stats.Unavailable {
		if count != 0 {
			t.Fatalf("unavailable %s counted %d times on a copy without a Console", reason, count)
		}
	}
	if cache.Contains(key.TenantID, key.StrategyID, "theirs") {
		t.Fatal("the gate answered from a set nobody named")
	}
	if commands := setCommands(); len(commands) != 0 {
		t.Fatalf("the Redis ran %d commands at the sets' prefix for a copy without a Console, want none: %v", len(commands), commands)
	}
}

// An unbound location has nothing to subscribe to: Watch waits for a
// binding or the end of its context, rather than returning at once and
// having the copy subscribe again in a tight loop.
func TestAnUnboundLocationWaitsInsteadOfSubscribing(t *testing.T) {
	location := &linkdLocationSwitch{replaced: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- location.Watch(ctx, func(bool) {}, func(openalerts.StrategyKey) {}, func(openalerts.NoticeRefusal) {})
	}()
	select {
	case err := <-done:
		t.Fatalf("Watch returned %v on an unbound location", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := location.ReadSet(ctx, openalerts.StrategyKey{TenantID: "t", StrategyID: "1"}); err != openalerts.ErrLocationUnconfirmed {
		t.Fatalf("ReadSet = %v, want the location named unconfirmed", err)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("Watch returned nil on its context's end, which reads as a move")
	}
}

// A reconciliation that finds the link writing to a Redis this process holds
// no connection to stops every read: the place read until then is not the
// Console's any more, and none is bound until one is named that can be read.
func TestALinkMovedOutOfReachUnbindsTheReads(t *testing.T) {
	f := newRebindFixture(t)
	if err := f.index.Location.rebind(f.cfg.RuntimeStoreRedis(), "old:open"); err != nil {
		t.Fatal(err)
	}
	if current, _ := f.index.Location.binding(); !current.bound() {
		t.Fatal("fixture: the location was not bound")
	}
	f.index.Location.relocate(openalerts.TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: "hook:open",
		Address: "192.0.2.50:6379", Database: 0, Sources: []string{"source"}})
	if current, _ := f.index.Location.binding(); current.bound() {
		t.Fatal("the reads stayed bound to a place the Console no longer names")
	}
	if facts := f.index.Location.Discovery(); facts.Outcome != fleet.LinkdDiscoveryNoHeldConnection || facts.Target == nil || facts.Target.Address != "192.0.2.50:6379" {
		t.Fatalf("discovery facts = %+v, want no held connection at the named place", facts)
	}
	// Rounds with a strategy tracked run no command at the place unbound.
	oldCommands := redisCommandsOn(t, f.cfg.RuntimeStoreRedis().Address, "old:open")
	if err := f.index.Cache.SetTracked([]openalerts.StrategyKey{{TenantID: "tenant", StrategyID: "1001"}}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = f.index.Cache.Run(ctx) }()
	for round := 0; round < 5; round++ {
		f.index.Cache.RequestCalibration()
		f.index.Cache.Refresh(ctx)
		time.Sleep(10 * time.Millisecond)
	}
	if commands := oldCommands(); len(commands) != 0 {
		t.Fatalf("the unbound place ran %d commands, want none: %v", len(commands), commands)
	}
}

// redisCommandsOn records every command the Redis at address runs from now
// on, through MONITOR on a connection of its own, and returns the ones whose
// arguments name prefix when stop is called. What it counts is what the
// server ran, whichever client sent it - not what the copy says it read.
func redisCommandsOn(t *testing.T, address, prefix string) (stop func() []string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.Write([]byte("MONITOR\r\n")); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	if line, err := reader.ReadString('\n'); err != nil || !strings.HasPrefix(line, "+OK") {
		t.Fatalf("MONITOR answered %q, %v", line, err)
	}
	marker := fmt.Sprintf("monitor-end-%d", time.Now().UnixNano())
	lines := make(chan string, 1024)
	go func() {
		defer close(lines)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			lines <- line
		}
	}()
	var named []string
	collected := make(chan struct{})
	go func() {
		defer close(collected)
		for line := range lines {
			if strings.Contains(line, marker) {
				return
			}
			if strings.Contains(line, prefix) {
				named = append(named, strings.TrimSpace(line))
			}
		}
	}()
	return func() []string {
		t.Helper()
		// The server streams commands in the order it runs them, so once
		// this ECHO is seen every command before it has been.
		probe := redis.NewClient(&redis.Options{Addr: address})
		defer func() { _ = probe.Close() }()
		if err := probe.Echo(context.Background(), marker).Err(); err != nil {
			t.Fatal(err)
		}
		select {
		case <-collected:
		case <-time.After(5 * time.Second):
			t.Fatal("MONITOR never streamed the end marker")
		}
		return named
	}
}

// A notice the bound subscriber drops reaches the copy through the switch as
// a refusal, so the copy's count is the link's, whichever place it is bound
// to.
func TestARefusedNoticeReachesTheCopyThroughTheLocationSwitch(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	binding, err := newLinkdBinding(client, config.RedisConnectionConfig{}, "test.linkd", openalerts.ReadLimits{MaxMembers: 10, MaxBytes: 1 << 10, MaxPages: 1, PageSize: 10})
	if err != nil {
		t.Fatal(err)
	}
	location := &linkdLocationSwitch{current: binding, replaced: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan bool, 4)
	refused := make(chan openalerts.NoticeRefusal, 4)
	done := make(chan error, 1)
	go func() {
		done <- location.Watch(ctx, func(value bool) { ready <- value }, func(openalerts.StrategyKey) {},
			func(reason openalerts.NoticeRefusal) { refused <- reason })
	}()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("the bound subscriber was not acknowledged")
	}
	if err := client.Publish(ctx, "test.linkd:changes", `{"bk_tenant_id":"t","strategy_id":"1","key":"foreign"}`).Err(); err != nil {
		t.Fatal(err)
	}
	select {
	case reason := <-refused:
		if reason != openalerts.NoticeUndecodable {
			t.Fatalf("refused %q, want undecodable", reason)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the refusal did not reach the copy through the switch")
	}
	cancel()
	<-done
}
