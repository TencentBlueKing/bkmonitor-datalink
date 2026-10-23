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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openalerts"
)

// rebindFixture is a process whose runtime Redis is one server and whose
// link writes its sets to another it already holds a connection to, and
// whose startup could not ask the Console: it reads nothing. The Console
// refuses until answering is set - when the background discovery reaches
// it - and then answers with the link's target.
type rebindFixture struct {
	cfg         config.Config
	linkdClient *redis.Client
	index       linkdIndex
	answering   atomic.Bool
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
	f := &rebindFixture{}
	target := openalerts.TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: "hook:open", Address: linkdAddress,
		Database: 0, Sources: []string{"source"}}
	answer := consoleFor(target)
	console := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !f.answering.Load() {
			http.Error(w, "unavailable", http.StatusBadGateway)
			return
		}
		answer(w, r)
	}))
	t.Cleanup(console.Close)
	cfg.PhaseTwo.Linkd.ConsoleURL = console.URL
	cfg.PhaseTwo.Linkd.Username, cfg.PhaseTwo.Linkd.Password = "user", "secret"
	startup := &fleet.LinkdDiscoveryFacts{Outcome: fleet.LinkdDiscoveryFailed, Attempts: linkdDiscoveryAttempts, Error: "console unreachable"}
	open := func(connection config.RedisConnectionConfig) (redis.UniversalClient, bool) {
		if sameRedisConnection(connection, cfg.RuntimeStoreRedis()) {
			return runtimeClient, false
		}
		return redis.NewClient(&redis.Options{Addr: connection.Address, DB: connection.DB}), true
	}
	index, err := newLinkdIndex(cfg, runtimeClient, cfg.RuntimeStoreRedis(), startup, open, time.Now, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if owned := index.Location.OwnedClient(); owned != nil {
			_ = owned.Close()
		}
	})
	f.cfg, f.linkdClient, f.index = cfg, linkdClient, index
	return f
}

func (f *rebindFixture) target() openalerts.TargetBinding {
	return openalerts.TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: "hook:open",
		Address: f.cfg.PlatformCache.TargetGroup.Address, Database: 0, Sources: []string{"source"}}
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

