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

// StateConflictError preserves a state version refusal through the Slot's
// error wrappers. It names the observation without changing retry behavior.
type StateConflictError struct {
	Stage  string
	Status string
	// RepeatedKey: the conflicting item was the later copy of a key the same
	// request had already written -- the producer made two different
	// statements for one series. The line has to say so, because from the
	// status alone this is indistinguishable from a race with another writer.
	RepeatedKey bool
	// Kind is which comparison refused a STATE_VERSION_CONFLICT, with the two
	// revisions it compared (StoredRevision is zero for a key not found) and
	// how the stored ApplyVersion ordered against the mutation's. Empty on a
	// STALE_VERSION, which has one way of being reached. When a chunk refuses
	// more than one item these are the first item's; the counts by kind
	// travel on the observation.
	Kind              execution.StateVersionConflictKind
	ExpectedRevision  uint64
	StoredRevision    uint64
	VersionComparison execution.ApplyVersionComparison
}

func (err *StateConflictError) Error() string {
	text := fmt.Sprintf("%s: %s", err.Stage, err.Status)
	if err.Kind != "" {
		text += fmt.Sprintf(" (%s: expected revision %d, stored revision %d", err.Kind, err.ExpectedRevision, err.StoredRevision)
		if err.VersionComparison != "" {
			text += fmt.Sprintf(", stored version %s", err.VersionComparison)
		}
		text += ")"
	}
	if err.RepeatedKey {
		text += " (repeated key in the same request)"
	}
	return text
}

// StateConflictReason recognizes only the two version refusals. Other state
// errors retain their existing classification rather than being guessed from text.
func StateConflictReason(err error) (execution.ReasonCode, bool) {
	var conflict *StateConflictError
	if !errors.As(err, &conflict) || conflict == nil {
		return "", false
	}
	switch conflict.Status {
	case string(execution.StateVersionConflict):
		return execution.ReasonCode(contract.ReasonStateVersionConflict), true
	case string(execution.StateStaleVersion):
		return execution.ReasonCode(contract.ReasonStateStaleVersion), true
	default:
		return "", false
	}
}
