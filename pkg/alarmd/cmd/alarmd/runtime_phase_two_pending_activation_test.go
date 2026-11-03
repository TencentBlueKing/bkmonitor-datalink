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

// A publication an earlier round published but never activated (the
// process stopped between the two) is superseded by the next round that
// reads the source: every round that reads a change publishes it and
// activates it, so a source that changes on every round moves the
// activation on every round. It used to be different: a change waited for a
// second identical read, a source that changed on every round never gave
// one, and only a special branch could catch the activation up to the
// stranded publication while its payload expired. Here the payload of the
// publication the fleet runs expires too, and the round that follows
// activates the change it read.
func TestProductionPhaseTwoRefreshActivatesEveryChangeWhileTheSourceKeepsChanging(t *testing.T) {
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
		writer.Header().Set("Content-Type", "application/json")
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
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
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
	initial, err := repository.LoadActivationHead(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// A second publication is published without being activated: the source
	// alone is refreshed, as if the process had stopped in between.
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
	edit(`"threshold": 80`, `"threshold": 81`)
	stranded, refreshErr := control.dependencies.Reconciler.Refresh(ctx, control.dependencies.Source, control.dependencies.Planner)
	if refreshErr != nil || stranded.Status != controlplane.SourceRefreshPublished || stranded.Publication == initial.Current {
		t.Fatalf("source refresh of a change = (%+v, %v), want it published by the round that read it", stranded, refreshErr)
	}
	if activation, err := repository.LoadActivationHead(ctx); err != nil || activation.Current != initial.Current {
		t.Fatalf("the source refresh alone must not activate: activation=%+v err=%v", activation, err)
	}
	nowUnix.Add(2)
	// The payload the fleet executes expires, the way a publication that is no
	// longer current stops being renewed.
	catalogPrefix := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "catalog")
	if err := redisClient.Del(ctx, catalogPrefix+":manifest:"+string(initial.Current.SnapshotRevision)).Err(); err != nil {
		t.Fatal(err)
	}

	// The source changes again before the periodic Refresh runs, and keeps
	// changing on every round after that. Each round publishes what it read
	// and the activation follows it.
	previousEpoch := stranded.Publication.PublicationEpoch
	for round, thresholds := range [][2]string{{"81", "82"}, {"82", "83"}, {"83", "84"}, {"84", "85"}} {
		edit(`"threshold": `+thresholds[0], `"threshold": `+thresholds[1])
		result, refreshErr := control.Refresh(ctx)
		if refreshErr != nil || result.Status != phaseTwoControlHealthy || len(result.QueryGroups) != 1 {
			t.Fatalf("round %d Refresh() = (%+v, %v), want healthy", round+1, result, refreshErr)
		}
		refreshMu.Lock()
		last := refreshes[len(refreshes)-1]
		refreshMu.Unlock()
		if last.Status != observability.SourceRefreshPublished || last.PublicationEpoch <= previousEpoch || !last.CountsKnown {
			t.Fatalf("round %d refresh line = %+v, want a new publication after epoch %d", round+1, last, previousEpoch)
		}
		previousEpoch = last.PublicationEpoch
		activation, err := repository.LoadActivationHead(ctx)
		if err != nil || activation.Current.PublicationEpoch != last.PublicationEpoch ||
			string(activation.Current.SnapshotRevision) != last.SnapshotRevision {
			t.Fatalf("round %d activation = %+v err=%v, want the publication the round made (%+v)", round+1, activation, err, last)
		}
		if _, err := loadPublishedSnapshot(ctx, repository, activation.Current); err != nil {
			t.Fatalf("round %d: the activated publication must be readable: %v", round+1, err)
		}
	}
}
