package main

import (
	"context"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/redisfailure"
)

// The cost refresh names every Redis call it makes - its own projection
// published, the registry paged, the others' projections loaded - as
// cost_projection, so the diagnostics client's failures say when it was the
// cost view that lost a read. Nothing answers on the address: the calls
// fail, and a failed call is the one whose name matters.
func TestTheCostRefreshNamesItsRedisCalls(t *testing.T) {
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })
	hook := &redistest.CallerHook{}
	client.AddHook(hook)
	registry, err := ownership.NewRedisStoreWithClient(client, "test:ob:registry")
	if err != nil {
		t.Fatal(err)
	}
	limits := fleet.CostProjectionLimits{PublishBytes: 16 << 10, ReadBytes: 64 << 10, ReadCommands: 4, Timeout: time.Second, FreshFor: time.Minute, TTL: 2 * time.Minute}
	projection, err := fleet.NewCostProjectionStore(client, "test:ob:cost", limits)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	r := &observationCostRefresh{store: projection, registry: registry, reader: client, cache: fleet.NewCostCandidatesCache(func() time.Time { return at }, time.Minute),
		limits: limits, registryLimits: ownership.ObservationRegistryLimits{Bytes: 8 << 10, Commands: 4, Rows: 2, Timeout: time.Second}, replica: "a", interval: time.Second}
	r.publish(context.Background(), at, observability.CostSnapshot{Enabled: true, ProcessID: "process", Scope: "process_observed_candidates", GeneratedAt: at})
	callers := hook.Callers()
	if len(callers) < 2 {
		t.Fatalf("calls = %v, want at least the publish and the registry read", callers)
	}
	for _, caller := range callers {
		if caller != redisfailure.CallerCostProjection {
			t.Fatalf("a cost refresh call named itself %q: %v", caller, callers)
		}
	}
}
