package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

func TestSlotTimingSourceAllReturnsAndObserverIsolation(t *testing.T) {
	wantErr := errors.New("source failure")
	for _, due := range []bool{false, true} {
		for _, err := range []error{nil, wantErr} {
			for _, panicObserver := range []bool{false, true} {
				called, timed := false, 0
				source := observedProductionSlotSource{next: slotSourceFunc(func(context.Context, execution.QueryGroupIdentity) (scheduler.FrozenSlot, bool, error) {
					called = true
					return scheduler.FrozenSlot{}, due, err
				}), observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
					if o.Stage == observability.StageSlotSourceCompleted {
						if !called || o.Duration < 0 {
							t.Fatal("timing precedes source return")
						}
						timed++
					}
					if panicObserver {
						panic("observer failure")
					}
				})}
				_, gotDue, gotErr := source.Next(context.Background(), "qg")
				if gotDue != due || gotErr != err || timed != 1 {
					t.Fatalf("due=%v err=%v timed=%d", gotDue, gotErr, timed)
				}
			}
		}
	}
}

func TestSlotTimingClockAndDisabled(t *testing.T) {
	clockCalls := 0
	now := func() time.Time { clockCalls++; return time.Unix(int64(clockCalls), 0) }
	finish := startSlotTiming(context.Background(), nil, observability.StageRunnerCompleted, now)
	finish()
	if clockCalls != 0 {
		t.Fatal("disabled timer reads clock")
	}
	var got observability.Observation
	finish = startSlotTiming(context.Background(), observability.ObserverFunc(func(_ context.Context, o observability.Observation) { got = o }), observability.StageRunnerCompleted, now)
	if clockCalls != 1 {
		t.Fatal("timer not started before call")
	}
	finish()
	if clockCalls != 2 || got.Duration != time.Second || got.Stage != observability.StageRunnerCompleted {
		t.Fatalf("unexpected timer: %+v", got)
	}
}

func TestSlotTimingDispatcherPreservesRunOneReturns(t *testing.T) {
	for _, attempted := range []bool{false, true} {
		for _, wantErr := range []error{nil, errors.New("runner failure")} {
			for _, panicObserver := range []bool{false, true} {
				observed := make(chan observability.Observation, 1)
				called := false
				lifecycle := &phaseTwoQueryGroupLifecycle{runner: &callbackPhaseTwoQueryGroup{run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
					called = true
					return execution.SlotExecutionResult{}, attempted, wantErr
				}}}
				bundle := &phaseTwoWorkerBundle{dependencies: phaseTwoWorkerBundleDependencies{Config: validGoAccessRuntimeConfig(), Observer: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
					if !called {
						t.Error("observation before RunOne returned")
					}
					observed <- o
					if panicObserver {
						panic("observer failure")
					}
				})}, runners: map[execution.QueryGroupIdentity]*phaseTwoQueryGroupLifecycle{"qg": lifecycle}}
				dispatcher := newPhaseTwoRunnerDispatcher(bundle, false)
				dispatcher.start(context.Background())
				dispatcher.jobs <- phaseTwoScheduledRunner{queryGroup: "qg", lifecycle: lifecycle}
				select {
				case result := <-dispatcher.results:
					if result.attempted != attempted || result.err != wantErr || result.admissionDenied {
						t.Fatalf("changed RunOne return: %+v", result)
					}
				case <-time.After(time.Second):
					t.Fatal("RunOne result blocked by timing")
				}
				dispatcher.stop()
				o := <-observed
				if o.Stage != observability.StageRunnerCompleted || o.Duration < 0 {
					t.Fatalf("unexpected observation: %+v", o)
				}
			}
		}
	}
}

func BenchmarkSlotTiming(b *testing.B) {
	for name, observer := range map[string]observability.Observer{"disabled": nil, "noop": observability.NopObserver{}} {
		b.Run(name, func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				startSlotTiming(ctx, observer, observability.StageRunnerCompleted, time.Now)()
			}
		})
	}
}
