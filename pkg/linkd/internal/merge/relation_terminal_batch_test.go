// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func terminalBatchFixture(t *testing.T, members int) executorFixture {
	t.Helper()
	f, current := linkBatchFixture(t, members-2, 0)
	if _, err := f.journal.LinkMembersBatch(t.Context(), current, f.parent, f.processor, f.decision.FrozenAt.Add(3*time.Second), members+1, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := f.journal.LinkStep(t.Context(), f.decision.TenantID, f.decision.ID, f.parent, f.processor, f.decision.FrozenAt.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	f.engine.Batch = config.MergeControlConfig{MemberBatchSize: 64, Concurrency: 4}
	return f
}

func terminalBatchReader(f executorFixture, requests *[][]string, failCall int, failure error) func(context.Context, string, []string) (store.AlertBatch, error) {
	return func(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
		*requests = append(*requests, append([]string(nil), ids...))
		if failCall > 0 && len(*requests) == failCall {
			return store.AlertBatch{}, failure
		}
		batch := store.AlertBatch{}
		for i := len(ids) - 1; i >= 0; i-- {
			a, err := f.repo.GetAlert(ctx, tenant, ids[i])
			if err != nil {
				return batch, err
			}
			batch.Alerts = append(batch.Alerts, a)
		}
		return batch, nil
	}
}

func TestRelationTerminalChecksUseOneReadAndOneCASPerConfirmationBatch(t *testing.T) {
	f := terminalBatchFixture(t, 70)
	for _, id := range f.decision.MemberIDs {
		endChild(t, f.relationFixture, id)
	}
	current := endingBatchRelation(t, f)
	requests := [][]string{}
	f.engine.CurrentAlerts = terminalBatchReader(f, &requests, 0, nil)
	individualChildren := 0
	f.engine.CurrentAlert = func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
		if id != f.parent.Alert.AlertID {
			individualChildren++
		}
		return f.repo.GetAlert(ctx, tenant, id)
	}
	docs := &linkBatchDocuments{Documents: f.journal.docs, writes: map[string]int{}}
	f.journal.docs = docs
	next, err := f.engine.checkRelationStep(t.Context(), current, f.decision.FrozenAt.Add(5*time.Second))
	if err != nil || len(requests) != 1 || len(requests[0]) != 64 || individualChildren != 0 {
		t.Fatal("terminal scan retained per-member reads", requests, individualChildren, err)
	}
	relationWrites, _ := docs.counts()
	if relationWrites != 1 {
		t.Fatal("terminal confirmations were persisted per member", relationWrites)
	}
	confirmed := 0
	for _, m := range next.Relation.Members {
		if m.State == "terminal" {
			confirmed++
		}
	}
	if confirmed != 64 || next.Relation.State != "ready" {
		t.Fatal("batch exceeded confirmation budget or recovered early", confirmed, next.Relation.State)
	}
	again, err := f.engine.checkRelationStep(t.Context(), next, f.decision.FrozenAt.Add(6*time.Second))
	if err != nil || len(requests) != 2 || len(requests[1]) != 6 || again.Relation.State != "ending" {
		t.Fatal("remaining terminal batch did not recover parent", requests, again.Relation.State, err)
	}
	parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, f.parent.Alert.AlertID)
	if err != nil || parent.Alert.Status != domain.AlertStatusRecovered || len(f.action.inputs) != 2 {
		t.Fatal("parent did not recover exactly once", err)
	}
}

func TestRelationTerminalChecksRetainPrefixOnLaterBatchFailure(t *testing.T) {
	f := terminalBatchFixture(t, 260)
	f.engine.Batch.MemberBatchSize = 256
	for _, id := range f.decision.MemberIDs {
		endChild(t, f.relationFixture, id)
	}
	requests := [][]string{}
	failure := errors.New("second real-time batch unavailable")
	f.engine.CurrentAlerts = terminalBatchReader(f, &requests, 2, failure)
	_, err := f.engine.checkRelationStep(t.Context(), endingBatchRelation(t, f), f.decision.FrozenAt.Add(5*time.Second))
	if !errors.Is(err, failure) {
		t.Fatal("batch failure was swallowed", err)
	}
	partial := endingBatchRelation(t, f)
	confirmed := 0
	for _, m := range partial.Relation.Members {
		if m.State == "terminal" {
			confirmed++
		}
	}
	if confirmed != 128 {
		t.Fatal("successful prior chunk was discarded or failure skipped", confirmed)
	}
	requests = nil
	f.engine.CurrentAlerts = terminalBatchReader(f, &requests, 0, nil)
	if _, err := f.engine.checkRelationStep(t.Context(), partial, f.decision.FrozenAt.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || len(requests[0]) != 128 || len(requests[1]) != 4 {
		t.Fatal("retry re-read terminal checkpoints", requests)
	}
}

type terminalRepairOperator struct {
	Operations
	finish func(context.Context, string, string) (store.StoredAlert, error)
}

func (o terminalRepairOperator) FinishMergeChanges(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	return o.finish(ctx, tenant, id)
}

func TestRelationTerminalChecksStopAtFirstActiveAndRepairPendingBeforeConfirmation(t *testing.T) {
	for _, failRepair := range []bool{false, true} {
		t.Run(fmt.Sprintf("repairFailure=%t", failRepair), func(t *testing.T) {
			f := terminalBatchFixture(t, 3)
			endChild(t, f.relationFixture, "a")
			endChild(t, f.relationFixture, "c-00")
			finished := []string{}
			individuals := []string{}
			failure := errors.New("pending output unavailable")
			f.engine.Operations = terminalRepairOperator{Operations: f.processor, finish: func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
				finished = append(finished, id)
				if failRepair {
					return store.StoredAlert{}, failure
				}
				return f.repo.GetAlert(ctx, tenant, id)
			}}
			f.engine.CurrentAlert = func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
				individuals = append(individuals, id)
				return f.repo.GetAlert(ctx, tenant, id)
			}
			f.engine.CurrentAlerts = func(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
				batch := store.AlertBatch{}
				for _, id := range ids {
					a, err := f.repo.GetAlert(ctx, tenant, id)
					if err != nil {
						return batch, err
					}
					if a.Alert.Status.Terminal() {
						a.Alert.PolicyChange = &domain.AlertPolicyChange{OperationID: "pending-terminal-policy-output", EffectiveAt: a.Alert.UpdateAt, Before: []domain.ShieldBinding{storetest.ShieldBinding(a.Alert.CreateAt)}}
						if err := a.Alert.Validate(); err != nil {
							t.Fatal("pending read fixture", err)
						}
					}
					batch.Alerts = append(batch.Alerts, a)
				}
				return batch, nil
			}
			_, err := f.engine.checkRelationStep(t.Context(), endingBatchRelation(t, f), f.decision.FrozenAt.Add(5*time.Second))
			if failRepair && !errors.Is(err, failure) {
				t.Fatal("pending output error swallowed", err)
			}
			if !failRepair && err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(finished, []string{"a"}) {
				t.Fatal("active boundary repaired later member", finished)
			}
			r := endingBatchRelation(t, f)
			a, _ := r.Relation.Member("a")
			b, _ := r.Relation.Member("b")
			c, _ := r.Relation.Member("c-00")
			expected := "terminal"
			if failRepair {
				expected = "linked"
			}
			if a.State != expected || b.State != "linked" || c.State != "linked" || r.Relation.State != "ready" || len(f.action.inputs) != 1 {
				t.Fatal("active member or pending error crossed confirmation prefix", r.Relation.Members)
			}
			if !failRepair && (len(individuals) != 2 || individuals[1] != "a") {
				t.Fatal("repaired terminal was not re-read", individuals)
			}
		})
	}
}

