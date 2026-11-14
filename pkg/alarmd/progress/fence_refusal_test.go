// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.
package progress

import (
	"context"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Each of the four fenced Progress writes says which refusal the owner fence
// gave, answered the way the store answers: the status with its ownership
// error beside it. Only a write the server never answered is an IO failure.
// The two refusals were read as REDIS_UNAVAILABLE where the error was judged
// before the status (the range writes) or the status had no branch (a moved
// scope at begin and commit).
func TestAProgressWriteTheFenceRefusesSaysWhy(t *testing.T) {
	ctx := context.Background()
	identity := execution.ProgressIdentity{QueryGroup: "q"}
	fence := execution.OwnerFence{QueryGroup: "q", OwnerID: "worker", OwnerEpoch: 1, LeaseToken: "lease"}
	writes := map[string]func(t *testing.T, refuse func(*controlFake)) (execution.ProgressCommitStatus, execution.ReasonCode){
		"BeginSlot": func(t *testing.T, refuse func(*controlFake)) (execution.ProgressCommitStatus, execution.ReasonCode) {
			fake := &controlFake{missing: true}
			refuse(fake)
			result, err := mustStore(t, fake).BeginSlot(ctx, execution.ProgressBeginRequest{
				Identity: identity, OwnerFence: fence, Projection: progressProjection(), ContentScope: "scope-a"})
			if err != nil {
				t.Fatal(err)
			}
			return result.Status, result.ReasonCode
		},
		"CommitProgress": func(t *testing.T, refuse func(*controlFake)) (execution.ProgressCommitStatus, execution.ReasonCode) {
			fake := &controlFake{missing: true}
			refuse(fake)
			result, err := mustStore(t, fake).CommitProgress(ctx, execution.ProgressCommitRequest{
				Identity: identity, OwnerFence: fence, ExpectedNextSlot: 60, ContentScope: "scope-a",
				Completion: execution.SlotCompletion{Contract: progressContract(), Kind: execution.CompletionFull,
					Primary: &execution.PrimaryInputFact{Completeness: execution.CompletenessFull, DataState: execution.DataStateData}, Result: observability.ResultSuccess},
			})
			if err != nil {
				t.Fatal(err)
			}
			return result.Status, result.ReasonCode
		},
		"BeginRange": func(t *testing.T, refuse func(*controlFake)) (execution.ProgressCommitStatus, execution.ReasonCode) {
			fake := &controlFake{value: mustEncode(t, execution.ScheduleProgress{Identity: identity, NextSlot: 60})}
			refuse(fake)
			result, err := mustStore(t, fake).BeginRange(ctx, execution.ExpiredRangeRequest{Projection: rangeFixture(t), OwnerFence: fence})
			if err != nil {
				t.Fatal(err)
			}
			return result.Status, result.ReasonCode
		},
		"CommitRange": func(t *testing.T, refuse func(*controlFake)) (execution.ProgressCommitStatus, execution.ReasonCode) {
			fake := &controlFake{value: mustEncode(t, execution.ScheduleProgress{Identity: identity, NextSlot: 60})}
			store := mustStore(t, fake)
			request := execution.ExpiredRangeRequest{Projection: rangeFixture(t), OwnerFence: fence}
			if begun, err := store.BeginRange(ctx, request); err != nil || begun.Status != execution.ProgressCommitted {
				t.Fatalf("BeginRange = (%+v, %v)", begun, err)
			}
			refuse(fake)
			result, err := store.CommitRange(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			return result.Status, result.ReasonCode
		},
	}
	for _, answer := range []struct {
		name       string
		refuse     func(*controlFake)
		want       execution.ProgressCommitStatus
		wantReason execution.ReasonCode
	}{
		{"stale owner", func(fake *controlFake) { fake.status = ownership.FencedCASStaleOwner }, execution.ProgressStaleOwner, ""},
		{"moved scope", func(fake *controlFake) { fake.status = ownership.FencedCASContentMoved }, execution.ProgressContentMoved, ""},
		{"no answer", func(fake *controlFake) { fake.casErr = errors.New("read tcp: i/o timeout") }, execution.ProgressRetryableIO,
			execution.ReasonCode(contract.ReasonRedisUnavailable)},
	} {
		for name, write := range writes {
			status, reason := write(t, answer.refuse)
			if status != answer.want || (answer.wantReason != "" && reason != answer.wantReason) {
				t.Errorf("%s answered %s: status %s reason %q, want %s %q", name, answer.name, status, reason, answer.want, answer.wantReason)
			}
		}
	}
}
