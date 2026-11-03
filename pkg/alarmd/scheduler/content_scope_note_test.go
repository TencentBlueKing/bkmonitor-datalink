// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The Runner tells the session the content scope of the Slot it is about to
// execute, before it executes it: the renewal that follows says what the
// holder runs, and a holder already on a pending change's content is then not
// capped at the change and does not hand itself over.
func TestTheRunnerNotesTheScopeOfTheSlotItRunsBeforeRunningIt(t *testing.T) {
	clock := newMutableClock(time.Unix(200, 0))
	slot := frozenSlot("query-group-1")
	slot.Dispatch.ContentScope = "view-b"
	source := &fakeSlotSource{slot: slot}
	session := &fakeSession{fence: slot.Dispatch.OwnerFence}
	var notedAtExecution []string
	executor := &callbackExecutor{onExecute: func() { notedAtExecution = append([]string(nil), session.noted...) }}
	flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewRunner("query-group-1", session, source, executor, flights, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notedAtExecution) != 1 || notedAtExecution[0] != "view-b" {
		t.Fatalf("scopes noted when the Slot executed = %q, want view-b noted before it ran", notedAtExecution)
	}
}

// callbackExecutor completes every Slot and runs onExecute as it starts.
type callbackExecutor struct{ onExecute func() }

func (executor *callbackExecutor) Execute(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	if executor.onExecute != nil {
		executor.onExecute()
	}
	return execution.SlotExecutionResult{Completed: true}, nil
}
