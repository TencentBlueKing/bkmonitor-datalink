package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/go-redis/redis/v8"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestUnlimitedRunnerRecoveryWaitDoesNotBlockNormal(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, redisClient)
	rawThird, err := redisClient.Get(ctx, "alarm-config.strategy_1002").Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var third map[string]any
	if err = json.Unmarshal(rawThird, &third); err != nil {
		t.Fatal(err)
	}
	third["id"] = 1003
	item := third["items"].([]any)[0].(map[string]any)
	item["id"] = 13
	item["query_md5"] = "independent-third-query"
	item["query_configs"].([]any)[0].(map[string]any)["result_table_id"] = "system.disk"
	rawThird, err = json.Marshal(third)
	if err != nil {
		t.Fatal(err)
	}
	if err = redisClient.Set(ctx, "alarm-config.strategy_1003", rawThird, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err = redisClient.Set(ctx, "alarm-config.strategy_ids", "[1001,1002,1003]", 0).Err(); err != nil {
		t.Fatal(err)
	}

	// Scheduler time is frozen, but the real HTTP transport uses wall-clock
	// context deadlines. Keep fixture deadlines ahead of setup/race overhead;
	// the logical one-second Slot stays unchanged.
	base := time.Now().Add(time.Minute).Unix()
	var clock atomic.Int64
	clock.Store(time.Unix(base, 0).UnixMilli())
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	firstEntered := make(chan struct{})
	secondEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFirst) }) }
	var uqCalls atomic.Int64
	var recoveryCalls atomic.Int64
	var normalTable atomic.Value
	var normalOnce sync.Once
	var inflight atomic.Int64
	var maxInflight atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		raw, _ := io.ReadAll(request.Body)
		uqCalls.Add(1)
		current := inflight.Add(1)
		defer inflight.Add(-1)
		for {
			maximum := maxInflight.Load()
			if current <= maximum || maxInflight.CompareAndSwap(maximum, current) {
				break
			}
		}
		if bytes.Contains(raw, []byte(normalTable.Load().(string))) {
			normalOnce.Do(func() { close(secondEntered) })
		} else if recoveryCalls.Add(1) == 1 {
			close(firstEntered)
			<-releaseFirst
		}

		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"shared-permit","is_partial":false,"result_table_id":[]}`))
	}))
	defer func() {
		release()
		uqServer.Close()
	}()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g3b-shared-query-permits"
	withCompatibilityOutput(&cfg, address)
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit = 0
	cfg.PhaseTwo.Scheduler.ProcessQueryPermits = 2
	cfg.PhaseTwo.Scheduler.RecoveryQueryPermits = 1
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)

	queued := make(chan struct{}, 1)
	var active, normalAdmissions atomic.Int64
	var lastWaiting atomic.Int64
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if observation.Dispatcher != nil {
					active.Store(int64(observation.Dispatcher.Active))
				}
				if observation.Stage == observability.StageQueryAdmission && observation.Operation == observability.OperationNormal {
					normalAdmissions.Add(1)
				}
				if observation.Stage == observability.StageQueryAdmission && observation.Result == observability.ResultStarted &&
					observation.Operation == observability.OperationReplay && observation.QueryPermit != nil &&
					observation.QueryPermit.QueueKind == observability.QueryQueueRecovery && observation.QueryPermit.RecoveryWaiting > 0 {
					lastWaiting.Store(int64(observation.QueryPermit.RecoveryWaiting))
					select {
					case queued <- struct{}{}:
					default:
					}
				}
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	defer func() {
		if err := bundle.Shutdown(ctx); err != nil {
			t.Errorf("phase-two production Shutdown() error = %v", err)
		}
	}()
	defer release()
	productionOwnership := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	if productionOwnership.flights == nil {
		t.Fatal("production ownership has no process-wide FlightCoordinator")
	}
	bundle.mu.RLock()
	runnerCount := len(bundle.runners)
	bundle.mu.RUnlock()
	if runnerCount != 3 {
		t.Fatalf("production runners = %d, want three owned Query Groups", runnerCount)
	}
	// Pick last lexical QG as normal: production dispatcher serves both earlier recovery QGs first.
	// Seed only after publication: recovery Slots are inside the actual Snapshot segment.
	groups := append([]execution.QueryGroupIdentity(nil), bundle.queryGroups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i] < groups[j] })
	normalGroup := groups[2]
	normalSchedule, err := productionOwnership.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, normalGroup)
	if err != nil {
		t.Fatal(err)
	}
	rawNormal, err := redisClient.Get(ctx, "alarm-config.strategy_"+normalSchedule.Plans[0].Identity.StrategyID).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	var normalDocument map[string]any
	if err = json.Unmarshal(rawNormal, &normalDocument); err != nil {
		t.Fatal(err)
	}
	normalTable.Store(normalDocument["items"].([]any)[0].(map[string]any)["query_configs"].([]any)[0].(map[string]any)["result_table_id"].(string))

	for _, seedGroup := range groups {
		next := base
		if seedGroup == normalGroup {
			next = base + 1
		}
		progress := execution.ScheduleProgress{Identity: execution.ProgressIdentity{QueryGroup: seedGroup}, NextSlot: execution.EvaluationTime(next), LastFullSlot: execution.EvaluationTime(next - 1)}
		if err := progress.Validate(); err != nil {
			t.Fatal(err)
		}
		rawProgress, err := json.Marshal(struct {
			Schema   string                     `json:"schema"`
			Progress execution.ScheduleProgress `json:"progress"`
		}{"alarmd-schedule-progress-v2", progress})
		if err != nil {
			t.Fatal(err)
		}
		keyDigest := sha256.Sum256([]byte(seedGroup))
		key := fmt.Sprintf("%s:{%x}:%s:progress", productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership"), keyDigest, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "schedule"))
		if err := redisClient.Set(ctx, key, rawProgress, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	clock.Store(time.Unix(base+1, 0).Add(500 * time.Millisecond).UnixMilli())

	tickDone := make(chan error, 1)
	go func() { tickDone <- bundle.runScheduledOnce(ctx) }()
	select {
	case <-firstEntered:
	case err := <-tickDone:
		t.Fatalf("production tick returned before first UQ: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("first owned Query Group did not enter UQ")
	}
	select {
	case <-secondEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("healthy Normal blocked behind recovery while P is available")
	}
	limit := time.Now().Add(3 * time.Second)
	for {
		current := loadPhaseTwoProgress(t, ctx, productionOwnership, normalGroup)
		if current.NextSlot == execution.EvaluationTime(base+2) {
			break
		}
		if time.Now().After(limit) {
			t.Fatalf("healthy Normal did not commit before recovery release: %+v", current)
		}
		time.Sleep(time.Millisecond)
	}
	t.Logf("normal committed while recovery HTTP remains blocked; active=%d Rwait=%d", active.Load(), lastWaiting.Load())
	release()
	if err := <-tickDone; err != nil {
		t.Fatalf("production tick error = %v", err)
	}

	if uqCalls.Load() != 3 || normalAdmissions.Load() == 0 {
		t.Fatalf("process UQ calls/max inflight = %d/%d, want 3 calls and normal admission", uqCalls.Load(), maxInflight.Load())
	}
	after := loadPhaseTwoProgress(t, ctx, productionOwnership, normalGroup)
	if after.LastFullSlot != execution.EvaluationTime(base+1) || after.NextSlot != execution.EvaluationTime(base+2) {
		t.Fatalf("normal failed real FULL: %+v", after)
	}
	t.Logf("released: HTTP=%d normalAdmissionObservations=%d normal FULL=%d Next=%d", uqCalls.Load(), normalAdmissions.Load(), after.LastFullSlot, after.NextSlot)
}
