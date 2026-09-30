// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

func TestStateConflictTerminalObservationKeepsRegisteredReason(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want observability.ReasonCode
	}{
		{"preflight conflict", &worker.StateConflictError{Stage: "state mutation preflight", Status: string(execution.StateVersionConflict)}, contract.ReasonStateVersionConflict},
		{"preflight stale", &worker.StateConflictError{Stage: "state mutation preflight", Status: string(execution.StateStaleVersion)}, contract.ReasonStateStaleVersion},
		{"apply conflict", &worker.StateConflictError{Stage: "state apply did not complete", Status: string(execution.StateApplyVersionConflict)}, contract.ReasonStateVersionConflict},
		{"apply stale", &worker.StateConflictError{Stage: "state apply did not complete", Status: string(execution.StateApplyStale)}, contract.ReasonStateStaleVersion},
		{"unknown", errors.New("unclassified state failure"), observability.ReasonInternalUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			wrapped := fmt.Errorf("alarmd worker: execute frozen Slot: %w", test.err)
			var completed *observability.Observation
			executor := observedProductionSlotExecutor{
				next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
					return execution.SlotExecutionResult{}, wrapped
				}),
				observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					if observation.Stage == observability.StageSlotCompleted {
						normalized := observability.NormalizeObservation(observation)
						completed = &normalized
					}
				}),
			}
			result, err := executor.Execute(context.Background(), gapGuardSlotRequest(execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1001"}))
			if err != wrapped || result.Completed {
				t.Fatalf("execution behavior changed: %+v, %v", result, err)
			}
			if completed == nil || completed.Result != observability.ResultFailed || completed.ReasonCode != test.want {
				t.Fatalf("terminal observation = %+v, want failed/%s", completed, test.want)
			}
		})
	}
	for _, reason := range []string{contract.ReasonStateVersionConflict, contract.ReasonStateStaleVersion} {
		definition, found := contract.LookupReasonV2(reason)
		if !found || definition.Domains != contract.ReasonDomainObservation {
			t.Fatalf("reason %s must be registered for observation only: %+v", reason, definition)
		}
	}
}
