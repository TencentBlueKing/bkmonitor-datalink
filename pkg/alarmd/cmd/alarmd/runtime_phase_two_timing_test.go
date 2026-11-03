package main

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The timing record says how long a call took. It must not also claim the call
// failed.
//
// It used to report TERMINAL with no reason on every SlotSource.Next and every
// RunOne, which in this catalog is a failure result carrying no explanation, so
// it normalized to "nobody reported one". On the object page that rendered as a
// Query Group terminating over and over for an unstated cause, once per tick.
// Nothing was wrong with the Query Group: about nine in ten of those calls are
// a dispatcher poll answering "not due yet", which is what a tick normally
// does. The failures on this path already report themselves where they happen.
func TestSlotTimingRecordsDurationWithoutClaimingFailure(t *testing.T) {
	var seen []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		seen = append(seen, observation)
	})
	current := time.Unix(1_700_000_000, 0)
	now := func() time.Time { return current }

	done := startSlotTiming(context.Background(), observer, observability.StageSlotSourceCompleted, now)
	current = current.Add(17 * time.Millisecond)
	done()

	if len(seen) != 1 {
		t.Fatalf("observations=%d, want one", len(seen))
	}
	observation := seen[0]
	if observation.Result == observability.ResultTerminal {
		t.Errorf("result=%q: a duration record must not report a terminated call", observation.Result)
	}
	if observation.Result != observability.ResultSuccess {
		t.Errorf("result=%q, want success", observation.Result)
	}
	// A success carries no reason, so it cannot normalize to reason_not_reported.
	if reason := observability.NormalizeReason(observation.ReasonCode, observation.Result); reason != observability.ReasonNone {
		t.Errorf("reason=%q, want none", reason)
	}
	if observation.Duration != 17*time.Millisecond {
		t.Errorf("duration=%v, want the measured 17ms", observation.Duration)
	}
}
