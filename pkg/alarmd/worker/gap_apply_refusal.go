// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// GapApplyRefusal preserves the gap marker store's refusal through the Slot's
// error wrappers, so the completion line can name which of the three happened.
//
// It carries the Plan and the revision this Slot expected because the question
// these refusals raise is always the same one: the marker moved between the
// read and the write, so who else wrote it. A line that says only CONFLICT
// sends a reader to look for a second writer with nothing to identify it by;
// with the Plan and the expected revision, two lines from two Slots can be laid
// beside each other.
type GapApplyRefusal struct {
	Stage string
	// Site is which of the Slot's two gap apply calls refused:
	// GapSiteBeforeEvents or GapSiteAfterState. Both write the same Plan-level
	// key, so a refusal that does not say which one it came from leaves the
	// most likely second writer -- the Slot's own other call -- indistinguishable
	// from a writer in another process.
	Site             string
	Status           execution.GapGuardApplyStatus
	Plan             execution.PlanIdentity
	StateGeneration  execution.StateGeneration
	ExpectedRevision uint64
}

// The two gap apply sites of one Slot, both writing the same Plan-level key.
const (
	GapSiteBeforeEvents = "before_events"
	GapSiteAfterState   = "after_state"
)

func (err *GapApplyRefusal) Error() string {
	site := err.Site
	if site == "" {
		site = "unlocated"
	}
	return fmt.Sprintf("%s: %s (site %s, strategy %s, generation %s, expected marker revision %d)",
		err.Stage, err.Status, site, err.Plan.StrategyID, err.StateGeneration, err.ExpectedRevision)
}

// ReasonCode is the bounded name this refusal reports as, empty for a status
// that is not a refusal this build names.
func (err *GapApplyRefusal) ReasonCode() execution.ReasonCode {
	switch err.Status {
	case execution.GapGuardConflict:
		return execution.ReasonCode(contract.ReasonGapApplyConflict)
	case execution.GapGuardStale:
		return execution.ReasonCode(contract.ReasonGapApplyStaleVersion)
	case execution.GapGuardRetryable:
		return execution.ReasonCode(contract.ReasonGapWriteRetryable)
	}
	return ""
}

// withExpectedMarkerRevision fills in the revision this Slot proposed against,
// which lives on the mutation rather than on the store's answer.
//
// A no-op for any other error, and for a refusal whose Plan is not in this
// chunk - the revision is evidence, and evidence attached to the wrong Plan is
// worse than none.
func withExpectedMarkerRevision(err error, mutations []execution.PlanGapMutation) error {
	var refusal *GapApplyRefusal
	if !errors.As(err, &refusal) || refusal == nil {
		return err
	}
	for _, mutation := range mutations {
		if mutation.Identity.Plan == refusal.Plan && mutation.Identity.StateGeneration == refusal.StateGeneration {
			refusal.ExpectedRevision = mutation.ExpectedMarkerRevision
			break
		}
	}
	return err
}

// GapApplySiteOf returns which of the Slot's two gap applies refused, empty
// when err is not one of the store's refusals.
//
// Separate from GapApplyReason because the two answer different questions and
// a line carries both: the reason says what the store did, the site says which
// of this Slot's two writes it did it to.
func GapApplySiteOf(err error) string {
	var refusal *GapApplyRefusal
	if !errors.As(err, &refusal) || refusal == nil {
		return ""
	}
	return refusal.Site
}

// GapApplyReason returns the bounded reason when err is one of the gap marker
// store's refusals, so an observer can name it instead of calling it unknown.
//
// A status this build does not name returns false rather than a guess: the
// point of the three names is that they separate three situations, and a
// fourth status folded into one of them would make the separation wrong
// exactly where a new status most needs to be noticed.
func GapApplyReason(err error) (execution.ReasonCode, bool) {
	var refusal *GapApplyRefusal
	if !errors.As(err, &refusal) || refusal == nil {
		return "", false
	}
	reason := refusal.ReasonCode()
	if reason == "" {
		return "", false
	}
	return reason, true
}