type terminalReferenceDocuments struct {
	*referenceBatchDocuments
	updates int
}

func (d *terminalReferenceDocuments) Put(ctx context.Context, kind, id, expected string, raw json.RawMessage) error {
	if kind == "merge_relations" && expected != "" {
		d.updates++
	}
	return d.referenceBatchDocuments.Put(ctx, kind, id, expected, raw)
}

func TestEndRelationBatchBuildsIncompleteReferencesInBatchBeforeUnlink(t *testing.T) {
	f := newRelationFixture(t)
	parent := closeRelationParent(t, f)
	at := f.decision.FrozenAt.Add(3 * time.Second)
	r, err := f.journal.BeginEndRelation(t.Context(), f.decision.TenantID, f.decision.ID, parent, at)
	if err != nil {
		t.Fatal(err)
	}
	if r.Relation.IndexOffset != 0 {
		t.Fatal("fixture unexpectedly indexed")
	}
	docs := &terminalReferenceDocuments{referenceBatchDocuments: &referenceBatchDocuments{memoryDocuments: f.docs}}
	f.journal.docs = docs
	p, err := lifecycle.NewProcessor(f.repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: &endingBatchHook{hook: f.state}}, {Name: "action", Purpose: "action", Hook: &endingBatchHook{hook: f.action}}}, config.DefaultSeverityConfig(), journalClock{at: at}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(f.journal))
	if err != nil {
		t.Fatal(err)
	}
	ended, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, p, at, 16, 4)
	if err != nil || ended.Relation.State != "ended" || docs.creates != 1 || docs.reads != 0 || docs.singleRefs != 0 || docs.updates != 2 || len(f.action.inputs) != 0 {
		t.Fatal("early close indexed references one by one or admitted child", ended.Relation.State, docs.creates, docs.reads, docs.singleRefs, docs.updates, err)
	}
	states := len(f.state.inputs)
	writes := docs.updates
	again, err := f.journal.EndRelationBatchWithBudget(t.Context(), f.decision.TenantID, f.decision.ID, p, at.Add(time.Second), 16, 4)
	if err != nil || again.Version != ended.Version || docs.updates != writes || len(f.state.inputs) != states {
		t.Fatal("early-close retry was not idempotent", err)
	}
}

func TestRelationTerminalBatchRejectsMissingAndForeignRowsWithoutLosingEarlierProof(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreign=%t", foreign), func(t *testing.T) {
			f := terminalBatchFixture(t, 3)
			endChild(t, f.relationFixture, "a")
			endChild(t, f.relationFixture, "b")
			endChild(t, f.relationFixture, "c-00")
			f.engine.CurrentAlerts = func(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
				batch := store.AlertBatch{}
				for _, id := range ids {
					if id == "b" && !foreign {
						batch.NotFound = append(batch.NotFound, id)
						continue
					}
					a, err := f.repo.GetAlert(ctx, tenant, id)
					if err != nil {
						return batch, err
					}
					if id == "b" {
						a.Alert.BKTenantID = "foreign"
					}
					batch.Alerts = append(batch.Alerts, a)
				}
				return batch, nil
			}
			_, err := f.engine.checkRelationStep(t.Context(), endingBatchRelation(t, f), f.decision.FrozenAt.Add(5*time.Second))
			expected := store.ErrNotFound
			if foreign {
				expected = policy.ErrAccess
			}
			if !errors.Is(err, expected) {
				t.Fatal("invalid batch row accepted", err)
			}
			r := endingBatchRelation(t, f)
			a, _ := r.Relation.Member("a")
			b, _ := r.Relation.Member("b")
			if a.State != "terminal" || b.State != "linked" {
				t.Fatal("row error discarded valid prefix or confirmed invalid member", r.Relation.Members)
			}
		})
	}
}
