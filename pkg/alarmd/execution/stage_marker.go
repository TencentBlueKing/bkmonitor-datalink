// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"context"
	"errors"
	"sync/atomic"
	"time"
)

// The stages a running Slot can be found in, closed: where the Runner and the
// coordinator have got to, so that an execution found past its deadline
// names where it is stuck (design 02 section 6.5). A stage is entered, not
// completed; the last one entered is where an execution that never returns
// stopped. Output, state and progress are the commit boundary: an execution
// that has entered any of them is never declined (StageMarker.Decline).
const (
	SlotStageSource   = "source"
	SlotStageExecute  = "execute"
	SlotStageQuery    = "query"
	SlotStageEvaluate = "evaluate"
	SlotStageOutput   = "output"
	SlotStageState    = "state"
	SlotStageProgress = "progress"
)

// SlotStages is every stage, in the order a Slot passes them.
var SlotStages = []string{
	SlotStageSource, SlotStageExecute, SlotStageQuery, SlotStageEvaluate, SlotStageOutput, SlotStageState, SlotStageProgress,
}

// ErrExecutionDeclined is the commit refused to an execution the watchdog
// already let go as hung: its Query Group's lease is being released, so it
// writes nothing - no output a next holder would send again, no State without
// the output it follows.
var ErrExecutionDeclined = errors.New("alarmd execution: declined as hung before it began to commit")

// The commit word of a StageMarker: the execution's generation, so that a
// claim on one execution never lands on the next, and whether it has begun to
// commit or been declined. The two moves out of open are exclusive: whichever
// lands first decides.
const (
	commitOpen uint64 = iota
	commitEntered
	commitDeclined
	commitStateBits = 2
	commitStateMask = 1<<commitStateBits - 1
)

// StageMarker holds, for the execution in flight on one Runner, the stage it
// last entered, the deadline its work runs under, and whether it has begun to
// commit. Read from the watchdog's goroutine while the execution runs, so all
// atomic.
//
// The deadline is the execution's own, not its Slot's: the frozen Slot's
// query deadline is its first attempt's, and a replay, retry or probe runs
// minutes after it under the deadline access derives from its arrival. So
// the Runner sets it only for a normal execution, and access raises it as
// each physical query starts (ExtendDeadline). Zero is no deadline yet, and
// an execution without one is not judged.
type StageMarker struct {
	stage    atomic.Value
	deadline atomic.Int64
	commit   atomic.Uint64
}

// Begin starts a new execution in stage with no deadline yet, open to commit.
func (marker *StageMarker) Begin(stage string) {
	if marker == nil {
		return
	}
	marker.deadline.Store(0)
	marker.stage.Store(stage)
	for {
		word := marker.commit.Load()
		next := (word>>commitStateBits + 1) << commitStateBits
		if marker.commit.CompareAndSwap(word, next) {
			return
		}
	}
}

// Clear says no execution is in flight: nothing for the watchdog to judge.
func (marker *StageMarker) Clear() { marker.Begin("") }

// Enter records stage as the one the execution is in now.
func (marker *StageMarker) Enter(stage string) {
	if marker != nil {
		marker.stage.Store(stage)
	}
}

// Extend raises the execution's deadline to deadline, counted from at when it
// is already past there: work started under a deadline that has gone runs
// until its client notices, and that is measured from when it started, not
// from a deadline that passed before it existed. Never lowered: one of
// several queries ending early does not cut the others' time.
func (marker *StageMarker) Extend(deadline, at time.Time) {
	if marker == nil || deadline.IsZero() {
		return
	}
	if deadline.Before(at) {
		deadline = at
	}
	millis := deadline.UnixMilli()
	for {
		current := marker.deadline.Load()
		if current >= millis || marker.deadline.CompareAndSwap(current, millis) {
			return
		}
	}
}

// Deadline is the execution's deadline so far, zero for none.
func (marker *StageMarker) Deadline() time.Time {
	if marker == nil {
		return time.Time{}
	}
	if millis := marker.deadline.Load(); millis > 0 {
		return time.UnixMilli(millis)
	}
	return time.Time{}
}

// Stage is the stage last entered, empty when none is in flight.
func (marker *StageMarker) Stage() string {
	if marker == nil {
		return ""
	}
	stage, _ := marker.stage.Load().(string)
	return stage
}

// Decline claims the execution in flight as hung when it has a deadline, now
// is past it by more than grace, and it has not begun to commit. Once claimed
// the execution cannot commit (EnterCommit). It returns the stage it is stuck
// in and the deadline it overran.
//
// An execution inside its commit boundary is never claimed, however late:
// its output is written or being written, and the State and Progress writes
// after it run under their own bounded timeouts. Letting it go between them
// would leave acknowledged output unapplied, for the next holder to send again.
func (marker *StageMarker) Decline(now time.Time, grace time.Duration) (string, time.Time, bool) {
	if marker == nil {
		return "", time.Time{}, false
	}
	word := marker.commit.Load()
	deadline := marker.Deadline()
	stage := marker.Stage()
	if word&commitStateMask != commitOpen || deadline.IsZero() || !now.After(deadline.Add(grace)) {
		return stage, deadline, false
	}
	if !marker.commit.CompareAndSwap(word, word&^commitStateMask|commitDeclined) {
		return stage, deadline, false
	}
	return stage, deadline, true
}

// EnterCommit enters stage, one of the commit boundary's, and reports whether
// the execution may write: true unless the watchdog declined it first.
func (marker *StageMarker) EnterCommit(stage string) bool {
	if marker == nil {
		return true
	}
	for {
		word := marker.commit.Load()
		switch word & commitStateMask {
		case commitDeclined:
			return false
		case commitEntered:
			marker.stage.Store(stage)
			return true
		}
		if marker.commit.CompareAndSwap(word, word&^commitStateMask|commitEntered) {
			marker.stage.Store(stage)
			return true
		}
	}
}

type stageMarkerKey struct{}

// WithStageMarker carries marker to the code a Slot runs, for MarkStage,
// ExtendDeadline and EnterCommit.
func WithStageMarker(ctx context.Context, marker *StageMarker) context.Context {
	if marker == nil {
		return ctx
	}
	return context.WithValue(ctx, stageMarkerKey{}, marker)
}

func stageMarkerOf(ctx context.Context) *StageMarker {
	marker, _ := ctx.Value(stageMarkerKey{}).(*StageMarker)
	return marker
}

// MarkStage records that the Slot running under ctx has entered stage; a
// context without a marker records nothing.
func MarkStage(ctx context.Context, stage string) {
	stageMarkerOf(ctx).Enter(stage)
}

// ExtendDeadline raises the deadline of the execution running under ctx to
// that of a query it starts at at (StageMarker.Extend).
func ExtendDeadline(ctx context.Context, deadline, at time.Time) {
	stageMarkerOf(ctx).Extend(deadline, at)
}

// EnterCommit is called before the execution running under ctx writes
// anything of its commit - output, State or Progress - and returns
// ErrExecutionDeclined when the watchdog let it go first. A context without
// a marker always may.
func EnterCommit(ctx context.Context, stage string) error {
	if !stageMarkerOf(ctx).EnterCommit(stage) {
		return ErrExecutionDeclined
	}
	return nil
}
