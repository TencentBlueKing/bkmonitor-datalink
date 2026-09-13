// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// TestProductionPhaseTwoRefreshActivatesTheStrandedLatestWhileTheSourceKeepsChanging:
// a publication that an earlier round confirmed and published but never
// activated (the process stopped between the two) is still the one the
// fleet should execute. A periodic Refresh that finds the source changed
// again reports the new candidate as pending; it used to return there, so
// the stranded publication was only activated by a later round that
// published, and a source that changed on every round kept the activation
// frozen on a publication whose payload had already expired, with no way
// out. InitialRefresh loops on pending and never had this gap. The pending
// round now catches the activation up to the latest publication, and says
// so on the refresh line.
func TestProductionPhaseTwoRefreshActivatesTheStrandedLatestWhileTheSourceKeepsChanging(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd:phase-two:pending-activation"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	var nowUnix atomic.Int64
	nowUnix.Store(time.Now().Unix())
	var refreshMu sync.Mutex
	var refreshes []observability.SourceRefreshFacts
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: func() time.Time { return time.Unix(nowUnix.Load(), 0) }, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if observation.SourceRefresh == nil {
					return
				}
				refreshMu.Lock()
				defer refreshMu.Unlock()
				refreshes = append(refreshes, *observation.SourceRefresh)
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("open production bundle: %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(ctx) }()
	control := bundle.dependencies.Control.(*productionPhaseTwoControl)
	repository := control.dependencies.Repository
	initial, err := repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// A second publication is confirmed and published without being activated:
	// the source alone is refreshed, as if the process had stopped in between.
	edit := func(oldValue, newValue string) {
		t.Helper()
		current, getErr := redisClient.Get(ctx, "alarm-config.strategy_1001").Bytes()
		if getErr != nil {
			t.Fatal(getErr)
		}
		changed := bytes.Replace(current, []byte(oldValue), []byte(newValue), 1)
		if bytes.Equal(changed, current) {
			t.Fatalf("edit %q -> %q changed nothing", oldValue, newValue)
		}
		if err := redisClient.Set(ctx, "alarm-config.strategy_1001", changed, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	refreshSource := func() controlplane.SourceRefreshResult {
		t.Helper()
		result, refreshErr := control.dependencies.Reconciler.Refresh(ctx, control.dependencies.Source, control.dependencies.Planner)
		if refreshErr != nil {
			t.Fatalf("source refresh error = %v", refreshErr)
		}
		return result
	}
	edit(`"threshold": 80`, `"threshold": 81`)
	if result := refreshSource(); result.Status != controlplane.SourceRefreshPendingConfirmation {
		t.Fatalf("first changed refresh = %+v, want pending", result)
	}
	stranded := refreshSource()
	if stranded.Status != controlplane.SourceRefreshPublished || stranded.Publication == initial.Current {
		t.Fatalf("confirmed refresh = %+v, want a new publication", stranded)
	}
	if activation, err := repository.LoadActivation(ctx); err != nil || activation.Current != initial.Current {
		t.Fatalf("the source refresh alone must not activate: activation=%+v err=%v", activation, err)
	}
	nowUnix.Add(2)
	// The payload the fleet executes expires, the way a publication that is no
	// longer current stops being renewed.
	catalogPrefix := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog")
	if err := redisClient.Del(ctx, catalogPrefix+":snapshot:"+string(initial.Current.SnapshotRevision)).Err(); err != nil {
		t.Fatal(err)
	}

	// The source changes again before the periodic Refresh sees the stranded
	// publication, and keeps changing on every round after that.
	edit(`"threshold": 81`, `"threshold": 82`)
	result, refreshErr := control.Refresh(ctx)
	if refreshErr != nil || result.Status != phaseTwoControlHealthy || len(result.QueryGroups) != 1 {
		t.Fatalf("Refresh() with a pending candidate = (%+v, %v), want the stranded publication activated and healthy", result, refreshErr)
	}
	activation, err := repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if activation.Current != stranded.Publication || activation.RecordRevision != initial.RecordRevision+1 {
		t.Fatalf("activation = %+v, want the stranded publication %+v at revision %d", activation, stranded.Publication, initial.RecordRevision+1)
	}
	if _, err := repository.LoadPublishedSnapshot(ctx, activation.Current); err != nil {
		t.Fatalf("the activated publication must be readable: %v", err)
	}
	refreshMu.Lock()
	last := refreshes[len(refreshes)-1]
	refreshMu.Unlock()
	if last.Status != observability.SourceRefreshPending || !last.ActivationCaughtUp ||
		last.ActivatedEpoch != stranded.Publication.PublicationEpoch || last.SnapshotRevision != "" || !last.CountsKnown {
		t.Fatalf("the refresh line must say a pending round caught the activation up: %+v", last)
	}

	// Every later round finds another candidate; the activation stays on the
	// latest publication and the rounds stay healthy.
	for round, thresholds := range [][2]string{{"82", "83"}, {"83", "84"}, {"84", "85"}} {
		edit(`"threshold": `+thresholds[0], `"threshold": `+thresholds[1])
		result, refreshErr := control.Refresh(ctx)
		if refreshErr != nil || result.Status != phaseTwoControlHealthy || len(result.QueryGroups) != 1 {
			t.Fatalf("round %d Refresh() = (%+v, %v), want healthy", round+1, result, refreshErr)
		}
		refreshMu.Lock()
		last := refreshes[len(refreshes)-1]
		refreshMu.Unlock()
		if last.Status != observability.SourceRefreshPending || last.ActivationCaughtUp {
			t.Fatalf("round %d must be an ordinary pending round: %+v", round+1, last)
		}
	}
	if activation, err := repository.LoadActivation(ctx); err != nil || activation.Current != stranded.Publication {
		t.Fatalf("activation after further pending rounds = %+v err=%v, want %+v", activation, err, stranded.Publication)
	}
}
