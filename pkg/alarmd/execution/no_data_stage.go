// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import (
	"context"
	"errors"
)

// NoDataStaged is what of one Plan's whole memory a Slot attempt has written
// into its staging key ahead of the statement: the marker naming the
// write, the groups written and their memory folded, and the present-as-of
// they were written against. A first write's present groups are staged as
// the windows land them, a segment at a time, so the worker holds one segment
// rather than the whole delta; the statement then carries only the groups
// left (PlanNoDataMutation.Staged).
//
// The fold is the store's own, of the values it encoded, carried here by the
// worker from one segment to the next and not read back from the server at
// the swap: it is compared there with the statement's MemoryDigest, which
// the writer folded apart from its judgements, so a group staged twice, or
// one either path left out, is refused rather than put in place. That the
// server holds those values is proved by each segment's marker check and the
// swap's HLEN, not by the fold.
type NoDataStaged struct {
	Marker      []byte
	PresentAsOf int64
	memory      NoDataMemoryDigestAccumulator
}

// Groups is how many groups have been staged.
func (staged *NoDataStaged) Groups() uint64 {
	if staged == nil {
		return 0
	}
	return staged.memory.groups
}

// Fold is the staged groups' memory, to fold the rest into.
func (staged *NoDataStaged) Fold() NoDataMemoryDigestAccumulator {
	if staged == nil {
		return NoDataMemoryDigestAccumulator{}
	}
	return staged.memory
}

// NewNoDataStaged is a staging write's state after a segment: the marker it
// proves and the fold so far. Only the store makes one.
func NewNoDataStaged(marker []byte, presentAsOf int64, memory NoDataMemoryDigestAccumulator) *NoDataStaged {
	return &NoDataStaged{Marker: append([]byte(nil), marker...), PresentAsOf: presentAsOf, memory: memory}
}

// NoDataStageRequest stages one segment of a Plan's whole memory under the
// attempt's staging key: Set, a segment's groups at most
// (NoDataFirstWriteSegmentGroups), each once, written against PresentAsOf;
// Staged what this attempt staged of the Plan before, nil for the first
// segment, which starts the key over.
type NoDataStageRequest struct {
	Contract    FrozenExecutionContractRef
	Identity    PlanNoDataIdentity
	Attempt     NoDataWriteAttempt
	PresentAsOf int64
	Set         []NoDataGroupDelta
	Staged      *NoDataStaged
}

// NoDataStageResult is a staged segment: APPLIED with the staging state to
// pass on, RETRYABLE_IO when the server was unreachable or the staging key
// was lost between two segments, DETERMINISTIC_INVALID for a segment that
// cannot be staged.
type NoDataStageResult struct {
	Status     NoDataApplyStatus
	ReasonCode ReasonCode
	Staged     *NoDataStaged
}

// NoDataStageDiscard takes back an attempt's staging key for a Plan whose
// round ends without its statement - not judged, or refused before the swap:
// deleted when it still holds this write's marker, left otherwise.
type NoDataStageDiscard struct {
	Identity PlanNoDataIdentity
	Attempt  NoDataWriteAttempt
	Staged   *NoDataStaged
}

// PlanNoDataStager stages a first write's groups ahead of its statement. A
// store without it is written whole at the statement, as before staging.
type PlanNoDataStager interface {
	StageNoData(context.Context, NoDataStageRequest) (NoDataStageResult, error)
	DiscardNoDataStage(context.Context, NoDataStageDiscard) error
}

// ValidateNoDataStageSegment checks a segment before it is staged: within
// one segment, every group keyed, in canonical order and storable against
// the present-as-of.
func ValidateNoDataStageSegment(request NoDataStageRequest) error {
	if len(request.Set) == 0 || len(request.Set) > NoDataFirstWriteSegmentGroups {
		return errors.New("alarmd execution: a no-data stage segment holds none or more than a segment of groups")
	}
	if request.PresentAsOf < 0 {
		return errors.New("alarmd execution: no-data present-as-of must not be negative")
	}
	if request.Staged != nil && request.Staged.PresentAsOf != request.PresentAsOf {
		return errors.New("alarmd execution: a no-data stage segment is written against another present-as-of than the staged ones")
	}
	previous := ""
	for index, group := range request.Set {
		if group.GroupKey == "" || index > 0 && group.GroupKey <= previous {
			return errors.New("alarmd execution: no-data stage segment groups are not keyed in canonical order")
		}
		previous = group.GroupKey
		if group.Absent == nil && request.PresentAsOf == 0 {
			return errors.New("alarmd execution: a Plan that has never had data cannot store a group as present")
		}
	}
	return nil
}

// NoDataGroupMemoryOfValue is the memory a stored value is of, against the
// record's present-as-of: the inverse of how the value is written (a group
// seen at the present-as-of and not absent is written as present).
func NoDataGroupMemoryOfValue(value NoDataGroupDelta, presentAsOf int64) NoDataGroupMemory {
	if value.Absent == nil {
		return NoDataGroupMemory{GroupKey: value.GroupKey, LastSeen: presentAsOf}
	}
	return NoDataGroupMemory{GroupKey: value.GroupKey, LastSeen: value.Absent.LastSeen,
		FirstAbsent: value.Absent.FirstAbsent, SuppressedAt: value.Absent.SuppressedAt}
}
