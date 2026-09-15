package main

import (
	"context"
	"net/http"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// This is the real Redis catalog, Source, Runner, Coordinator and Progress path.
// The controlled FULL_EMPTY backend proves eligibility restoration, not the
// throughput of a populated Query. The sibling FULL/ACK/State test covers that.
func TestExpiredRangeV2ProductionRestoresReplayAfterBothPrefixes(t *testing.T) {
	f := newExpiredRangeProductionBundle(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"series":[],"status":null,"trace_id":"prefix-replay","is_partial":false}`))
	}, nil)
	ctx := context.Background()
	f.clock.Store((f.base + 1800) * 1000)
	for _, qg := range f.bundle.queryGroups {
		runner := f.bundle.runners[qg].runner
		var kinds []execution.CompletionKind
		for i := 0; i < 4; i++ {
			result, attempted, err := runner.RunOne(ctx)
			if err != nil || !attempted || !result.Completed {
				t.Fatalf("run %d: %+v attempted=%v err=%v", i, result, attempted, err)
			}
			kinds = append(kinds, result.CompletionKind)
		}
		want := []execution.CompletionKind{execution.CompletionSnapshotUnavailable, execution.CompletionSnapshotUnavailable, execution.CompletionGapSkipped, execution.CompletionFullEmpty}
		for i := range want {
			if kinds[i] != want[i] {
				t.Fatalf("prefix did not restore executable suffix: got %v want %v", kinds, want)
			}
		}
		p := loadPhaseTwoProgress(t, ctx, f.production, qg)
		if p.LastFullSlot == 0 || p.UnfinishedRange != nil || p.UnfinishedSlot != nil {
			t.Fatalf("terminal progress: %+v", p)
		}
	}
	if f.queries.Load() != int64(len(f.bundle.queryGroups)) || len(f.events.snapshot()) != 0 {
		t.Fatalf("query-free prefixes produced work: queries=%d events=%d", f.queries.Load(), len(f.events.snapshot()))
	}
}
