// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"context"
	"errors"
	"testing"
	"time"
)

const markerGrace = time.Minute

// An execution with no deadline of its own is never judged, however late the
// clock: a recovery has none until access derives it, and a deadline that is
// not the execution's - its frozen Slot's first attempt's - is no reason.
func TestAnExecutionWithoutItsOwnDeadlineIsNeverDeclined(t *testing.T) {
	var marker StageMarker
	marker.Begin(SlotStageExecute)
	if _, _, declined := marker.Decline(time.Now().Add(24*time.Hour), markerGrace); declined {
		t.Fatal("an execution with no deadline yet was declined")
	}
}

// A replay started minutes after its frozen Slot's deadline runs under the
// deadline access derived from its arrival. Past that one by the grace it is
// declined, and not a moment before.
func TestAnExecutionIsJudgedByTheDeadlineItRunsUnder(t *testing.T) {
	arrival := time.UnixMilli(1_000_000)
	own := arrival.Add(55 * time.Second)
	var marker StageMarker
	marker.Begin(SlotStageExecute)
	marker.Extend(own, arrival)
	if _, _, declined := marker.Decline(own.Add(markerGrace), markerGrace); declined {
		t.Fatal("declined at its own deadline plus exactly the grace, want only past it")
	}
	stage, deadline, declined := marker.Decline(own.Add(markerGrace+time.Millisecond), markerGrace)
	if !declined || !deadline.Equal(own) || stage != SlotStageExecute {
		t.Fatalf("Decline past its own deadline = (%q, %v, %t), want (execute, %v, true)", stage, deadline, declined, own)
	}
}

// A deadline already gone when the work starts counts from the start: the
// work runs until its client notices, which is now, and an execution is not
// hung for having been handed a deadline that passed before it existed.
func TestADeadlineAlreadyPastCountsFromWhenTheWorkStarted(t *testing.T) {
	started := time.UnixMilli(5_000_000)
	var marker StageMarker
	marker.Begin(SlotStageExecute)
	marker.Extend(started.Add(-5*time.Minute), started)
	if got := marker.Deadline(); !got.Equal(started) {
		t.Fatalf("deadline = %v, want the start %v", got, started)
	}
	if _, _, declined := marker.Decline(started.Add(markerGrace-time.Second), markerGrace); declined {
		t.Fatal("declined inside the grace from its start")
	}
}

// Each query raises the deadline to its own; none lowers it. A second query
// started later is judged by its deadline, not by the first one's that has
// passed, and a query ending early does not cut the others' time.
func TestEachQueryRaisesTheDeadlineAndNoneLowersIt(t *testing.T) {
	base := time.UnixMilli(9_000_000)
	var marker StageMarker
	marker.Begin(SlotStageQuery)
	marker.Extend(base.Add(30*time.Second), base)
	marker.Extend(base.Add(10*time.Second), base)
	if got := marker.Deadline(); !got.Equal(base.Add(30 * time.Second)) {
		t.Fatalf("deadline after an earlier one = %v, want the later %v kept", got, base.Add(30*time.Second))
	}
	second := base.Add(2 * time.Minute)
	marker.Extend(base.Add(30*time.Second), second)
	if got := marker.Deadline(); !got.Equal(second) {
		t.Fatalf("deadline after a second query started at %v under a passed deadline = %v, want its start", second, got)
	}
	if _, _, declined := marker.Decline(second.Add(markerGrace-time.Second), markerGrace); declined {
		t.Fatal("the second query was judged by the first one's deadline")
	}
}

// Once an execution has begun to commit it is never declined: its output is
// written or being written, and releasing its lease before the State write
// would leave that output unapplied for the next holder to send again.
func TestAnExecutionInsideItsCommitIsNeverDeclined(t *testing.T) {
	for _, stage := range []string{SlotStageOutput, SlotStageState, SlotStageProgress} {
		var marker StageMarker
		marker.Begin(SlotStageExecute)
		marker.Extend(time.UnixMilli(1_000), time.UnixMilli(1_000))
		ctx := WithStageMarker(context.Background(), &marker)
		if err := EnterCommit(ctx, stage); err != nil {
			t.Fatalf("%s: EnterCommit = %v, want admitted", stage, err)
		}
		if got, _, declined := marker.Decline(time.UnixMilli(1_000).Add(time.Hour), markerGrace); declined || got != stage {
			t.Fatalf("%s: Decline an hour past the deadline = (%q, %t), want refused in %s", stage, got, declined, stage)
		}
		// Later commit stages of the same execution still write.
		if err := EnterCommit(ctx, SlotStageProgress); err != nil {
			t.Fatalf("%s: a later commit stage = %v, want admitted", stage, err)
		}
	}
}

// The other way round: once declined, the execution writes nothing of its
// commit, so a slow execution the watchdog let go cannot land output after
// its lease is released.
func TestADeclinedExecutionCannotCommit(t *testing.T) {
	var marker StageMarker
	marker.Begin(SlotStageEvaluate)
	marker.Extend(time.UnixMilli(1_000), time.UnixMilli(1_000))
	if _, _, declined := marker.Decline(time.UnixMilli(1_000).Add(2*markerGrace), markerGrace); !declined {
		t.Fatal("an evaluation past its deadline and grace was not declined")
	}
	ctx := WithStageMarker(context.Background(), &marker)
	for _, stage := range []string{SlotStageOutput, SlotStageState, SlotStageProgress} {
		if err := EnterCommit(ctx, stage); !errors.Is(err, ErrExecutionDeclined) {
			t.Fatalf("EnterCommit(%s) after the decline = %v, want ErrExecutionDeclined", stage, err)
		}
	}
	if _, _, declined := marker.Decline(time.UnixMilli(1_000).Add(3*markerGrace), markerGrace); declined {
		t.Fatal("an execution was declined twice")
	}
}

// A claim belongs to one execution. The next execution on the same Runner
// starts open and without a deadline, and a cleared marker - no execution in
// flight - is not judged at all.
func TestAClaimOnOneExecutionNeverReachesTheNext(t *testing.T) {
	var marker StageMarker
	marker.Begin(SlotStageQuery)
	marker.Extend(time.UnixMilli(1_000), time.UnixMilli(1_000))
	if _, _, declined := marker.Decline(time.UnixMilli(1_000).Add(2*markerGrace), markerGrace); !declined {
		t.Fatal("setup: the first execution was not declined")
	}
	marker.Clear()
	if _, _, declined := marker.Decline(time.UnixMilli(1_000).Add(time.Hour), markerGrace); declined {
		t.Fatal("a cleared marker was declined")
	}
	marker.Begin(SlotStageSource)
	if !marker.Deadline().IsZero() {
		t.Fatalf("the next execution starts with deadline %v, want none", marker.Deadline())
	}
	if err := EnterCommit(WithStageMarker(context.Background(), &marker), SlotStageOutput); err != nil {
		t.Fatalf("the next execution's commit = %v, want admitted", err)
	}
}

// Code that runs with no marker - a supplement, a test - always commits and
// marks nothing.
func TestWithoutAMarkerEverythingIsAdmitted(t *testing.T) {
	ctx := context.Background()
	MarkStage(ctx, SlotStageQuery)
	ExtendDeadline(ctx, time.Now(), time.Now())
	if err := EnterCommit(ctx, SlotStageOutput); err != nil {
		t.Fatalf("EnterCommit without a marker = %v", err)
	}
}
