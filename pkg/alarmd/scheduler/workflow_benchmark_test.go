package scheduler

import (
	"context"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"testing"
	"time"
)

func BenchmarkWorkflowRunnerNoDue(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "no_observer"
		if enabled {
			name = "nop_observer"
		}
		b.Run(name, func(b *testing.B) {
			f := NewFlightCoordinator()
			if enabled {
				f.observer = observability.NopObserver{}
			}
			now := time.Unix(100, 0)
			r, e := NewRunner("query-group-1", &fakeSession{fence: execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "w", OwnerEpoch: 1, LeaseToken: "t"}}, &fakeSlotSource{}, &blockingExecutor{}, f, func() time.Time { return now })
			if e != nil {
				b.Fatal(e)
			}
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _, _ = r.RunOne(ctx)
			}
		})
	}
}
