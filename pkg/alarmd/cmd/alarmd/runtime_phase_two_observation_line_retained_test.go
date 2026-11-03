package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Through the production assembly: a Slot's retained bytes reach the census
// through the bundle's observer, the cost source reads it on the heartbeat,
// and the observation line's retained budget, as its metric reads it, is
// that peak and a quarter more once the one owned Query Group has it.
func TestTheProductionLineReservesTheRetainedPoolByItsMeasuredPeak(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-retained-reservation-wiring"
	recorder := metric.NewRecorder(metric.BuildInfo{})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
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
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	retained := func() float64 {
		t.Helper()
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() != "alarmd_observation_memory_budget_size_bytes" {
				continue
			}
			for _, m := range family.GetMetric() {
				for _, label := range m.GetLabel() {
					if label.GetName() == "budget" && label.GetValue() == "retained" {
						return m.GetGauge().GetValue()
					}
				}
			}
		}
		t.Fatal("no retained budget series")
		return 0
	}
	ceiling := float64(cfg.PhaseTwo.Coordinator.MaxRetainedBytes)
	if got := retained(); got != ceiling {
		t.Fatalf("before any reading the retained budget is %v, want the ceiling %v", got, ceiling)
	}
	const queryGroup = "qg-retained"
	now := time.Now().Unix()
	bundle.dependencies.Observer.Observe(observability.ContextWithTraceFields(ctx, observability.TraceFields{QueryGroupKey: queryGroup}),
		observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
			Result: observability.ResultSuccess, Duration: time.Millisecond, DurationKnown: true, FollowingSlot: now + 60,
			Trace:           observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: now},
			SlotBudgetUsage: &observability.SlotBudgetUsageFacts{RetainedBytes: 4000}})
	bundle.workerCosts.boundByOwned(func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{queryGroup} })
	bundle.workerCosts.Costs()
	if got := retained(); got != 5000 {
		t.Fatalf("with the one owned Query Group's peak at 4000 the retained budget is %v, want 5000 at once from the unmeasured ceiling", got)
	}
}
