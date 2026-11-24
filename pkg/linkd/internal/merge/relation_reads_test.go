// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type alteredMergeRead struct {
	*memory.Repository
	target string
	alter  func(*store.StoredAlert)
}

func TestUnlinkRejectsUnexpectedParentOrigin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*store.StoredAlert)
	}{
		{"ordinary source", func(a *store.StoredAlert) { a.Alert.EventSourceID = "ordinary" }},
		{"missing aggregate role", func(a *store.StoredAlert) { a.Alert.Merge = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRelationFixture(t)
			finishLinks(t, f)
			d := f.decision
			parent := closeRelationParent(t, f)
			if _, err := f.journal.BeginEndRelation(t.Context(), d.TenantID, d.ID, parent, d.FrozenAt.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			before, err := f.repo.GetAlert(t.Context(), d.TenantID, "a")
			if err != nil {
				t.Fatal(err)
			}
			stateCount, actionCount := len(f.state.inputs), len(f.action.inputs)
			p, err := lifecycle.NewProcessor(alteredMergeRead{f.repo, parent.Alert.AlertID, tc.alter}, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: f.state}, {Name: "action", Purpose: "action", Hook: f.action}}, config.DefaultSeverityConfig(), journalClock{at: d.FrozenAt.Add(4 * time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(f.journal))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.UnlinkMergeMember(t.Context(), d.TenantID, "a", d.ID); err == nil {
				t.Fatal("unrelated terminal snapshot allowed relationship removal")
			}
			after, err := f.repo.GetAlert(t.Context(), d.TenantID, "a")
			if err != nil || !reflect.DeepEqual(before, after) || len(f.state.inputs) != stateCount || len(f.action.inputs) != actionCount {
				t.Fatal("invalid parent read changed child or emitted output", err)
			}
		})
	}
}

func TestMergePendingReadValidatesScopeBeforeHooks(t *testing.T) {
	f := newRelationFixture(t)
	prepareReferences(t, f)
	a, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, "a")
	if err != nil {
		t.Fatal(err)
	}
	// 返回一个结构有效、仍待完成输出的其他租户快照；读取失败必须早于 Hook 执行。
	next := a.Alert.Clone()
	next.BKTenantID = "foreign"
	next.Merge, _ = next.Merge.ReleaseWindow(f.decision.WindowID)
	next.UpdateAt = next.UpdateAt.Add(time.Second)
	next.MergeChange = &domain.AlertMergeChange{Kind: "release", OperationID: "foreign-release", WindowID: f.decision.WindowID, EffectiveAt: next.UpdateAt, Before: a.Alert.Merge.Clone(), After: next.Merge.Clone()}
	if err := next.Validate(); err != nil {
		t.Fatal(err)
	}
	stateCount, actionCount := len(f.state.inputs), len(f.action.inputs)
	p, err := lifecycle.NewProcessor(alteredMergeRead{f.repo, "a", func(a *store.StoredAlert) { a.Alert = next.Clone() }}, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: f.state}, {Name: "action", Purpose: "action", Hook: f.action}}, config.DefaultSeverityConfig(), journalClock{at: next.UpdateAt}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(f.journal))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.FinishMergeChanges(t.Context(), f.decision.TenantID, "a"); err == nil {
		t.Fatal("foreign pending output accepted")
	}
	if len(f.state.inputs) != stateCount || len(f.action.inputs) != actionCount {
		t.Fatal("foreign pending output reached hooks")
	}
}

func (r alteredMergeRead) GetAlert(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	a, err := r.Repository.GetAlert(ctx, tenant, id)
	if err == nil && id == r.target {
		r.alter(&a)
	}
	return a, err
}

func TestParentReadinessRejectsInvalidMemberFactsBeforeAdmission(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(*store.StoredAlert)
	}{
		{"foreign tenant", func(a *store.StoredAlert) { a.Alert.BKTenantID = "foreign" }},
		{"different alert", func(a *store.StoredAlert) { a.Alert.AlertID = "different" }},
		{"missing version", func(a *store.StoredAlert) { a.Version = store.VersionToken{} }},
		{"invalid snapshot", func(a *store.StoredAlert) { a.Alert.Severity = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRelationFixture(t)
			d := f.decision
			r := prepareReferences(t, f)
			for _, id := range []string{"a", "b"} {
				linked, err := f.processor.LinkMergeMember(t.Context(), d.TenantID, id, d.ID)
				if err != nil {
					t.Fatal(err)
				}
				r, err = f.journal.ConfirmRelationMember(t.Context(), r, linked, d.FrozenAt)
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.journal.ReadyRelation(t.Context(), r, d.FrozenAt); err != nil {
				t.Fatal(err)
			}
			before, err := f.repo.GetAlert(t.Context(), d.TenantID, f.parent.Alert.AlertID)
			if err != nil {
				t.Fatal(err)
			}
			stateCount := len(f.state.inputs)
			p, err := lifecycle.NewProcessor(alteredMergeRead{f.repo, "a", tc.alter}, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "state", Purpose: "state", Hook: f.state}, {Name: "action", Purpose: "action", Hook: f.action}}, config.DefaultSeverityConfig(), journalClock{at: d.FrozenAt.Add(time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(f.journal))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := p.ReadyMergeParent(t.Context(), d.TenantID, f.parent.Alert.AlertID, d.ID); err == nil {
				t.Fatal("invalid member was accepted as proof for parent admission")
			}
			after, err := f.repo.GetAlert(t.Context(), d.TenantID, f.parent.Alert.AlertID)
			if err != nil || !reflect.DeepEqual(before, after) || len(f.state.inputs) != stateCount || len(f.action.inputs) != 0 {
				t.Fatal("invalid read changed parent or emitted output", err)
			}
		})
	}
}
