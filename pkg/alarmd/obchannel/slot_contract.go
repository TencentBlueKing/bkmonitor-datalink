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
}

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
)
