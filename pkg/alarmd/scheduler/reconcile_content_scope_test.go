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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// decision-016 batch 2, leader half: a reconcile round brings each record
// to the content its Query Group is published with when the round declares
// scopes, withdraws every scope when it withdraws, and touches no scope when
// it knows nothing about them. A scope decision keeps the desired worker and
// is conditioned on the revision the round read, like a placement.
func TestAReconcileRoundBringsRecordsToTheContentTheyArePublishedWith(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := rebalanceWorker("a", ownership.WorkerReady, now.Add(time.Minute), "shadow")
	record := func(queryGroup execution.QueryGroupIdentity, scope, pending string) ownership.AssignmentRecord {
		return ownership.AssignmentRecord{
			QueryGroup: queryGroup, DesiredWorkerID: "a", AssignmentGeneration: 1, RecordRevision: 4, ControlEpoch: 1,
			PlacementReason: ownership.PlacementRendezvous, AssignedAt: now.Add(-time.Minute),
			ContentScope: scope, PendingContentScope: pending,
		}
	}
	digests := map[execution.QueryGroupIdentity]string{
		"unnamed": "qg-object-1", "current": "qg-object-2", "pending": "qg-object-3", "moved": "qg-object-4b", "unknown-scope": "",
		"stray-pending": "qg-object-7",
	}
	cases := []struct {
		name    string
		policy  ContentScopePolicy
		records map[execution.QueryGroupIdentity]ownership.AssignmentRecord
		want    map[execution.QueryGroupIdentity]ownership.AssignmentDecision
	}{
		{
			name: "declaring names the content once and leaves settled records alone", policy: ContentScopesDeclared,
			records: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
				"unnamed": record("unnamed", "", ""),
				"current": record("current", "qg-object-2", ""),
				"pending": record("pending", "qg-object-old", "qg-object-3"),
				"moved":   record("moved", "qg-object-4a", ""),
				// Content the round does not know is not a withdrawal.
				"unknown-scope":   record("unknown-scope", "qg-object-5", ""),
				"not-in-manifest": record("not-in-manifest", "qg-object-6", ""),
				// On the published content but with a change pending towards
				// other content -- a scope written ahead of a cutover that
				// did not happen: republishing the current content cancels it.
				"stray-pending": record("stray-pending", "qg-object-7", "qg-object-never-activated"),
			},
			want: map[execution.QueryGroupIdentity]ownership.AssignmentDecision{
				"unnamed":       {ContentScope: "qg-object-1"},
				"moved":         {ContentScope: "qg-object-4b"},
				"stray-pending": {ContentScope: "qg-object-7"},
			},
		},
		{
			name: "withdrawing clears every record that names or pends a scope", policy: ContentScopesWithdrawn,
			records: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
				"unnamed": record("unnamed", "", ""),
				"current": record("current", "qg-object-2", ""),
				"pending": record("pending", "", "qg-object-3"),
			},
			want: map[execution.QueryGroupIdentity]ownership.AssignmentDecision{
				"current": {WithdrawContentScope: true},
				"pending": {WithdrawContentScope: true},
			},
		},
		{
			name: "a round that knows nothing about scopes touches none", policy: ContentScopesUntouched,
			records: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{
				"unnamed": record("unnamed", "", ""),
				"current": record("current", "qg-object-2", ""),
			},
			want: map[execution.QueryGroupIdentity]ownership.AssignmentDecision{},
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			decisions := map[execution.QueryGroupIdentity]ownership.AssignmentDecision{}
			store := &countingAssignmentStore{
				workers: []ownership.WorkerRegistration{worker}, records: test.records,
				observeDecision: func(decision ownership.AssignmentDecision) { decisions[decision.QueryGroup] = decision },
			}
			reconciler, err := NewReconciler(NewRouter(nil), store)
			if err != nil {
				t.Fatal(err)
			}
			queryGroups := make([]execution.QueryGroupIdentity, 0, len(test.records))
			for queryGroup := range test.records {
				queryGroups = append(queryGroups, queryGroup)
			}
			_, _, err = reconciler.ReconcileRoundWithScopes(context.Background(), ownership.PublicationAuthority{}, queryGroups,
				[]ownership.WorkerRegistration{worker}, now, ContentScopes{Policy: test.policy, Digests: digests})
			if err != nil {
				t.Fatalf("ReconcileRoundWithScopes() error = %v", err)
			}
			if len(decisions) != len(test.want) {
				t.Fatalf("published decisions = %+v, want exactly %v", decisions, test.want)
			}
			for queryGroup, want := range test.want {
				got, published := decisions[queryGroup]
				if !published {
					t.Fatalf("%s: no decision published, want scope %q withdraw %t", queryGroup, want.ContentScope, want.WithdrawContentScope)
				}
				if got.ContentScope != want.ContentScope || got.WithdrawContentScope != want.WithdrawContentScope {
					t.Fatalf("%s: decision = %+v, want scope %q withdraw %t", queryGroup, got, want.ContentScope, want.WithdrawContentScope)
				}
				// A scope decision is not a move: the desired worker, the
				// placement reason and the revision it is conditioned on
				// are the record's own.
				if got.DesiredWorkerID != "a" || got.PlacementReason != ownership.PlacementRendezvous || got.ExpectedRecordRevision != 4 {
					t.Fatalf("%s: a scope decision changed the placement: %+v", queryGroup, got)
				}
			}
		})
	}
}

// A placement made while the round declares carries the content it places
// onto, so a Query Group is never placed unnamed and named a round later;
// and one made while the round withdraws withdraws, so a move carries no
// stale scope along.
func TestAPlacementCarriesTheRoundsContentDecision(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := rebalanceWorker("a", ownership.WorkerReady, now.Add(time.Minute), "shadow")
	for _, test := range []struct {
		name         string
		policy       ContentScopePolicy
		wantScope    string
		wantWithdraw bool
	}{
		{name: "declared", policy: ContentScopesDeclared, wantScope: "qg-object-1"},
		{name: "withdrawn", policy: ContentScopesWithdrawn, wantWithdraw: true},
		{name: "untouched", policy: ContentScopesUntouched},
	} {
		t.Run(test.name, func(t *testing.T) {
			var decision ownership.AssignmentDecision
			store := &countingAssignmentStore{
				workers: []ownership.WorkerRegistration{worker}, records: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{},
				observeDecision: func(published ownership.AssignmentDecision) { decision = published },
			}
			reconciler, err := NewReconciler(NewRouter(nil), store)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = reconciler.ReconcileRoundWithScopes(context.Background(), ownership.PublicationAuthority{},
				[]execution.QueryGroupIdentity{"fresh"}, []ownership.WorkerRegistration{worker}, now,
				ContentScopes{Policy: test.policy, Digests: map[execution.QueryGroupIdentity]string{"fresh": "qg-object-1"}})
			if err != nil || store.published != 1 {
				t.Fatalf("ReconcileRoundWithScopes() error = %v, published %d", err, store.published)
			}
			if decision.ContentScope != test.wantScope || decision.WithdrawContentScope != test.wantWithdraw {
				t.Fatalf("placement decision = %+v, want scope %q withdraw %t", decision, test.wantScope, test.wantWithdraw)
			}
		})
	}
}
