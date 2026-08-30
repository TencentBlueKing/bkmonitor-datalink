// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestQueryFreeProgressRequiresExactCompletionReason(t *testing.T) {
	for _, test := range []struct {
		name   string
		kind   execution.CompletionKind
		reason execution.ReasonCode
		valid  bool
	}{
		{name: "snapshot exact", kind: execution.CompletionSnapshotUnavailable, reason: execution.ReasonCode(contract.ReasonSnapshotUnavailable), valid: true},
		{name: "gap exact", kind: execution.CompletionGapSkipped, reason: execution.ReasonCode(contract.ReasonGapSkipped), valid: true},
		{name: "snapshot with gap reason", kind: execution.CompletionSnapshotUnavailable, reason: execution.ReasonCode(contract.ReasonGapSkipped)},
		{name: "gap with snapshot reason", kind: execution.CompletionGapSkipped, reason: execution.ReasonCode(contract.ReasonSnapshotUnavailable)},
		{name: "generic coverage reason", kind: execution.CompletionSnapshotUnavailable, reason: execution.ReasonCode(contract.ReasonQueryUnavailable)},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := queryFreeProgressRequest(test.kind, test.reason).Validate()
			if (err == nil) != test.valid {
				t.Fatalf("Validate() error=%v, valid=%t", err, test.valid)
			}
		})
	}
}

func queryFreeProgressRequest(kind execution.CompletionKind, reason execution.ReasonCode) execution.ProgressCommitRequest {
	contractRef := execution.FrozenExecutionContractRef{
		Slot: execution.SlotIdentity{
			QueryGroup: "query-group", ScheduleRevision: "schedule-v1", EvaluationTime: 1_788_000_000,
		},
		SnapshotRevision: "snapshot-v1", QueryRevision: "query-v1",
		ScheduleRevision: "schedule-v1", DuePlanSetDigest: "due-plans-v1",
	}
	return execution.ProgressCommitRequest{
		Namespace: execution.ProgressNamespace{QueryGroup: "query-group", ScheduleRevision: "schedule-v1"},
		OwnerFence: execution.OwnerFence{
			QueryGroup: "query-group", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "lease-1",
		},
		ExpectedNextSlot: contractRef.Slot.EvaluationTime,
		Completion: execution.SlotCompletion{
			Contract: contractRef, Kind: kind, Result: observability.ResultDegraded, ReasonCode: reason,
		},
	}
}