// The production shape of A3: the Console did not answer at startup. The
// sets are not read at all meanwhile - no fallback location, which answered
// for nobody - and the copy says the location is unconfirmed. The background
// discovery then finds the link, the reads move there, and the copy reads
// the set the link holds - in the same process, with nothing restarted.
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
	if current, _ := f.index.Location.binding(); current.bound() {
		t.Fatal("a location the Console did not name was bound")
	}
	go func() { _ = cache.Run(ctx) }()
	time.Sleep(300 * time.Millisecond)
	if stats := cache.Stats(); stats.Loaded != 0 || stats.UnavailableReason != openalerts.UnavailableLocationUnconfirmed {
		t.Fatalf("loaded %d reason %q before the Console answered, want nothing read and the location unconfirmed", stats.Loaded, stats.UnavailableReason)
	}

	failures := 2
	discover := func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		if failures > 0 {
			failures--
			return openalerts.TargetBinding{}, errors.New("console unreachable")
		}
		f.answering.Store(true)
		return f.target(), nil
	}
	go f.index.Location.retry(ctx, f.cfg, discover, 10*time.Millisecond, 20*time.Millisecond)

	waitUntil(t, 10*time.Second, "the copy reads the link's set after the move", func() bool {
		stats := cache.Stats()
		return stats.Loaded == 1 && stats.Members == 1 && stats.LocationConfirmed
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

// Only a failed startup discovery is asked again. An adopted location is an
// answer, and a Console that answers with a Redis this
// process holds no connection to is an answer too: it is recorded and the
// retry stops, rather than asking the same question forever.
func TestOnlyAFailedDiscoveryIsAskedAgainAndAnAnswerEndsIt(t *testing.T) {
	for _, outcome := range []string{fleet.LinkdDiscoveryAdopted, fleet.LinkdDiscoveryNoHeldConnection} {
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
	if facts := location.Discovery(); calls != 1 || facts.Outcome != fleet.LinkdDiscoveryNoHeldConnection || facts.TargetCount != 1 {
		t.Fatalf("calls %d facts %+v, want one call ending in no_held_connection, the one target listed", calls, facts)
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

// A move of the reads forgets what the copy held from before it: a
// strategy's calibration made at the place the Console named is gone the
// moment the reads move elsewhere, before anything is read there, so no
// strategy answers from the old place as the link's word.
func TestAMoveOfTheReadsForgetsWhatTheCopyHeld(t *testing.T) {
	f := newRebindFixture(t)
	f.answering.Store(true)
	ctx := context.Background()
	cache := f.index.Cache
	if err := cache.SetTracked([]openalerts.StrategyKey{{TenantID: "tenant", StrategyID: "1001"}}); err != nil {
		t.Fatal(err)
	}
	// The first round finds the link's place and moves there; a later one
	// confirms it and calibrates the strategy at it.
	waitUntil(t, 5*time.Second, "the strategy calibrated at the place the Console names", func() bool {
		cache.RequestCalibration()
		cache.Refresh(ctx)
		return cache.Stats().Calibrated == 1
	})
	if err := f.index.Location.rebind(f.cfg.RuntimeStoreRedis(), "elsewhere:open"); err != nil {
		t.Fatal(err)
	}
	if stats := cache.Stats(); stats.Calibrated != 0 || stats.Members != 0 {
		t.Fatalf("calibrated %d members %d right after the move, want nothing held from the old place", stats.Calibrated, stats.Members)
	}
}

// The Console comes back and a reconciliation finds the link before the
// startup retry asks again: the reconciliation moves the reads, and the
// retry, waking later, stops without asking and without moving the reads to
// the same place a second time - which would forget everything read there.
func TestARetryAfterAReconciliationFoundTheLinkLeavesTheReadsAlone(t *testing.T) {
	f := newRebindFixture(t)
	f.answering.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cache := f.index.Cache
	if err := cache.SetTracked([]openalerts.StrategyKey{{TenantID: "tenant", StrategyID: "1001"}}); err != nil {
		t.Fatal(err)
	}
	var asked atomic.Int64
	discover := func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		asked.Add(1)
		return f.target(), nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.index.Location.retry(ctx, f.cfg, discover, 300*time.Millisecond, time.Second)
	}()
	waitUntil(t, 5*time.Second, "the reconciliation moved the reads and the strategy is calibrated there", func() bool {
		cache.RequestCalibration()
		cache.Refresh(ctx)
		return cache.Stats().Calibrated == 1
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the retry kept running after a reconciliation found the link")
	}
	if got := asked.Load(); got != 0 {
		t.Fatalf("the retry asked the Console %d times after the reads had moved, want none", got)
	}
	if stats := cache.Stats(); stats.Calibrated != 1 {
		t.Fatalf("calibrated %d after the retry, want the calibration made at the place kept", stats.Calibrated)
	}
	if facts := f.index.Location.Discovery(); facts.Outcome != fleet.LinkdDiscoveryAdopted {
		t.Fatalf("discovery outcome %q, want adopted", facts.Outcome)
	}
}

// A reconciliation can move the reads while the retry is asking the
// Console: the retry then finds them already where the answer names and
// does not move them again - the connection the move opened is the one kept.
func TestARetryAnsweredWhileAReconciliationMovedTheReadsDoesNotMoveThemAgain(t *testing.T) {
	f := newRebindFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var moved redis.UniversalClient
	discover := func(context.Context, openalerts.HTTPReconcilerOptions) (openalerts.TargetBinding, error) {
		// The reconciliation lands between the retry's question and its move.
		f.index.Location.relocate(f.target())
		moved = f.index.Location.OwnedClient()
		return f.target(), nil
	}
	f.index.Location.retry(ctx, f.cfg, discover, time.Millisecond, time.Millisecond)
	if moved == nil {
		t.Fatal("fixture: the reconciliation did not move the reads to the link's Redis")
	}
	if f.index.Location.OwnedClient() != moved {
		t.Fatal("the retry moved the reads again to where they already were")
	}
	if facts := f.index.Location.Discovery(); facts.Outcome != fleet.LinkdDiscoveryAdopted {
		t.Fatalf("discovery outcome %q, want adopted", facts.Outcome)
	}
}
