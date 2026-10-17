// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

// 大组领域测试不伪造网络性能；只测批推进正确性、CAS/读取次数和并发安全的成员端口。
type scaleLinkOperator struct {
	RelationOperator
	template store.StoredAlert
	mu       sync.Mutex
	calls    map[string]int
}

func (o *scaleLinkOperator) LinkMergeMemberPrepared(_ context.Context, tenant, id string, r domain.MergeRelation) (store.StoredAlert, error) {
	o.mu.Lock()
	o.calls[id]++
	o.mu.Unlock()
	a := o.template.Alert.Clone()
	a.BKTenantID = tenant
	a.AlertID = id
	a.Fingerprint = "fp-" + id
	a.Merge = &domain.AlertMerge{Role: "original", State: "merged", RelationIDs: []string{r.ID}}
	return store.StoredAlert{Alert: a, Version: o.template.Version}, nil
}

func (o *scaleLinkOperator) LinkMergeMember(ctx context.Context, tenant, id, relation string) (store.StoredAlert, error) {
	return store.StoredAlert{}, fmt.Errorf("large batch did not reuse fixed relation")
}

type scaleLinkDocuments struct {
	Documents
	mu            sync.Mutex
	reads, writes map[string]int
}

func (d *scaleLinkDocuments) Get(ctx context.Context, kind, id string) (json.RawMessage, string, error) {
	d.mu.Lock()
	d.reads[kind]++
	d.mu.Unlock()
	return d.Documents.Get(ctx, kind, id)
}

func (d *scaleLinkDocuments) Put(ctx context.Context, kind, id, expected string, raw json.RawMessage) error {
	if err := d.Documents.Put(ctx, kind, id, expected, raw); err != nil {
		return err
	}
	if expected != "" {
		d.mu.Lock()
		d.writes[kind]++
		d.mu.Unlock()
	}
	return nil
}

func scaleLinkFixture(tb testing.TB, count int) (*Journal, StoredDecision, store.StoredAlert, *scaleLinkOperator, *scaleLinkDocuments) {
	tb.Helper()
	compiled, err := policy.Compile(policy.Merge, json.RawMessage(`{"name":"merge","is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[{"period":"everyday","open_clock_time":"00:00:00","close_clock_time":"23:59:59"}],"policy":[{"expression":"A","A":{"condition":"term","target_key":"level","target_value":"warning"}}],"merge_cycle":60,"is_cycle_merge":false,"aggregate_fields":[],"new_alarm_config":[{"key":"name","value":"joint ${alarm_num}"},{"key":"level","value":"warning"}]}`))
	if err != nil {
		tb.Fatal(err)
	}
	at := time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)
	window := strings.Repeat("b", 64)
	id, err := domain.MergeDecisionID("tenant", window)
	if err != nil {
		tb.Fatal(err)
	}
	d := Decision{ID: id, TenantID: "tenant", WindowID: window, GroupKey: strings.Repeat("c", 64), Policy: policy.Release{Scope: policy.Scope{TenantID: "tenant", Kind: policy.Merge}, ID: "merge", Version: 1, Spec: compiled.Canonical, Compiled: compiled.Summary}, FrozenAt: at.Add(time.Second), StartedAt: at, Deadline: at.Add(time.Minute), Outcome: "succeeded"}
	for i := range count {
		member := fmt.Sprintf("member-%05d", i)
		d.MemberIDs = append(d.MemberIDs, member)
		d.WaitMemberIDs = append(d.WaitMemberIDs, member)
	}
	e := storetest.Event(d.TenantID, "placeholder", "placeholder", "warning")
	e.EventSourceID = domain.BuiltinMergeEventSourceID
	e.Fingerprint = ParentFingerprint(d.TenantID, d.Policy.ID, d.MemberIDs)
	e.SourceEventID = d.ID
	e.SourceAlertID = e.Fingerprint
	e.OccurredAt = d.FrozenAt
	e.ProducedAt = d.FrozenAt
	e.ReceivedAt = d.FrozenAt
	e.CreateAt = d.FrozenAt
	e.EventID, err = domain.GenerateEventID(d.TenantID, e.EventSourceID, e.SourceEventID, d.FrozenAt)
	if err != nil {
		tb.Fatal(err)
	}
	e.MergeOrigin = &domain.MergeOrigin{OperationID: d.ID, Policy: domain.PolicyVersion{ID: d.Policy.ID, Version: d.Policy.Version, Digest: d.Policy.Compiled.Digest}, WindowID: d.WindowID, MembersDigest: MembersDigest(d.MemberIDs), MemberCount: len(d.MemberIDs)}
	e, err = e.Normalize()
	if err != nil {
		tb.Fatal(err)
	}
	d.Progress = Progress{Phase: "linking", ParentEvent: &e, ParentAlertID: "large-parent", CaptureOffset: count, UpdatedAt: d.FrozenAt}
	d, err = d.Normalize()
	if err != nil {
		tb.Fatal(err)
	}
	repo := memory.New()
	parent := storetest.Alert(d.TenantID, "large-parent", e.EventID, e.Fingerprint, "warning")
	parent.EventSourceID = domain.BuiltinMergeEventSourceID
	parent.Merge = &domain.AlertMerge{Role: "aggregate", State: "merged", OperationID: d.ID, RelationIDs: []string{d.ID}}
	savedParent, err := repo.CreateAlert(context.Background(), parent)
	if err != nil {
		tb.Fatal(err)
	}
	template, err := repo.CreateAlert(context.Background(), storetest.Alert(d.TenantID, "template", "template-event", "template-fp", "warning"))
	if err != nil {
		tb.Fatal(err)
	}
	r := domain.MergeRelation{ID: d.ID, TenantID: d.TenantID, WindowID: d.WindowID, GroupKey: d.GroupKey, Policy: e.MergeOrigin.Policy, ParentAlertID: savedParent.Alert.AlertID, ParentFingerprint: e.Fingerprint, WaitMemberIDs: slices.Clone(d.WaitMemberIDs), State: "preparing", IndexOffset: count + 1, CreatedAt: d.FrozenAt, UpdatedAt: d.FrozenAt}
	for _, member := range d.MemberIDs {
		r.Members = append(r.Members, domain.MergeRelationMember{AlertID: member, State: "pending"})
	}
	if err := r.Validate(); err != nil {
		tb.Fatal(err)
	}
	docs := &memoryDocuments{data: map[string]json.RawMessage{}, versions: map[string]int{}}
	key, err := decisionKey(d.TenantID, d.ID)
	if err != nil {
		tb.Fatal(err)
	}
	for kind, value := range map[string]any{"merge_decisions": d, "merge_relations": r} {
		raw, err := json.Marshal(value)
		if err != nil {
			tb.Fatal(err)
		}
		if err := docs.Put(context.Background(), kind, key, "", raw); err != nil {
			tb.Fatal(err)
		}
	}
	j, err := NewJournal(docs)
	if err != nil {
		tb.Fatal(err)
	}
	current, err := j.Get(context.Background(), d.TenantID, d.ID)
	if err != nil {
		tb.Fatal(err)
	}
	counted := &scaleLinkDocuments{Documents: docs, reads: map[string]int{}, writes: map[string]int{}}
	j.docs = counted
	return j, current, savedParent.StoredAlert, &scaleLinkOperator{template: template.StoredAlert, calls: map[string]int{}}, counted
}

