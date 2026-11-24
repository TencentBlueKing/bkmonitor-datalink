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
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
	"linkd/internal/store/memory"
)

type boundedMemberReads struct {
	*memory.Repository
	ids         map[string]bool
	used, limit int
}

func TestParentRecoveryCheckpointRejectsInvalidReads(t *testing.T) {
	for _, tc := range []struct {
		name  string
		alter func(store.StoredAlert) (store.StoredAlert, error)
	}{
		{"read failure", func(a store.StoredAlert) (store.StoredAlert, error) { return a, context.DeadlineExceeded }},
		{"foreign tenant", func(a store.StoredAlert) (store.StoredAlert, error) { a.Alert.BKTenantID = "foreign"; return a, nil }},
		{"another member", func(a store.StoredAlert) (store.StoredAlert, error) { a.Alert.AlertID = "a"; return a, nil }},
		{"missing version", func(a store.StoredAlert) (store.StoredAlert, error) { a.Version = store.VersionToken{}; return a, nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExecutorFixture(t)
			startExecutor(t, f)
			done := driveExecutor(t, f)
			endChild(t, f.relationFixture, "a")
			endChild(t, f.relationFixture, "b")
			f.engine.CurrentAlert = func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
				a, err := f.repo.GetAlert(ctx, tenant, id)
				if err == nil && id == "b" {
					return tc.alter(a)
				}
				return a, err
			}
			at := f.decision.FrozenAt.Add(4 * time.Second)
			if err := f.engine.CheckRelation(t.Context(), f.decision.TenantID, f.decision.ID, at); err == nil {
				t.Fatal("invalid read counted as terminal proof")
			}
			r, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
			if err != nil {
				t.Fatal(err)
			}
			a, _ := r.Relation.Member("a")
			b, _ := r.Relation.Member("b")
			if a.State != "terminal" || b.State == "terminal" {
				t.Fatal("valid checkpoint lost or invalid read confirmed")
			}
			parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, done.Decision.Progress.ParentAlertID)
			if err != nil || parent.Alert.Status != domain.AlertStatusActive || len(f.action.inputs) != 1 {
				t.Fatal("parent changed on incomplete proof", err)
			}
			f.engine.CurrentAlert = f.repo.GetAlert
			if err := f.engine.CheckRelation(t.Context(), f.decision.TenantID, f.decision.ID, at); err != nil {
				t.Fatal(err)
			}
			parent, err = f.repo.GetAlert(t.Context(), f.decision.TenantID, parent.Alert.AlertID)
			if err != nil || parent.Alert.Status != domain.AlertStatusRecovered || len(f.action.inputs) != 2 {
				t.Fatal("retry did not resume recovery", err)
			}
		})
	}
}

func TestParentRecoveryCheckpointKeepsParentRoleBoundary(t *testing.T) {
	f := newExecutorFixture(t)
	startExecutor(t, f)
	done := driveExecutor(t, f)
	f.engine.CurrentAlert = func(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
		a, err := f.repo.GetAlert(ctx, tenant, id)
		if err == nil && id == done.Decision.Progress.ParentAlertID {
			a.Alert.EventSourceID = "external"
		}
		return a, err
	}
	if err := f.engine.CheckRelation(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(4*time.Second)); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("non-builtin parent accepted", err)
	}
}

func (r *boundedMemberReads) GetAlert(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	if r.ids[id] {
		r.used++
		if r.used > r.limit {
			return store.StoredAlert{}, context.DeadlineExceeded
		}
	}
	return r.Repository.GetAlert(ctx, tenant, id)
}

// 模拟每轮只能完成部分成员读取的下游预算；终态不可逆，已验证事实应保留以避免从头读取永久超时。
func TestParentRecoveryKeepsTerminalProgressAcrossReadTimeouts(t *testing.T) {
	f := newExecutorFixture(t)
	for i := 0; i < 14; i++ {
		id := fmt.Sprintf("child-%02d", i)
		a := f.state.result.Members[0].Alert.Clone()
		a.AlertID, a.Fingerprint, a.SourceAlertID = id, "fp-"+id, id
		a.TriggerEventID, a.LatestEventID, a.SourceEventID = "opening-"+id, "opening-"+id, "source-"+id
		a.Merge.Pending[0].MemberEventID = a.TriggerEventID
		v, err := f.repo.CreateAlert(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		f.state.result.Members = append(f.state.result.Members, v.StoredAlert)
		f.state.result.Window.Members = append(f.state.result.Window.Members, redisstate.MergeMember{Main: domain.DependencyMain{AlertID: id}})
		f.state.result.Window.Frozen.MemberIDs = append(f.state.result.Window.Frozen.MemberIDs, id)
	}
	slices.Sort(f.state.result.Window.Frozen.MemberIDs)
	startExecutor(t, f)
	done := driveExecutor(t, f)
	ids := map[string]bool{}
	for _, id := range done.Decision.MemberIDs {
		ids[id] = true
		endChild(t, f.relationFixture, id)
	}
	reader := &boundedMemberReads{Repository: f.repo, ids: ids, limit: 4}
	p, err := lifecycle.NewProcessor(reader, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, []lifecycle.NamedFinalHook{{Name: "action", Purpose: "action", Hook: f.action}, {Name: "state", Purpose: "state", Hook: f.relationFixture.state}}, config.DefaultSeverityConfig(), journalClock{at: f.decision.FrozenAt.Add(4 * time.Second)}, slog.New(slog.NewTextHandler(io.Discard, nil)), lifecycle.WithMergeRelations(f.journal))
	if err != nil {
		t.Fatal(err)
	}
	f.engine.CurrentAlert = reader.GetAlert
	f.engine.Operations = p
	completed := false
	for attempt := 0; attempt < 16; attempt++ {
		reader.used = 0
		err := f.engine.CheckRelation(t.Context(), f.decision.TenantID, f.decision.ID, f.decision.FrozenAt.Add(time.Duration(5+attempt)*time.Second))
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		r, err := f.journal.GetRelation(t.Context(), f.decision.TenantID, f.decision.ID)
		if err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			confirmed := 0
			for _, m := range r.Relation.Members {
				if m.State == "terminal" {
					confirmed++
				}
			}
			if confirmed == 0 {
				t.Fatal("timeout discarded every observed terminal member; next round starts from the same prefix")
			}
		}
		if r.Relation.State == "ended" {
			completed = true
			break
		}
	}
	if !completed {
		t.Fatal("bounded reads never completed parent recovery and relation cleanup")
	}
	parent, err := f.repo.GetAlert(t.Context(), f.decision.TenantID, done.Decision.Progress.ParentAlertID)
	if err != nil || parent.Alert.Status != domain.AlertStatusRecovered || len(f.action.inputs) != 2 {
		t.Fatal("parent was not recovered exactly once", err)
	}
}
