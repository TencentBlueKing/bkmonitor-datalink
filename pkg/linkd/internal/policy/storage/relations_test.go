// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	mergeflow "linkd/internal/merge"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

func runMergeRelationContract(t *testing.T, s *Store, reopen func() *Store) {
	t.Helper()
	j, err := mergeflow.NewJournal(s)
	if err != nil {
		t.Fatal(err)
	}
	page, err := j.ListWork(t.Context(), "", 16)
	if err != nil || len(page.Decisions) != 1 {
		t.Fatal("expected journal fixture", err)
	}
	d := page.Decisions[0]
	current, err := j.Get(t.Context(), d.TenantID, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	repo := memory.New()
	event := d.Progress.ParentEvent
	parent := storetest.Alert(d.TenantID, "relation-parent", event.EventID, event.Fingerprint, "warning")
	parent.EventSourceID = domain.BuiltinMergeEventSourceID
	parent.Merge = &domain.AlertMerge{Role: "aggregate", State: "none", OperationID: d.ID}
	created, err := repo.CreateAlert(t.Context(), parent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.RecordParent(t.Context(), current, created.StoredAlert, d.FrozenAt); err != nil {
		t.Fatal(err)
	}
	relation, err := j.EnsureRelation(t.Context(), d.TenantID, d.ID, created.StoredAlert, d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	for relation.Relation.IndexOffset < len(relation.Relation.Members)+1 {
		relation, err = j.IndexRelationStep(t.Context(), relation, d.FrozenAt)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range d.MemberIDs {
		child := storetest.Alert(d.TenantID, id, "opening-"+id, "fp-"+id, "warning")
		child.Merge = &domain.AlertMerge{Role: "original", State: "merged", RelationIDs: []string{d.ID}}
		saved, err := repo.CreateAlert(t.Context(), child)
		if err != nil {
			t.Fatal(err)
		}
		relation, err = j.ConfirmRelationMember(t.Context(), relation, saved.StoredAlert, d.FrozenAt)
		if err != nil {
			t.Fatal(err)
		}
	}
	ready, err := j.ReadyRelation(t.Context(), relation, d.FrozenAt)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := mergeflow.NewJournal(reopen())
	if err != nil {
		t.Fatal(err)
	}
	found, err := restarted.GetRelation(t.Context(), d.TenantID, d.ID)
	if err != nil || !reflect.DeepEqual(found.Relation, ready.Relation) {
		t.Fatal("relation did not survive reopen", err)
	}
	refreshMergeFixture(t, s, "merge_relation_refs")
	for _, id := range append([]string{parent.AlertID}, d.MemberIDs...) {
		rows, err := restarted.ListRelations(t.Context(), d.TenantID, id, "", 16)
		if err != nil || len(rows.Relations) != 1 || rows.Relations[0].State != "ready" {
			t.Fatal("parent/member lookup missing", err)
		}
	}
	first, err := restarted.ListRelationMembers(t.Context(), d.TenantID, d.ID, "", 1)
	if err != nil || len(first.Members) != 1 || first.Next == "" {
		t.Fatal("relation member paging", err)
	}
	last, err := restarted.ListRelationMembers(t.Context(), d.TenantID, d.ID, first.Next, 1)
	if err != nil || len(last.Members) != 1 || first.Members[0].AlertID == last.Members[0].AlertID {
		t.Fatal("relation page repeated", err)
	}
	p, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, config.DefaultSeverityConfig(), lifecycle.SystemClock{}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(restarted))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.CloseAlert(t.Context(), lifecycle.CloseAlertCommand{OperationID: "close", BKTenantID: d.TenantID, AlertID: parent.AlertID, OperatorKind: domain.OperatorKindUser, OperatorID: "tester", Reason: "manual", EffectiveAt: parent.UpdateAt.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		actual, err := repo.GetAlert(t.Context(), d.TenantID, parent.AlertID)
		if err != nil {
			t.Fatal(err)
		}
		next, err := restarted.EndRelationStep(t.Context(), d.TenantID, d.ID, actual, p, d.FrozenAt.Add(3*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if next.Relation.State == "ended" {
			break
		}
	}
	reopened, err := mergeflow.NewJournal(reopen())
	if err != nil {
		t.Fatal(err)
	}
	ended, err := reopened.GetRelation(t.Context(), d.TenantID, d.ID)
	if err != nil || ended.Relation.State != "ended" || ended.Relation.EndReason != "parent_ended" || ended.Relation.EndOffset != len(d.WaitMemberIDs) {
		t.Fatal("end relation did not survive reopen", err)
	}
	for _, id := range d.MemberIDs {
		a, err := repo.GetAlert(t.Context(), d.TenantID, id)
		if err != nil || a.Alert.Status != domain.AlertStatusActive || len(a.Alert.Merge.RelationIDs) != 0 || a.Alert.Admission.AdmittedAt != nil {
			t.Fatal("end relation changed member lifecycle", err)
		}
	}
}