func runScaleLinkBatch(tb testing.TB, j *Journal, current StoredDecision, parent store.StoredAlert, ops *scaleLinkOperator, batchSize, concurrency int) StoredDecision {
	tb.Helper()
	ctx := context.Background()
	for current.Decision.Progress.MemberOffset < len(current.Decision.WaitMemberIDs) {
		next, err := j.LinkMembersBatch(ctx, current, parent, ops, current.Decision.FrozenAt.Add(time.Second), batchSize, concurrency)
		if err != nil {
			tb.Fatal(err)
		}
		if next.Decision.Progress.MemberOffset <= current.Decision.Progress.MemberOffset {
			tb.Fatal("large batch made no progress")
		}
		current = next
	}
	return current
}

func TestLinkMembersBatchLargeGroupsKeepOneConfirmationAndOffsetCASPerBatch(t *testing.T) {
	for _, tc := range []struct{ members, size, concurrency int }{{5000, 64, 8}, {10000, 128, 16}} {
		t.Run(fmt.Sprintf("%d/%d/%d", tc.members, tc.size, tc.concurrency), func(t *testing.T) {
			j, current, parent, ops, docs := scaleLinkFixture(t, tc.members)
			done := runScaleLinkBatch(t, j, current, parent, ops, tc.size, tc.concurrency)
			batches := (tc.members + tc.size - 1) / tc.size
			if done.Decision.Progress.MemberOffset != tc.members || docs.writes["merge_relations"] != batches || docs.writes["merge_decisions"] != batches || docs.reads["merge_relations"] != 2*batches || docs.reads["merge_decisions"] != batches {
				t.Fatal("per-member progress network requests remain", done.Decision.Progress.MemberOffset, docs.reads, docs.writes)
			}
			if len(ops.calls) != tc.members {
				t.Fatal("large group omitted members", len(ops.calls))
			}
			for id, calls := range ops.calls {
				if calls != 1 {
					t.Fatal("member repeated without retry", id, calls)
				}
			}
			r, err := j.GetRelation(t.Context(), current.Decision.TenantID, current.Decision.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, member := range r.Relation.Members {
				if member.State != "linked" {
					t.Fatal("large group relation not confirmed", member.AlertID)
				}
			}
			t.Logf("members=%d batch=%d concurrency=%d batches=%d progressCAS=%d relation/decisionReads=%d/%d", tc.members, tc.size, tc.concurrency, batches, 2*batches, 2*batches, batches)
		})
	}
}

func BenchmarkLinkMembersBatchLargeGroups(b *testing.B) {
	for _, members := range []int{5000, 10000} {
		for _, budget := range []struct{ size, concurrency int }{{16, 4}, {64, 8}, {128, 16}} {
			b.Run(fmt.Sprintf("%d/%d/%d", members, budget.size, budget.concurrency), func(b *testing.B) {
				for range b.N {
					b.StopTimer()
					j, current, parent, ops, docs := scaleLinkFixture(b, members)
					b.StartTimer()
					runScaleLinkBatch(b, j, current, parent, ops, budget.size, budget.concurrency)
					b.StopTimer()
					b.ReportMetric(float64(docs.writes["merge_relations"]+docs.writes["merge_decisions"]), "progressCAS/op")
					b.ReportMetric(float64(docs.reads["merge_relations"]+docs.reads["merge_decisions"]), "docReads/op")
					b.ReportMetric(float64(members), "members/op")
				}
			})
		}
	}
}
