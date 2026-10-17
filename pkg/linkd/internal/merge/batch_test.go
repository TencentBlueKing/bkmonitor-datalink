// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store/storetest"
)

type countedBatchDocuments struct {
	*memoryDocuments
	reads, creates, singleReads int
	failCreate                  int
}

func (d *countedBatchDocuments) Get(ctx context.Context, kind, id string) (json.RawMessage, string, error) {
	if kind == "merge_members" {
		d.singleReads++
	}
	return d.memoryDocuments.Get(ctx, kind, id)
}

func (d *countedBatchDocuments) GetMany(ctx context.Context, kind string, ids []string) (map[string]json.RawMessage, error) {
	d.reads++
	out := map[string]json.RawMessage{}
	for _, id := range ids {
		raw, _, err := d.memoryDocuments.Get(ctx, kind, id)
		if errors.Is(err, policy.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out[id] = raw
	}
	return out, nil
}

func (d *countedBatchDocuments) CreateMany(ctx context.Context, kind string, rows map[string]json.RawMessage) error {
	d.creates++
	if d.creates == d.failCreate {
		return errors.New("batch unavailable")
	}
	for id, raw := range rows {
		if err := d.Put(ctx, kind, id, "", raw); err != nil && !errors.Is(err, policy.ErrConflict) {
			return err
		}
	}
	return nil
}

func TestMergeSnapshotsUseBoundedBatchesAndPreserveFirstCapture(t *testing.T) {
	j, docs := newJournal(t)
	batch := &countedBatchDocuments{memoryDocuments: docs, failCreate: 2}
	j.docs = batch
	d := decisionFixture(t)
	d.MemberIDs, d.WaitMemberIDs = nil, nil
	alerts := []domain.Alert{}
	for i := range 256 {
		id := fmt.Sprintf("member-%03d", i)
		d.MemberIDs = append(d.MemberIDs, id)
		d.WaitMemberIDs = append(d.WaitMemberIDs, id)
		a := storetest.Alert(d.TenantID, id, "event-"+id, "fp-"+id, "warning")
		a.Title = "first"
		alerts = append(alerts, a)
	}
	saved, err := j.Claim(t.Context(), d)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.captureMany(t.Context(), saved, alerts); err == nil {
		t.Fatal("batch failure hidden")
	}
	current, err := j.Get(t.Context(), d.TenantID, d.ID)
	if err != nil || current.Decision.Progress.CaptureOffset != 0 {
		t.Fatal("failed batch advanced progress", err)
	}
	for i := range alerts {
		alerts[i].Title = "later"
	}
	batch.failCreate = 0
	if err := j.captureMany(t.Context(), saved, alerts); err != nil {
		t.Fatal(err)
	}
	before := batch.reads
	members, err := j.Members(t.Context(), current.Decision)
	if err != nil || len(members) != 256 {
		t.Fatal(err)
	}
	if batch.reads-before != 2 || batch.singleReads != 0 || batch.creates != 4 {
		t.Fatalf("reads=%d single=%d creates=%d", batch.reads-before, batch.singleReads, batch.creates)
	}
	for i, member := range members {
		want := "later"
		if i < 128 {
			want = "first"
		}
		if member.Alert.Title != want {
			t.Fatalf("member %d snapshot overwritten: %s", i, member.Alert.Title)
		}
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := j.Members(canceled, current.Decision); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel hidden", err)
	}
}

func TestExecutorRendersFromOneCompleteSnapshotRead(t *testing.T) {
	f := newExecutorFixture(t)
	batch := &countedBatchDocuments{memoryDocuments: f.docs}
	f.journal.docs = batch
	startExecutor(t, f)
	before := batch.reads
	if err := f.engine.StepDecision(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt); err != nil {
		t.Fatal(err)
	}
	current, err := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
	if err != nil || current.Decision.Progress.Phase != "prepared" {
		t.Fatal("parent not prepared", err)
	}
	if batch.reads-before != 1 || batch.singleReads != 0 {
		t.Fatalf("render repeated member reads: batch=%d single=%d", batch.reads-before, batch.singleReads)
	}
}
