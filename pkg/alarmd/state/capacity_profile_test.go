package state

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestCapacityProfileLegalGap(t *testing.T) {
	if os.Getenv("ALARMD_CAPACITY_PROFILE") != "1" {
		t.Skip("opt-in capacity measurement")
	}
	// Same default as config.Codec.MaxEncodedBytes; importing config here cycles.
	const maxValue = 512 << 10
	identity := execution.PlanGapIdentity{Plan: stateIdentityV2().Plan, StateGeneration: "generation"}
	envelope := gapEnvelope{Schema: executionGapSchemaV2, Identity: identity, MarkerRevision: 1, ApplyVersion: applyVersion(), MutationDigest: "capacity", ScheduleRevision: "schedule"}
	var raw []byte
	for i := uint32(1); ; i++ {
		envelope.Scopes = append(envelope.Scopes, execution.GapScopeState{Scope: execution.GapScope{LevelID: i, HasLevel: true}, Status: execution.GapStatusGapped, ReasonCode: execution.ReasonCode(contract.ReasonHistoryGapped), RequiredFullSlots: 2})
		candidate, err := json.Marshal(envelope)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidate) > maxValue {
			envelope.Scopes = envelope.Scopes[:len(envelope.Scopes)-1]
			break
		}
		raw = candidate
	}
	backend := &casMemoryBackend{values: map[string][]byte{}}
	key, _ := PlanGapKeyV2("capacity", identity)
	backend.values[key] = raw
	router, _ := NewFixedRouter("capacity", backend)
	store, _ := NewExecutionStore(ExecutionStoreOptions{Prefix: "capacity", Router: router, MaxValueBytes: maxValue, MaxItemsPerCall: 8192, MinTTL: time.Minute, MaxTTL: time.Hour, RestartMargin: time.Minute})
	req := execution.GapLoadRequest{Contract: frozenRef(), Items: []execution.PlanGapLoadItem{{Identity: identity, ApplyVersion: applyVersion(), ScheduleRevision: "schedule"}}}
	runtime.GC()
	var before, after, live runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	peak.Store(before.HeapAlloc)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			case <-time.After(time.Millisecond):
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				for old := peak.Load(); m.HeapAlloc > old; old = peak.Load() {
					if peak.CompareAndSwap(old, m.HeapAlloc) {
						break
					}
				}
			}
		}
	}()
	var snapshot execution.GapGuardSnapshot
	started := time.Now()
	err := store.LoadGapsInto(context.Background(), req, func(s execution.GapGuardSnapshot) error { snapshot = s; return nil })
	elapsed := time.Since(started)
	close(stop)
	<-done
	runtime.ReadMemStats(&after)
	if err != nil || snapshot.Status != execution.GapFound || len(snapshot.Scopes) != len(envelope.Scopes) {
		t.Fatalf("not legal: err=%v status=%s scopes=%d", err, snapshot.Status, len(snapshot.Scopes))
	}
	runtime.GC()
	runtime.ReadMemStats(&live)
	runtime.KeepAlive(snapshot)
	runtime.KeepAlive(backend)
	runtime.KeepAlive(envelope)
	runtime.KeepAlive(raw)
	t.Logf("raw_bytes=%d scopes=%d duration=%s alloc=%d heap_before=%d heap_after=%d sampled_peak=%d heap_after_gc=%d", len(raw), len(snapshot.Scopes), elapsed, after.TotalAlloc-before.TotalAlloc, before.HeapAlloc, after.HeapAlloc, peak.Load(), live.HeapAlloc)
}
