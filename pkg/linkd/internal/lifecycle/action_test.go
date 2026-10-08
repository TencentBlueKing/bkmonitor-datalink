// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/actiondelivery"
	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type intentRecorder struct {
	mu       sync.Mutex
	fail     bool
	requests map[string]actiondelivery.Request
	calls    int
}

func (r *intentRecorder) RecordAction(ctx context.Context, a domain.Alert, intent domain.AlertActionIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if a.Validate() != nil || intent.Validate(a) != nil {
		return errors.New("invalid frozen intent")
	}
	if r.requests == nil {
		r.requests = map[string]actiondelivery.Request{}
	}
	ids := []string{}
	for id := range intent.Targets {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		q, err := actiondelivery.BuildRequest(a, id, actiondelivery.Cause{Type: intent.CauseType, ID: intent.CauseID})
		if err != nil {
			return err
		}
		if old, ok := r.requests[q.ActionID]; ok && !reflect.DeepEqual(old, q) {
			return errors.New("original action changed")
		}
		r.requests[q.ActionID] = q
		if r.fail {
			return errors.New("injected partial enqueue failure")
		}
	}
	return nil
}

func actionProcessor(t *testing.T, repo store.Repository, recorder ActionRecorder) *Processor {
	p := newTestProcessor(t, repo, NoopFinalHook{})
	p.actionRecorder = recorder
	p.initialProjectionTargets = map[string]bool{"kac": true, "audit": false, "second": true}
	p.upgradePolicy = "update_current"
	return p
}

