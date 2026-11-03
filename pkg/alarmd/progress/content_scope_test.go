// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package progress

import (
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// decision-016 batch 2: the content scope a Progress write declares goes to
// the fenced write as declared -- begin, commit, and the expired range --
// and an undeclared one stays undeclared. The store adds nothing of its own.
func TestProgressWritesDeclareTheContentScopeTheyWereGiven(t *testing.T) {
	fake := &controlFake{missing: true}
	store := mustStore(t, fake)
	projection := progressProjection()
	projection.ContentScope = "qg-object-a"
	begin := execution.ProgressBeginRequest{
		Identity:   execution.ProgressIdentity{QueryGroup: "q"},
		OwnerFence: execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"},
		Projection: projection, ContentScope: "qg-object-a",
	}
	if result, err := store.BeginSlot(context.Background(), begin); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot() = (%+v, %v)", result, err)
	}
	commit := execution.ProgressCommitRequest{
		Identity: begin.Identity, OwnerFence: begin.OwnerFence, ExpectedNextSlot: 60, Projection: projection, ContentScope: "qg-object-a",
		Completion: execution.SlotCompletion{Contract: progressContract(), Kind: execution.CompletionFull,
			Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess},
	}
	if result, err := store.CommitProgress(context.Background(), commit); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("CommitProgress() = (%+v, %v)", result, err)
	}
	if want := []string{"qg-object-a", "qg-object-a"}; !reflect.DeepEqual(fake.scopes, want) {
		t.Fatalf("fenced writes declared %v, want %v: begin and commit, each with the Slot's scope", fake.scopes, want)
	}

	// The persisted projection keeps the scope for a retry to declare, and
	// a projection that lacks it is still the same unfinished Slot.
	undeclared := progressProjection()
	fake = &controlFake{missing: true}
	store = mustStore(t, fake)
	begin.Projection, begin.ContentScope = undeclared, ""
	if result, err := store.BeginSlot(context.Background(), begin); err != nil || result.Status != execution.ProgressCommitted {
		t.Fatalf("BeginSlot(undeclared) = (%+v, %v)", result, err)
	}
	loaded, err := store.LoadProgress(context.Background(), begin.Identity)
	if err != nil || loaded.Progress == nil || loaded.Progress.UnfinishedSlot == nil || loaded.Progress.UnfinishedSlot.ContentScope != "" {
		t.Fatalf("LoadProgress(undeclared) = (%+v, %v), want an unfinished Slot with no scope", loaded, err)
	}
	if !reflect.DeepEqual(fake.scopes, []string{""}) {
		t.Fatalf("an undeclared begin declared %v", fake.scopes)
	}
}
