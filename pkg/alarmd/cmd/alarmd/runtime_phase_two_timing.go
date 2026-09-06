package main

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// These durations nest: RunOne includes preparation and execution. They are not
// additive CPU time and do not measure time queued before RunOne is called.
func startSlotTiming(ctx context.Context, observer observability.Observer, stage observability.Stage, now func() time.Time) func() {
	if observer == nil {
		return func() {}
	}
	started := now()
	return func() {
		observeRuntime(ctx, observer, observability.Observation{
			Component: observability.ComponentScheduler, Stage: stage,
			Result: observability.ResultTerminal, Direction: observability.DirectionInternal,
			Duration: now().Sub(started),
		})
	}
}
