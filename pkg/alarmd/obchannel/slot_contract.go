// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package obchannel

import (
	"encoding/json"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/access"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// SlotPlan is reconstructed from a retained historical Segment, never from the
// current strategy. Prepared contains query semantics, not execution authority.
type SlotPlan struct {
	Contract     execution.FrozenExecutionContractRef
	ObjectDigest execution.ObjectDigest
	Prepared     access.PreparedExecution
	// ReadHoldBasis is where the contract's read hold was read from: one of
	// ReadHoldFromProgress, ReadHoldFromRecord, ReadHoldNoRecord.
	ReadHoldBasis string
}

// Where a historical Slot's read hold was read from, so a reader tells a
// stored fact from an inference: the Query Group's Progress, which still
// carries the Slot's contract; its read hold record; or no record at all,
// which a group whose hold has only ever been zero keeps -- read as zero for
// a Slot within the record's lifetime, readhold.RecordTTL. A record evicted
// from Redis reads the same as none.
const (
	ReadHoldFromProgress = "progress"
	ReadHoldFromRecord   = "record"
	ReadHoldNoRecord     = "no_record"
)

// SlotEvidence is a bounded read of retained records. Complete describes this
// read, not whether every historical input or attempt was captured.
type SlotEvidence struct {
	Records     []json.RawMessage `json:"records"`
	Samples     []json.RawMessage `json:"samples"`
	Limitations []string          `json:"limitations"`
	Complete    bool              `json:"complete"`
}

var (
	ErrHistoricalContractUnavailable = errors.New("historical Slot contract unavailable")
	ErrSlotBudgetExceeded            = errors.New("Slot evidence read budget exceeded")
	ErrSlotDependencyUnavailable     = errors.New("Slot evidence dependency unavailable")
	// ErrHistoricalReadHoldUnknown is a Slot whose read hold - which its
	// contract carries - is not known: its record did not decode, or no
	// longer reaches back to it, or there is none and the Slot is older than
	// a record's lifetime. Its contract is not rebuilt with another hold,
	// which would be a contract it never ran.
	ErrHistoricalReadHoldUnknown = errors.New("historical Slot read hold unknown")
)