func TestActionIntentPartialEnqueueBlocksNewBusinessAndReplaysFrozenPlan(t *testing.T) {
	repo := memory.New()
	rec := &intentRecorder{fail: true}
	p := actionProcessor(t, repo, rec)
	e := storetest.Event("tenant", "opening", "fp", "warning")
	row, err := repo.CreateEvent(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ProcessEvent(t.Context(), row.StoredEvent); err == nil {
		t.Fatal("partial enqueue ignored")
	}
	pending, err := repo.GetEvent(t.Context(), e.BKTenantID, e.EventID)
	if err != nil || pending.Processing.Plan == nil || pending.Processing.State != domain.EventProcessStateUnprocessed {
		t.Fatal("event completed before enqueue", err)
	}
	id := pending.Processing.Plan.Mutations[0].Alert.AlertID
	a, err := repo.GetAlert(t.Context(), e.BKTenantID, id)
	if err != nil || a.Alert.ActionPending == nil || a.Alert.Revision != 1 || len(rec.requests) != 1 {
		t.Fatal("original intent lost", err)
	}
	// 缺少入队端口不能当成普通未配置 Hook 跳过已保存意图。
	missing := actionProcessor(t, repo, nil)
	if _, err = missing.FinishActionDelivery(t.Context(), e.BKTenantID, id); err == nil {
		t.Fatal("missing recorder discarded intent")
	}
	later := storetest.Event("tenant", "upgrade", "fp", "critical")
	later.Title = "later title"
	next, err := repo.CreateEvent(t.Context(), later)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ProcessEvent(t.Context(), next.StoredEvent); err == nil {
		t.Fatal("later event bypassed partial action")
	}
	now, _ := repo.GetAlert(t.Context(), e.BKTenantID, id)
	if now.Alert.Revision != 1 || now.Alert.LatestEventID != e.EventID {
		t.Fatal("later mutation overwrote pending snapshot")
	}
	rec.fail = false
	// 补扫仅需 Alert，不要求原 Event 再次到达，也不更新业务版本。
	drained, err := p.FinishActionDelivery(t.Context(), e.BKTenantID, id)
	if err != nil || drained.Alert.ActionPending != nil || drained.Alert.Revision != 1 || len(rec.requests) != 2 {
		t.Fatal("intent drain failed", err)
	}
	if _, err = p.ProcessEvent(t.Context(), pending); err != nil {
		t.Fatal("frozen plan retry conflicted with completed intent", err)
	}
	before := rec.calls
	if _, err = p.ProcessEvent(t.Context(), next.StoredEvent); err != nil {
		t.Fatal(err)
	}
	upgraded, _ := repo.GetAlert(t.Context(), e.BKTenantID, id)
	if upgraded.Alert.Revision != 2 || upgraded.Alert.Severity != "critical" || upgraded.Alert.Title != a.Alert.Title || len(rec.requests) != 4 || rec.calls != before+1 {
		t.Fatal("upgrade lost original snapshot or duplicated actions")
	}
	duplicate := storetest.Event("tenant", "repeat", "fp", "critical")
	persistAndProcess(t, repo, p, duplicate)
	if len(rec.requests) != 4 {
		t.Fatal("repeat generated extra action")
	}
	cmd := CloseAlertCommand{BKTenantID: "tenant", AlertID: id, OperationID: "manual-close", OperatorKind: domain.OperatorKindUser, OperatorID: "operator", Reason: "done", EffectiveAt: upgraded.Alert.UpdateAt.Add(time.Minute)}
	rec.fail = true
	if _, err = p.CloseAlert(t.Context(), cmd); err == nil {
		t.Fatal("close discarded failed enqueue")
	}
	closed, _ := repo.GetAlert(t.Context(), "tenant", id)
	if closed.Alert.Status != domain.AlertStatusClosed || closed.Alert.ActionPending == nil || closed.Alert.ActionPending.CauseID != cmd.OperationID {
		t.Fatal("close not atomic with original intent")
	}
	rec.fail = false
	if _, err = p.CloseAlert(t.Context(), cmd); err != nil {
		t.Fatal(err)
	}
	if len(rec.requests) != 6 {
		t.Fatal("close action not deduplicated", len(rec.requests))
	}
	calls := rec.calls
	if _, err = p.CloseAlert(t.Context(), cmd); err != nil || rec.calls != calls {
		t.Fatal("completed close reenqueued", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = p.FinishActionDelivery(ctx, "tenant", id); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	if _, err = p.FinishActionDelivery(t.Context(), "other", id); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("tenant crossed", err)
	}
}

type actionClearFailure struct {
	store.Repository
	fail  bool
	after bool
}

func (r *actionClearFailure) CompareAndSetAlert(ctx context.Context, tenant, id string, version store.VersionToken, next domain.Alert) (store.StoredAlert, error) {
	current, err := r.GetAlert(ctx, tenant, id)
	if err != nil {
		return store.StoredAlert{}, err
	}
	clearing := current.Alert.ActionPending != nil && next.ActionPending == nil && current.Alert.Revision == next.Revision
	if clearing && r.fail && !r.after {
		r.fail = false
		return store.StoredAlert{}, errors.New("injected clear failure")
	}
	updated, err := r.Repository.CompareAndSetAlert(ctx, tenant, id, version, next)
	if err == nil && clearing && r.fail {
		r.fail = false
		return store.StoredAlert{}, errors.New("injected lost clear response")
	}
	return updated, err
}

func TestActionIntentClearFailureAndACKRace(t *testing.T) {
	for _, after := range []bool{false, true} {
		t.Run(map[bool]string{false: "clear failed", true: "clear response lost"}[after], func(t *testing.T) {
			base := memory.New()
			repo := &actionClearFailure{Repository: base, fail: true, after: after}
			rec := &intentRecorder{}
			p := actionProcessor(t, repo, rec)
			e := storetest.Event("tenant", "opening", "fp", "warning")
			row, err := repo.CreateEvent(t.Context(), e)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.ProcessEvent(t.Context(), row.StoredEvent); err == nil {
				t.Fatal("clear failure ignored")
			}
			pending, _ := repo.GetEvent(t.Context(), "tenant", e.EventID)
			id := pending.Processing.Plan.Mutations[0].Alert.AlertID
			current, _ := repo.GetAlert(t.Context(), "tenant", id)
			next := current.Alert.Clone()
			next.Projection, _, err = next.Projection.Acknowledge("kac", 1, 1, next.UpdateAt.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if _, err = repo.CompareAndSetAlert(t.Context(), "tenant", id, current.Version, next); err != nil {
				t.Fatal(err)
			}
			restarted := actionProcessor(t, repo, rec)
			if _, err = restarted.ProcessEvent(t.Context(), pending); err != nil {
				t.Fatal(err)
			}
			final, _ := repo.GetAlert(t.Context(), "tenant", id)
			if final.Alert.Revision != 1 || final.Alert.ActionPending != nil || final.Alert.Projection.Targets["kac"].SyncedRevision != 1 || len(rec.requests) != 2 {
				t.Fatal("retry lost ACK or original action")
			}
		})
	}
}

func TestMergeReleasePersistsActionBeforeOrdinaryOutputs(t *testing.T) {
	repo := memory.New()
	rec := &intentRecorder{fail: true}
	p := actionProcessor(t, repo, rec)
	hook := &recordingHook{}
	p.finalHooks = []NamedFinalHook{{Name: "state", Purpose: "state", Hook: hook}}
	a := releaseAlert()
	a.Projection.Targets = map[string]domain.ProjectionTargetState{"kac": {ActionEnabled: true, SourceVersion: 1, RequiredRevision: 1}}
	if _, err := repo.CreateAlert(t.Context(), a); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID); err == nil {
		t.Fatal("enqueue failure ignored")
	}
	pending, _ := repo.GetAlert(t.Context(), a.BKTenantID, a.AlertID)
	if pending.Alert.ActionPending == nil || pending.Alert.MergeChange == nil || pending.Alert.Merge.State != "released" || len(hook.inputs) != 0 {
		t.Fatal("release escaped durable boundary")
	}
	rec.fail = false
	done, err := p.ReleaseMergeWindow(t.Context(), a.BKTenantID, a.AlertID, a.Merge.Pending[0].WindowID)
	if err != nil || done.Alert.ActionPending != nil || done.Alert.MergeChange != nil || len(rec.requests) != 1 || done.Alert.Revision != 2 {
		t.Fatal("release retry lost action", err)
	}
}

func TestActionRejectsOversizedSnapshotBeforeBusinessCommit(t *testing.T) {
	repo := memory.New()
	rec := &intentRecorder{}
	p := actionProcessor(t, repo, rec)
	e := storetest.Event("tenant", "large", "fp", "warning")
	e.Content = strings.Repeat("x", 1<<20)
	row, err := repo.CreateEvent(t.Context(), e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.ProcessEvent(t.Context(), row.StoredEvent); err == nil {
		t.Fatal("oversized request committed")
	}
	if len(rec.requests) != 0 {
		t.Fatal("oversized request enqueued")
	}
	if _, err = repo.FindActiveAlert(t.Context(), store.ActiveAlertKey{BKTenantID: e.BKTenantID, EventSourceID: e.EventSourceID, Fingerprint: e.Fingerprint}); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("unrecordable action changed business state", err)
	}
}

type intentACKConflict struct {
	store.Repository
	conflict bool
}

func (r *intentACKConflict) CompareAndSetAlert(ctx context.Context, tenant, id string, version store.VersionToken, next domain.Alert) (store.StoredAlert, error) {
	current, err := r.GetAlert(ctx, tenant, id)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if r.conflict && current.Alert.ActionPending != nil && next.ActionPending == nil {
		r.conflict = false
		ack := current.Alert.Clone()
		ack.Projection, _, err = ack.Projection.Acknowledge("kac", 1, ack.Revision, ack.UpdateAt.Add(time.Second))
		if err != nil {
			return store.StoredAlert{}, err
		}
		if _, err = r.Repository.CompareAndSetAlert(ctx, tenant, id, current.Version, ack); err != nil {
			return store.StoredAlert{}, err
		}
		return store.StoredAlert{}, store.ErrVersionConflict
	}
	return r.Repository.CompareAndSetAlert(ctx, tenant, id, version, next)
}

func TestActionEnqueueCompletionRetriesConcurrentProjectionACK(t *testing.T) {
	base := memory.New()
	repo := &intentACKConflict{Repository: base, conflict: true}
	rec := &intentRecorder{}
	p := actionProcessor(t, repo, rec)
	e := storetest.Event("tenant", "opening", "fp", "warning")
	result := persistAndProcess(t, repo, p, e)
	got, err := repo.GetAlert(t.Context(), "tenant", result.AlertIDs[0])
	if err != nil || got.Alert.Revision != 1 || got.Alert.ActionPending != nil || got.Alert.Projection.Targets["kac"].SyncedRevision != 1 || len(rec.requests) != 2 || rec.calls != 2 {
		t.Fatal("projection ACK conflict lost action or ACK", err)
	}
}

func TestInitialOutputTargetsAreExplicitAndCopied(t *testing.T) {
	create := func(targets map[string]bool, rec ActionRecorder) (*Processor, error) {
		return NewProcessor(memory.New(), NoopRecentAlertCache{}, DeterministicAlertIDGenerator{}, testNoopEnricher{}, nil, testSeverity{}, fixedClock{time.Now()}, discardLogger{}, WithInitialProjectionTargets(targets), WithActionRecorder(rec))
	}
	if _, err := create(map[string]bool{"kac": true}, nil); err == nil {
		t.Fatal("action target lacks reliable recorder")
	}
	if _, err := create(map[string]bool{"bad/id": false}, nil); err == nil {
		t.Fatal("invalid target accepted")
	}
	cfg := map[string]bool{"audit": false}
	p, err := create(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg["audit"] = true
	cfg["later"] = true
	if p.initialProjectionTargets["audit"] || len(p.initialProjectionTargets) != 1 {
		t.Fatal("source caller mutated processor bindings")
	}
}

func TestActionSourceRotationAndTerminalPreserveEveryAdmittedAction(t *testing.T) {
	for _, action := range []domain.EventAction{domain.EventActionResolved, domain.EventActionClosed} {
		t.Run(string(action), func(t *testing.T) {
			repo := memory.New()
			rec := &intentRecorder{}
			p := actionProcessor(t, repo, rec)
			p.upgradePolicy = "close_and_create"
			opening := storetest.Event("tenant", "opening", "fp", "warning")
			first := persistAndProcess(t, repo, p, opening)
			upgrade := storetest.Event("tenant", "upgrade", "fp", "critical")
			second := persistAndProcess(t, repo, p, upgrade)
			if len(rec.requests) != 6 || second.Outcome != OutcomeAlertRotated {
				t.Fatal("rotation omitted old close/new firing", len(rec.requests))
			}
			old, err := repo.GetAlert(t.Context(), "tenant", first.AlertIDs[0])
			if err != nil || old.Alert.Status != domain.AlertStatusClosed {
				t.Fatal(err)
			}
			terminal := storetest.Event("tenant", "terminal", "fp", "critical")
			terminal.Evaluations[0].Action = action
			final := persistAndProcess(t, repo, p, terminal)
			if len(rec.requests) != 8 || final.EventState != domain.EventProcessStateAccepted {
				t.Fatal("source terminal lost action", len(rec.requests))
			}
			for _, q := range rec.requests {
				if q.Action == "firing" && q.Cause.ID == terminal.EventID {
					t.Fatal("terminal regenerated firing")
				}
			}
		})
	}
}
