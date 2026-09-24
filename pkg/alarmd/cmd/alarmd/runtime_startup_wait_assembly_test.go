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
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// startupWaitFixture is a deployment whose strategy cache and runtime store
// answer and whose compatibility output's service Redis is not up yet. That
// Redis is opened last in assembly, after the CMDB index has been built and
// its maintenance started, so a wait there has the most to leave behind.
type startupWaitFixture struct {
	cfg           config.Config
	serviceRedis  string
	runtimeClient *redis.Client
	recorder      *metric.Recorder
	health        *phaseTwoApplicationHealth
	uq            *httptest.Server
}

func newStartupWaitFixture(t *testing.T) *startupWaitFixture {
	t.Helper()
	sourceAddress, sourceClient := startPhaseTwoRedis(t)
	runtimeAddress, runtimeClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := sourceClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}
	uq := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	t.Cleanup(uq.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serviceRedis := listener.Addr().String()
	_ = listener.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = runtimeAddress
	cfg.Redis.StatePrefix = "alarmd:phase-two:startup-wait"
	withCompatibilityOutput(&cfg, serviceRedis)
	platformCache := cfg.Redis.Connection()
	platformCache.Address = sourceAddress
	cfg.PlatformCache.Strategy = &platformCache
	cfg.PlatformCache.CMDB = &platformCache
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uq.URL
	return &startupWaitFixture{cfg: cfg, serviceRedis: serviceRedis, runtimeClient: runtimeClient,
		recorder: metric.NewRecorder(metric.BuildInfo{}), health: newPhaseTwoApplicationHealth(), uq: uq}
}

func (f *startupWaitFixture) open(ctx context.Context) (*phaseTwoWorkerBundle, error) {
	return openProductionPhaseTwoBundleWithDependencies(
		ctx, f.cfg, f.recorder, observability.Discard(observability.ComponentRuntime), f.health,
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: f.uq.Client(), StartupWaitInitial: 20 * time.Millisecond,
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
}

func (f *startupWaitFixture) waits(t *testing.T, dependency string) float64 {
	t.Helper()
	return counterValue(t, f.recorder, "bkmonitor_alarmd_startup_dependency_wait_total", map[string]string{"dependency": dependency})
}

// runningGoroutines reports whether any goroutine is in the named function.
func runningGoroutines(function string) bool {
	buffer := make([]byte, 1<<22)
	return strings.Contains(string(buffer[:runtime.Stack(buffer, true)]), function)
}

// The production shape of B1: a Redis the process needs is not answering
// when the replica starts. Before this change the process exited and the
// kubelet's backoff decided when it tried again. Now it stays up, not ready
// under the dependency's name, and joins once that Redis answers.
func TestAssemblyWaitsForARedisThatIsNotUpAndJoinsWhenItAnswers(t *testing.T) {
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	f := newStartupWaitFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type opened struct {
		bundle *phaseTwoWorkerBundle
		err    error
	}
	done := make(chan opened, 1)
	go func() {
		bundle, err := f.open(ctx)
		done <- opened{bundle, err}
	}()
	waitUntil(t, 10*time.Second, "the service Redis is waited on", func() bool { return f.waits(t, "redis_legacy_output") >= 2 })
	select {
	case result := <-done:
		t.Fatalf("assembly returned while its service Redis was down: %v", result.err)
	default:
	}
	snapshot := f.health.HealthSnapshot()
	if snapshot.Ready || len(snapshot.Reasons) != 1 || snapshot.Reasons[0] != observability.ReasonCode(contract.ReasonRedisUnavailable) {
		t.Fatalf("while waiting: %+v, want not ready under %s", snapshot, contract.ReasonRedisUnavailable)
	}

	_, port, err := net.SplitHostPort(f.serviceRedis)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, refused := startOwnPhaseTwoRedis(t, executable, port); refused != "" {
		t.Skipf("the service Redis port was taken meanwhile: %s", refused)
	}
	var result opened
	select {
	case result = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("assembly did not continue after the service Redis answered")
	}
	if result.err != nil {
		t.Fatalf("assembly after the service Redis answered: %v", result.err)
	}
	defer func() {
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		_ = result.bundle.Shutdown(shutdownCtx)
	}()
	if err := result.bundle.Start(ctx); err != nil {
		t.Fatalf("Start after the wait: %v", err)
	}
	for _, other := range []string{"redis_source", "redis_runtime", "ownership_store", "state_store", "cmdb_index"} {
		if got := f.waits(t, other); got != 0 {
			t.Errorf("%s was waited on %v times, want only the service Redis", other, got)
		}
	}
}

// A replica stopped while it waits leaves nothing behind: the CMDB index's
// maintenance, started before the wait, stops, and every client assembly had
// opened is closed. This is the wait on the last dependency, where the most
// had already been started.
func TestAReplicaStoppedWhileWaitingLeavesNothingRunning(t *testing.T) {
	f := newStartupWaitFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := f.open(ctx)
		done <- err
	}()
	waitUntil(t, 10*time.Second, "the service Redis is waited on", func() bool { return f.waits(t, "redis_legacy_output") >= 1 })
	if !runningGoroutines("cmd/alarmd.maintainCMDBIndex(") {
		t.Fatal("setup: the CMDB index maintenance is not running during the wait")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "redis_legacy_output") {
			t.Fatalf("err = %v, want the cancellation, naming the dependency", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("assembly did not stop with the process")
	}
	waitUntil(t, 5*time.Second, "the CMDB index maintenance stops", func() bool { return !runningGoroutines("cmd/alarmd.maintainCMDBIndex(") })
	waitUntil(t, 5*time.Second, "the runtime store's clients are closed", func() bool {
		clients, err := f.runtimeClient.ClientList(context.Background()).Result()
		return err == nil && strings.Count(strings.TrimSpace(clients), "\n") == 0
	})
}

// A Redis that answers with a refusal is not waited on: a wrong password is
// the deployment's to fix, and startup ends on it as before.
func TestAssemblyEndsOnARedisThatRefuses(t *testing.T) {
	f := newStartupWaitFixture(t)
	if err := f.runtimeClient.ConfigSet(context.Background(), "requirepass", "not-the-configured-one").Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := f.open(ctx)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("err = %v, ctx = %v, want a refusal before the deadline", err, ctx.Err())
	}
	if got := f.waits(t, "redis_runtime"); got != 0 {
		t.Fatalf("a refusing Redis was waited on %v times", got)
	}
}
