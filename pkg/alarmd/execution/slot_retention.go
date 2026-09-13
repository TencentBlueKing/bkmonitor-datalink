// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"errors"
	"time"
)

// How long a Slot is still read or executed after its evaluation time is one
// decision, and two sides act on it: the scheduler, which decides whether a
// Slot is live, replayable or expired, and the Control Leader, which decides
// whether a closed Schedule Segment still has a Slot anyone will read. Both
// call the functions below. A second implementation of the same arithmetic
// would let the two drift apart on some branch while every test stays green,
// and the drift here would be a Segment dropped under a Slot that is still
// going to run - which nothing can recover.

var (
	ErrSlotRetentionInvalid = errors.New("alarmd execution: slot retention limits are invalid")
	// ErrSlotDeadlineDrift reports a Slot whose Plans yield no valid query
	// deadline: no Plan is aligned at it, or a completion deadline does not
	// leave room for the query reserve.
	ErrSlotDeadlineDrift = errors.New("alarmd execution: slot has no valid query deadline")
)

// SlotRetention holds the three durations that bound a Slot's lifetime
// beyond its query deadline.
type SlotRetention struct {
	// QueryReserve is taken off every Plan's completion deadline to leave
	// room for the downstream query.
	QueryReserve time.Duration
	// MaxReplayAge is how long after the query deadline a missed Slot may
	// still be replayed.
	MaxReplayAge time.Duration
	// TerminalDelay is how long after the replay window a Slot's facts are
	// still read before they are terminal.
	TerminalDelay time.Duration
}

func (retention SlotRetention) Validate() error {
	if retention.QueryReserve <= 0 || retention.MaxReplayAge <= 0 || retention.TerminalDelay <= 0 {
		return ErrSlotRetentionInvalid
	}
	return nil
}

// SlotQueryDeadlineUnixMilli is the earliest completion deadline among the
// Plans aligned at slot, less the query reserve.
func SlotQueryDeadlineUnixMilli(schedule FrozenQueryGroupSchedule, slot EvaluationTime, queryReserve time.Duration) (int64, error) {
	if queryReserve <= 0 {
		return 0, ErrSlotRetentionInvalid
	}
	deadline := int64(0)
	for _, plan := range schedule.Plans {
		if !plan.Spec.IsAligned(slot) {
			continue
		}
		completion, valid := plan.Spec.CompletionDeadlineUnixMilli(slot)
		if !valid {
			return 0, ErrSlotDeadlineDrift
		}
		candidate := completion - queryReserve.Milliseconds()
		if candidate <= int64(slot)*1000 {
			return 0, ErrSlotDeadlineDrift
		}
		if deadline == 0 || candidate < deadline {
			deadline = candidate
		}
	}
	if deadline == 0 {
		return 0, ErrSlotDeadlineDrift
	}
	return deadline, nil
}

// SlotRecoveryBoundaries derives from a query deadline the end of the replay
// window and the instant after which nothing about the Slot is read.
func SlotRecoveryBoundaries(deadline int64, maxReplayAge, terminalDelay time.Duration) (recoveryUntil, keepUntil int64, err error) {
	if maxReplayAge <= 0 || terminalDelay <= 0 {
		return 0, 0, ErrSlotRetentionInvalid
	}
	recoveryUntil = time.UnixMilli(deadline).Add(maxReplayAge).UnixMilli()
	keepUntil = time.UnixMilli(recoveryUntil).Add(terminalDelay).UnixMilli()
	if recoveryUntil <= deadline || keepUntil <= recoveryUntil {
		return 0, 0, ErrSlotRetentionInvalid
	}
	return recoveryUntil, keepUntil, nil
}

// SegmentKeepUntilUnixMilli is the instant after which no Slot of a closed
// Segment is read or executed anymore: the largest keepUntil over every Slot
// the Segment contains, each derived exactly as the scheduler derives it. An
// open Segment has no such instant.
func SegmentKeepUntilUnixMilli(schedule FrozenQueryGroupSchedule, retention SlotRetention) (int64, error) {
	if err := retention.Validate(); err != nil {
		return 0, err
	}
	if schedule.Segment.End == nil {
		return 0, errors.New("alarmd execution: an open Segment has no keep-until instant")
	}
	keepUntil := int64(0)
	slot, ok := schedule.FirstSlot()
	for ok {
		deadline, err := SlotQueryDeadlineUnixMilli(schedule, slot, retention.QueryReserve)
		if err != nil {
			return 0, err
		}
		_, slotKeepUntil, err := SlotRecoveryBoundaries(deadline, retention.MaxReplayAge, retention.TerminalDelay)
		if err != nil {
			return 0, err
		}
		if slotKeepUntil > keepUntil {
			keepUntil = slotKeepUntil
		}
		slot, ok = schedule.NextSlotAfter(slot)
	}
	return keepUntil, nil
}
