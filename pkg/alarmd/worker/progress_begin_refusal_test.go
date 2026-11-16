// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A BeginSlot the owner fence refuses wrote nothing of the Slot, so the
// attempt comes back as its ownership rejection carrying the fence's word --
// a stale fence or a moved content scope -- for the Runner to count as one,
// and nothing after the begin runs. A conflict is not the fence and keeps its
// retry.
func TestABeginTheFenceRefusesIsTheAttemptsOwnershipRejection(t *testing.T) {
	for _, refusal := range []struct {
		status execution.ProgressCommitStatus
		want   error
	}{
		{execution.ProgressStaleOwner, ownership.ErrStaleFence},
		{execution.ProgressContentMoved, ownership.ErrContentScopeMoved},
	} {
		fixture, fenced := newFencedFixture(t, false)
		fixture.ports.beginStatus = refusal.status
		result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
		var notBegun *execution.SlotBeginRefusedError
		if result.Completed || !errors.As(err, &notBegun) || !errors.Is(err, refusal.want) {
			t.Fatalf("%s at begin: result=%+v err=%v, want the attempt refused carrying %v", refusal.status, result, err, refusal.want)
		}
		if len(fenced.fences) != 0 || !isZeroProgressCommit(fixture.ports.lastProgress) {
			t.Fatalf("%s at begin: the Slot went on past its begin (state applies %d, progress %+v)", refusal.status, len(fenced.fences), fixture.ports.lastProgress)
		}
	}
	fixture, _ := newFencedFixture(t, false)
	fixture.ports.beginStatus = execution.ProgressConflict
	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || result.ReasonCode != execution.ReasonCode(contract.ReasonProgressBeginRejected) {
		t.Fatalf("conflict at begin: result=%+v err=%v, want a retry naming %s", result, err, contract.ReasonProgressBeginRejected)
	}
}
