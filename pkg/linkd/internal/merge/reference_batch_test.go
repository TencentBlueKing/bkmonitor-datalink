// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

type referenceBatchDocuments struct {
	*memoryDocuments
	creates, reads, relationWrites, singleRefs, maxBatch int
	partialCreate                                        bool
	failCreateCall                                       int
	afterCreate                                          func()
}

func (d *referenceBatchDocuments) Put(ctx context.Context, kind, id, expected string, raw json.RawMessage) error {
	if kind == "merge_relation_refs" {
		d.singleRefs++
	}
	if kind == "merge_relations" {
		d.relationWrites++
	}
	return d.memoryDocuments.Put(ctx, kind, id, expected, raw)
}

func (d *referenceBatchDocuments) Get(ctx context.Context, kind, id string) (json.RawMessage, string, error) {
	if kind == "merge_relation_refs" {
		d.singleRefs++
	}
	return d.memoryDocuments.Get(ctx, kind, id)
}

func (d *referenceBatchDocuments) PutReferences(ctx context.Context, rows map[string]json.RawMessage) error {
	d.creates++
	d.maxBatch = max(d.maxBatch, len(rows))
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for i, key := range keys {
		if d.partialCreate && (d.failCreateCall == 0 || d.creates == d.failCreateCall) && i == 2 {
			return errors.New("partial reference write")
		}
		if err := d.memoryDocuments.PutReferences(ctx, map[string]json.RawMessage{key: rows[key]}); err != nil {
			return err
		}
	}
	if d.afterCreate != nil {
		d.afterCreate()
	}
	return nil
}

