package scheduler

import (
	"context"
	"errors"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"testing"
	"time"
)

func TestRecoveryChannelsLeavePForNormalAndReuseAcrossQueries(t *testing.T) {
	limits := testRecoveryLimits()
	limits.ProcessQueryPermits = 3
	limits.RecoveryQueryPermits = 2
	f, err := NewFlightCoordinatorWithRecovery(limits, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	deadline := time.Now().Add(time.Minute)
	slot := execution.SlotIdentity{QueryGroup: "recovery", EvaluationTime: 100}
	channels, err := f.AcquireRecoveryChannels(ctx, slot, execution.OperationReplay, deadline, 20)
	if err != nil {
		t.Fatal(err)
	}
	defer channels.Release()
	if channels.count != 2 || f.queryPermitSnapshot().Inflight != 0 {
		t.Fatal("R reservation must not acquire P")
	}
	waiting, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		c, e := f.AcquireRecoveryChannels(waiting, execution.SlotIdentity{QueryGroup: "waiting", EvaluationTime: 100}, execution.OperationReplay, deadline, 1)
		if c != nil {
			c.Release()
		}
		done <- e
	}()
	normal, err := f.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: "healthy", EvaluationTime: 100}, execution.OperationNormal, deadline)
	if err != nil {
		t.Fatal(err)
	}
	normal.Release()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for range 4 {
		permit, err := channels.AcquireQueryPermit(ctx, slot, execution.OperationReplay, deadline)
		if err != nil {
			t.Fatal(err)
		}
		if permit.RecoveryPermit() == nil {
			t.Fatal("missing actual recovery authority")
		}
		permit.Release()
		if snapshot := f.queryPermitSnapshot(); snapshot.Inflight != 0 || snapshot.RecoveryInflight != 2 {
			t.Fatalf("query release changed reserved channels: %+v", snapshot)
		}
	}
	channels.Release()
	channels.Release()
	if s := f.queryPermitSnapshot(); s.Inflight != 0 || s.RecoveryInflight != 0 {
		t.Fatal(s)
	}
}

func TestRecoveryChannelCancellationWhileWaitingForPReleasesBoth(t *testing.T) {
	limits := testRecoveryLimits()
	limits.ProcessQueryPermits = 2
	limits.RecoveryQueryPermits = 1
	f, err := NewFlightCoordinatorWithRecovery(limits, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	deadline := time.Now().Add(time.Minute)
	var held []*QueryPermit
	for _, qg := range []execution.QueryGroupIdentity{"a", "b"} {
		p, e := f.AcquireQueryPermit(ctx, execution.SlotIdentity{QueryGroup: qg, EvaluationTime: 100}, execution.OperationNormal, deadline)
		if e != nil {
			t.Fatal(e)
		}
		held = append(held, p)
	}
	slot := execution.SlotIdentity{QueryGroup: "recovery", EvaluationTime: 100}
	channels, err := f.AcquireRecoveryChannels(ctx, slot, execution.OperationRetry, deadline, 1)
	if err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithCancel(ctx)
	cancel()
	if permit, err := channels.AcquireQueryPermit(wait, slot, execution.OperationRetry, deadline); permit != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(permit, err)
	}
	channels.Release()
	for _, p := range held {
		p.Release()
	}
	if s := f.queryPermitSnapshot(); s.Inflight != 0 || s.RecoveryInflight != 0 {
		t.Fatal(s)
	}
}
