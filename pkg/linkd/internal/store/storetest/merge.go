// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storetest

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

func runMergeContract(t *testing.T, factory Factory) {
	for _, kind := range []string{"member_unlink", "parent_recover"} {
		t.Run("merge end intent "+kind, func(t *testing.T) {
			repo := factory(t)
			relation, window := strings.Repeat("e", 64), strings.Repeat("f", 64)
			a := Alert("tenant", "end-"+kind, "opening", "fp-"+kind, "warning")
			a.Merge = &domain.AlertMerge{Role: "original", State: "merged", RelationIDs: []string{relation}}
			if kind == "parent_recover" {
				a.EventSourceID = domain.BuiltinMergeEventSourceID
				a.Merge.Role = "aggregate"
				a.Merge.RelationsReady = true
				a.Merge.OperationID = relation
				at := a.UpdateAt
				a.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "system_operation", CauseID: "admission"}
			}
			created, err := repo.CreateAlert(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			next := a.Clone()
			next.UpdateAt = next.UpdateAt.Add(time.Second)
			if kind == "parent_recover" {
				next.Status = domain.AlertStatusRecovered
				next.EndType = domain.AlertEndTypeSystem
				next.EndReason = "merge_members_ended"
				next.EndAt = &next.UpdateAt
			} else {
				next.Merge, _, err = next.Merge.WithoutRelation(window, relation)
				if err != nil {
					t.Fatal(err)
				}
			}
			next.MergeChange = &domain.AlertMergeChange{Kind: kind, OperationID: "end-operation", RelationID: relation, WindowID: window, EffectiveAt: next.UpdateAt, Before: a.Merge.Clone(), After: next.Merge.Clone(), ActionReady: kind == "parent_recover"}
			updated, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, next)
			if err != nil {
				t.Fatal(err)
			}
			found, err := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
			if err != nil || !reflect.DeepEqual(found.Alert, updated.Alert) {
				t.Fatal("end intent roundtrip", err)
			}
			done := found.Alert.Clone()
			done.MergeChange = nil
			final, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, found.Version, done)
			if err != nil || final.Alert.Status != next.Status || !final.Alert.UpdateAt.Equal(next.UpdateAt) || !reflect.DeepEqual(final.Alert.Admission, a.Admission) {
				t.Fatal("end bookkeeping changed business state", err)
			}
		})
	}
	t.Run("merge release intent survives storage and gates business mutations", func(t *testing.T) {
		repo := factory(t)
		a := Alert("tenant", "release-member", "opening", "release-fp", "warning")
		w := domain.MergeWait{WindowID: strings.Repeat("a", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, GroupKey: strings.Repeat("c", 64), MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0}, StartedAt: a.UpdateAt, Deadline: a.UpdateAt.Add(time.Minute)}
		a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{w}}
		created, err := repo.CreateAlert(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		next := a.Clone()
		next.Merge, _ = next.Merge.ReleaseWindow(w.WindowID)
		next.UpdateAt = next.UpdateAt.Add(time.Second)
		operation := strings.Repeat("d", 64)
		at := next.UpdateAt
		next.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: a.Severity, CauseType: "system_operation", CauseID: operation}
		next.MergeChange = &domain.AlertMergeChange{Kind: "release", OperationID: operation, WindowID: w.WindowID, EffectiveAt: at, Before: a.Merge.Clone(), After: next.Merge.Clone(), ActionReady: true}
		updated, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, next)
		if err != nil {
			t.Fatal(err)
		}
		found, err := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
		if err != nil || !reflect.DeepEqual(found.Alert.MergeChange, updated.Alert.MergeChange) {
			t.Fatal("merge intent missing", err)
		}
		bad := found.Alert.Clone()
		bad.UpdateAt = bad.UpdateAt.Add(time.Second)
		bad.MergeChange = nil
		if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, found.Version, bad); err == nil {
			t.Fatal("business mutation bypassed intent")
		}
		done := found.Alert.Clone()
		done.MergeChange = nil
		cleared, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, found.Version, done)
		if err != nil || !cleared.Alert.UpdateAt.Equal(at) || cleared.Alert.MergeChange != nil {
			t.Fatal("metadata completion failed", err)
		}
	})

	t.Run("merge waiting round trip fixed boundaries and terminal", func(t *testing.T) {
		repo := factory(t)
		a := Alert("tenant", "merge-member", "opening", "fp", "warning")
		w := domain.MergeWait{WindowID: strings.Repeat("a", 64), Policy: domain.PolicyVersion{ID: "merge", Version: 1, Digest: strings.Repeat("b", 64)}, GroupKey: strings.Repeat("c", 64), MemberEventID: a.TriggerEventID, Severity: a.Severity, Groups: []int{0, 2}, StartedAt: a.UpdateAt, Deadline: a.UpdateAt.Add(time.Minute)}
		a.Merge = &domain.AlertMerge{Role: "original", State: "pending", Pending: []domain.MergeWait{w}}
		created, err := repo.CreateAlert(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		found, err := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
		if err != nil || !reflect.DeepEqual(found.Alert.Merge, a.Merge) {
			t.Fatal("merge round trip", err)
		}
		changed := found.Alert.Clone()
		changed.UpdateAt = changed.UpdateAt.Add(time.Second)
		changed.Merge.Pending[0].Deadline = changed.Merge.Pending[0].Deadline.Add(time.Second)
		if _, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, changed); err == nil {
			t.Fatal("deadline changed under same window")
		}
		changed = found.Alert.Clone()
		changed.UpdateAt = changed.UpdateAt.Add(time.Second)
		changed.Status = domain.AlertStatusRecovered
		changed.EndAt = &changed.UpdateAt
		changed.EndType = domain.AlertEndTypeSource
		changed.EndReason = "resolved"
		changed.Merge = changed.Merge.EndWaiting()
		terminal, err := repo.CompareAndSetAlert(t.Context(), a.BKTenantID, a.AlertID, created.Version, changed)
		if err != nil {
			t.Fatal(err)
		}
		found, err = repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
		if err != nil || !reflect.DeepEqual(found.Alert, terminal.Alert) || found.Alert.Merge.State != "released" {
			t.Fatal("terminal merge did not round trip", err)
		}
		// Event 裁决独立保存首次窗口，不随 Alert 终态清理而抹除历史证据。
		e := Event("tenant", "event", "fp", "warning")
		ce, err := repo.CreateEvent(t.Context(), e)
		if err != nil {
			t.Fatal(err)
		}
		enriched, err := repo.CompareAndSetEventEnrichment(t.Context(), e.BKTenantID, e.EventID, ce.Version, Enrichment(e))
		if err != nil {
			t.Fatal(err)
		}
		ref := store.PolicyReleaseRef{Kind: "merge", ID: w.Policy.ID, Version: w.Policy.Version, Digest: w.Policy.Digest}
		snapshot := &store.PolicyContext{EvaluatedAt: e.CreateAt, Releases: []store.PolicyReleaseRef{ref}}
		d := &store.PolicyDecision{Merge: &store.MergeDecision{Severity: "warning", Steps: []store.MergeStep{{Policy: ref, Outcome: "joined", Wait: &w}}}}
		frozen, err := repo.CompareAndSetEventResult(t.Context(), e.BKTenantID, e.EventID, enriched.Version, store.EventResult{State: domain.EventProcessStateUnprocessed, PolicyContext: snapshot})
		if err != nil {
			t.Fatal(err)
		}
		result, err := repo.CompareAndSetEventResult(t.Context(), e.BKTenantID, e.EventID, frozen.Version, store.EventResult{State: domain.EventProcessStateAccepted, Outcome: "alert_created", RelatedAlertIDs: []string{a.AlertID}, ProcessedAt: e.CreateAt.Add(time.Second), PolicyContext: snapshot, PolicyDecision: d})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(result.Processing.PolicyDecision, d) {
			t.Fatal("merge decision did not round trip")
		}
	})
}
