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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func observationsAt(observations *[]observability.Observation, stage observability.Stage) []observability.Observation {
	var found []observability.Observation
	for _, observation := range *observations {
		if observation.Stage == stage {
			found = append(found, observation)
		}
	}
	return found
}

// A Slot whose events the broker acknowledged and whose State the ownership
// store then refused is the duplicate a handover exists to avoid: the next
// owner redoes the Slot from Progress and sends those events again. It is
// named once, by the store's word, with how many events it had sent.
func TestASlotWhoseStateIsRefusedAfterItsEventsWereAckedIsNamedOnce(t *testing.T) {
	for _, test := range []struct {
		refusal error
		want    string
	}{
		{ownership.ErrStaleFence, contract.ReasonOwnershipStaleFence},
		{ownership.ErrNotDesired, contract.ReasonOwnershipNotDesired},
		{ownership.ErrContentScopeMoved, contract.ReasonContentScopeMoved},
	} {
		t.Run(test.want, func(t *testing.T) {
			fixture, fenced := newFencedFixture(t, false)
			fenced.refusal = test.refusal
			_, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
			if !errors.Is(err, test.refusal) {
				t.Fatalf("Execute() error = %v, want %v wrapped", err, test.refusal)
			}
			if fixture.ports.eventCount == 0 {
				t.Fatal("the Slot sent no events before its State write; this test needs it to")
			}
			unapplied := observationsAt(fixture.observations, observability.StageOutputUnapplied)
			if len(unapplied) != 1 || string(unapplied[0].ReasonCode) != test.want ||
				unapplied[0].Counts.Events != int64(fixture.ports.eventCount) {
				t.Fatalf("output_unapplied lines = %+v, want one under %s with %d events", unapplied, test.want, fixture.ports.eventCount)
			}
		})
	}
}

// A State write that failed for any other reason is retried by the same
// owner, whose replay finds the events already sent; it is not this.
func TestAStateWriteThatFailsForAnotherReasonIsNotOutputUnapplied(t *testing.T) {
	fixture, fenced := newFencedFixture(t, false)
	fenced.refusal = errors.New("redis: connection refused")
	if _, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal)); err == nil {
		t.Fatal("the injected State failure did not fail the Slot")
	}
	if unapplied := observationsAt(fixture.observations, observability.StageOutputUnapplied); len(unapplied) != 0 {
		t.Fatalf("output_unapplied lines = %+v, want none for a failure that is not an ownership refusal", unapplied)
	}
}