func referenceBatchFixture(t *testing.T, members int) (*Journal, *referenceBatchDocuments, StoredRelation) {
	t.Helper()
	j, docs := newJournal(t)
	d := decisionFixture(t)
	r := domain.MergeRelation{ID: d.ID, TenantID: d.TenantID, WindowID: d.WindowID, GroupKey: d.GroupKey, Policy: domain.PolicyVersion{ID: d.Policy.ID, Version: d.Policy.Version, Digest: d.Policy.Compiled.Digest}, ParentAlertID: "parent", ParentFingerprint: "fp-parent", State: "preparing", CreatedAt: d.FrozenAt, UpdatedAt: d.FrozenAt}
	for i := range members {
		id := fmt.Sprintf("member-%03d", i)
		r.Members = append(r.Members, domain.MergeRelationMember{AlertID: id, State: "pending"})
		r.WaitMemberIDs = append(r.WaitMemberIDs, id)
	}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	key, err := decisionKey(r.TenantID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := docs.Put(t.Context(), "merge_relations", key, "", raw); err != nil {
		t.Fatal(err)
	}
	current, err := j.GetRelation(t.Context(), r.TenantID, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	batch := &referenceBatchDocuments{memoryDocuments: docs}
	j.docs = batch
	return j, batch, current
}

func TestIndexRelationBatchUsesBoundedReferenceIOAndOneProgressCAS(t *testing.T) {
	j, docs, current := referenceBatchFixture(t, 256)
	next, err := j.IndexRelationBatch(t.Context(), current, current.Relation.UpdatedAt, 257)
	if err != nil || next.Relation.IndexOffset != 257 || docs.creates != 3 || docs.reads != 0 || docs.maxBatch != 128 || docs.singleRefs != 0 || docs.relationWrites != 1 || next.Version != "2" {
		t.Fatal("references still use per-member I/O or progress CAS", next.Relation.IndexOffset, docs.creates, docs.reads, docs.maxBatch, docs.singleRefs, docs.relationWrites, err)
	}
	for offset := range 257 {
		alert, role := next.Relation.ParentAlertID, "parent"
		if offset > 0 {
			alert, role = next.Relation.Members[offset-1].AlertID, "member"
		}
		scope, err := referenceScope(next.Relation.TenantID, alert)
		if err != nil {
			t.Fatal(err)
		}
		raw, _, err := docs.memoryDocuments.Get(t.Context(), "merge_relation_refs", scope+next.Relation.ID)
		var ref relationReference
		if err != nil || json.Unmarshal(raw, &ref) != nil || ref.TenantID != next.Relation.TenantID || ref.AlertID != alert || ref.RelationID != next.Relation.ID || ref.Role != role {
			t.Fatal("incorrect reference identity", offset, ref, err)
		}
	}
	if final, err := j.IndexRelationBatch(t.Context(), next, next.Relation.UpdatedAt, 16); err != nil || final.Version != next.Version || docs.creates != 3 || docs.relationWrites != 1 {
		t.Fatal("completed prefix repeated writes", final, err)
	}
}

func TestIndexRelationBatchFailureLeavesPrefixForIdempotentRetry(t *testing.T) {
	for _, mode := range []string{"partial create", "later chunk partial create", "progress CAS", "canceled after create"} {
		t.Run(mode, func(t *testing.T) {
			members := 8
			if mode == "later chunk partial create" {
				members = 256
			}
			j, docs, current := referenceBatchFixture(t, members)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var want error
			switch mode {
			case "partial create":
				docs.partialCreate = true
			case "later chunk partial create":
				docs.partialCreate, docs.failCreateCall = true, 2
			case "progress CAS":
				docs.failKind = "merge_relations"
			case "canceled after create":
				docs.afterCreate, want = cancel, context.Canceled
			}
			if _, err := j.IndexRelationBatch(ctx, current, current.Relation.UpdatedAt, members+1); err == nil || (want != nil && !errors.Is(err, want)) {
				t.Fatal("batch failure changed existing single-step semantics", err, want)
			}
			actual, err := j.GetRelation(t.Context(), current.Relation.TenantID, current.Relation.ID)
			if err != nil || actual.Version != current.Version || actual.Relation.IndexOffset != 0 {
				t.Fatal("failed batch advanced prefix", actual, err)
			}
			written := map[string]string{}
			for key, raw := range docs.data {
				written[key] = string(raw)
			}
			docs.partialCreate, docs.afterCreate = false, nil
			next, err := j.IndexRelationBatch(t.Context(), actual, actual.Relation.UpdatedAt, members+1)
			if err != nil || next.Relation.IndexOffset != members+1 || next.Version != "2" {
				t.Fatal("partial prefix did not resume", next, err)
			}
			for key, raw := range written {
				if key != "merge_relations/"+prefix(current.Relation.TenantID)+current.Relation.ID && string(docs.data[key]) != raw {
					t.Fatal("retry changed a fixed reference", key)
				}
			}
		})
	}
}

func TestIndexRelationBatchRepairsDerivedReferenceWithoutReadback(t *testing.T) {
	j, docs, current := referenceBatchFixture(t, 4)
	r := current.Relation
	scope, err := referenceScope(r.TenantID, r.ParentAlertID)
	if err != nil {
		t.Fatal(err)
	}
	key := scope + r.ID
	raw, err := json.Marshal(relationReference{TenantID: "foreign", AlertID: r.ParentAlertID, RelationID: r.ID, Role: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	if err := docs.memoryDocuments.Put(t.Context(), "merge_relation_refs", key, "", raw); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		next, err := j.IndexRelationBatch(t.Context(), current, r.UpdatedAt, 5)
		if err != nil {
			t.Fatal(err)
		}
		current = next
	}
	stored, _, err := docs.memoryDocuments.Get(t.Context(), "merge_relation_refs", key)
	var got relationReference
	if err != nil || json.Unmarshal(stored, &got) != nil || got.TenantID != r.TenantID || docs.singleRefs != 0 || docs.reads != 0 {
		t.Fatal("derived reference was not repaired directly", string(stored), err)
	}
}

func TestIndexRelationBatchPreservesSingleDocumentFallbackAndValidation(t *testing.T) {
	j, docs, current := referenceBatchFixture(t, 4)
	j.docs = docs.memoryDocuments
	first, err := j.IndexRelationBatch(t.Context(), current, current.Relation.UpdatedAt, 2)
	if err != nil || first.Relation.IndexOffset != 2 || first.Version != "2" {
		t.Fatal("legacy document reader lost single progress CAS", first, err)
	}
	next, err := j.IndexRelationBatch(t.Context(), first, first.Relation.UpdatedAt, 3)
	if err != nil || next.Relation.IndexOffset != 5 || next.Version != "3" {
		t.Fatal("prefix order changed across batches", next, err)
	}
	if _, err := j.IndexRelationBatch(t.Context(), next, next.Relation.UpdatedAt, 0); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("invalid batch size accepted", err)
	}
	if _, err := j.IndexRelationBatch(t.Context(), next, next.Relation.UpdatedAt.Add(-time.Second), 1); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("old processing time accepted", err)
	}
	stale := first
	if _, err := j.IndexRelationBatch(t.Context(), stale, stale.Relation.UpdatedAt, 1); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("stale relation progress overwrote new prefix", err)
	}
}
