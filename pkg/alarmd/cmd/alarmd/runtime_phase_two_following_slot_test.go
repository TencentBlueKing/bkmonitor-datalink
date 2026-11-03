// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"net/http"
	"sync"
	"testing"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// The retained-peak census holds a Query Group's last Slot peak for two of
// its own periods, and it reads the period from the Slot after this one on
// the completion. Through the production bundle a scheduled Slot's
// slot_completed line carries that Slot, one period after its own.
func TestAScheduledSlotCompletionCarriesTheSlotAfterIt(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	document := controlledG4StrategyDocument(t, 7403, strategy.DetectorKindThreshold, "usage", "system.cpu_summary",
		[]string{"host"}, [][]map[string]any{{{"method": "gte", "threshold": 50}}})
	if err := client.Set(ctx, "alarm-config.strategy_ids", "[7403]", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "alarm-config.strategy_7403", []byte(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	backend := &poolBackend{}
	base := controlledG4Base(t) + 60
	backend.clock.Store(base * 1000)
	type completion struct{ evaluation, following int64 }
	var mu sync.Mutex
	var completions []completion
	cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-following-slot")
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(poolBudgetSettle)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(poolBudgetReserve)
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}),
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: backend.now, HTTPClient: &http.Client{Transport: controlledRoundTripper(backend.roundTrip)},
			AdditionalObserver: observability.ObserverFunc(func(ctx context.Context, o observability.Observation) {
				if o.Stage != observability.StageSlotCompleted || o.Result != observability.ResultSuccess {
					return
				}
				evaluation := o.Trace.EvaluationTime
				if evaluation == 0 {
					evaluation = observability.TraceFieldsFromContext(ctx).EvaluationTime
				}
				mu.Lock()
				defer mu.Unlock()
				completions = append(completions, completion{evaluation: evaluation, following: o.FollowingSlot})
			}),
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	if len(bundle.queryGroups) != 1 {
		t.Fatalf("Query Groups = %v, want one", bundle.queryGroups)
	}
	backend.clock.Store(base*1000 + firstReadWaitOf(cfg, bundle.queryGroups[0], 60).Milliseconds() + 500)
	if err := runScheduledOnceSettled(ctx, bundle); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(completions) == 0 {
		t.Fatal("no Slot completed")
	}
	for _, c := range completions {
		if c.evaluation <= 0 || c.following != c.evaluation+60 {
			t.Fatalf("completions %+v, want each to carry the Slot one sixty-second period after its own", completions)
		}
	}
}
