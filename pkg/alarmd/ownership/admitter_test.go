// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func TestAdmitterRequiresCurrentFenceAndCurrentPlanActivation(t *testing.T) {
	request := execution.SideEffectAdmissionRequest{
		Contract: execution.FrozenExecutionContractRef{
			Slot:                 execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: 100},
			SnapshotRevision:     "snapshot-1",
			QueryRevision:        "query-1",
			ScheduleRevision:     "schedule-1",
			ScheduleSegmentStart: 100,
			DuePlanSetDigest:     "plans-1",
		},
		Plan:            execution.PlanKey{PlanIdentity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: "strategy"}},
		StateApplyEpoch: 1,
		OwnerFence:      execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"},
	}
	fence := &fakeFenceChecker{}
	activation := &fakeActivationReader{active: true}
	admitter, err := NewAdmitter(fence, activation)
	if err != nil {
		t.Fatalf("NewAdmitter() error = %v", err)
	}
	result, err := admitter.Check(context.Background(), request)
	if err != nil || !result.Admitted {
		t.Fatalf("Check(active) = (%+v, %v)", result, err)
	}

	activation.active = false
	result, err = admitter.Check(context.Background(), request)
	if err != nil || result.Admitted || result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("Check(inactive) = (%+v, %v)", result, err)
	}

	fence.err = ErrStaleFence
	activation.active = true
	if _, err := admitter.Check(context.Background(), request); !errors.Is(err, ErrStaleFence) {
		t.Fatalf("Check(stale fence) error = %v, want ErrStaleFence", err)
	}
}

// CheckActivation answers the activation half and never reads the fence: a
// fence that would refuse does not refuse it, and the activation decides.
// Its caller's next write runs the fence in its own script.
func TestCheckActivationReadsTheActivationAndNotTheFence(t *testing.T) {
	request := execution.SideEffectAdmissionRequest{
		Contract: execution.FrozenExecutionContractRef{
			Slot:                 execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: 100},
			SnapshotRevision:     "snapshot-1",
			QueryRevision:        "query-1",
			ScheduleRevision:     "schedule-1",
			ScheduleSegmentStart: 100,
			DuePlanSetDigest:     "plans-1",
		},
		Plan:            execution.PlanKey{PlanIdentity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "business", StrategyID: "strategy"}},
		StateApplyEpoch: 1,
		OwnerFence:      execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"},
	}
	fence := &fakeFenceChecker{err: ErrStaleFence}
	activation := &fakeActivationReader{active: true}
	admitter, err := NewAdmitter(fence, activation)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := admitter.CheckActivation(context.Background(), request); err != nil || !result.Admitted {
		t.Fatalf("CheckActivation(active, stale fence) = (%+v, %v), want admitted", result, err)
	}
	activation.active = false
	result, err := admitter.CheckActivation(context.Background(), request)
	if err != nil || result.Admitted || result.ReasonCode != execution.ReasonCode(contract.ReasonConfigDrift) {
		t.Fatalf("CheckActivation(inactive) = (%+v, %v), want refused for %s", result, err, contract.ReasonConfigDrift)
	}
	if fence.calls != 0 {
		t.Fatalf("CheckActivation read the fence %d times", fence.calls)
	}
	incomplete := request
	incomplete.StateApplyEpoch = 0
	if _, err := admitter.CheckActivation(context.Background(), incomplete); err == nil {
		t.Fatal("CheckActivation took an incomplete Plan identity")
	}
}

type fakeFenceChecker struct {
	err   error
	calls int
}

func (checker *fakeFenceChecker) CheckFence(context.Context, execution.OwnerFence) error {
	checker.calls++
	return checker.err
}

type fakeActivationReader struct {
	active bool
	err    error
}

func (reader *fakeActivationReader) IsPlanActive(
	context.Context,
	execution.FrozenExecutionContractRef,
	execution.PlanKey,
	execution.StateApplyEpoch,
) (bool, error) {
	return reader.active, reader.err
}
