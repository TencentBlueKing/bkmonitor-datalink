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
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
)

func (d *memoryDocuments) ListMergeRequests(ctx context.Context, p, after string, limit int) ([]json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	keys := []string{}
	for key, raw := range d.data {
		if strings.HasPrefix(key, "merge_requests/"+p) && strings.TrimPrefix(key, "merge_requests/") > after {
			var r RetryRequest
			if json.Unmarshal(raw, &r) != nil {
				return nil, policy.ErrInvalid
			}
			if r.State == "pending" {
				keys = append(keys, key)
			}
		}
	}
	slices.Sort(keys)
	out := []json.RawMessage{}
	for _, key := range keys[:min(limit, len(keys))] {
		out = append(out, slices.Clone(d.data[key]))
	}
	return out, nil
}

func (d *memoryDocuments) CountMergeRequests(ctx context.Context, p string, limit int) (int, error) {
	rows, e := d.ListMergeRequests(ctx, p, "", limit)
	return len(rows), e
}

type retryTestLocker struct {
	gate       chan struct{}
	releaseErr error
}

func newRetryTestLocker() *retryTestLocker { return &retryTestLocker{gate: make(chan struct{}, 1)} }

func (l *retryTestLocker) Acquire(ctx context.Context, _ string) (scheduler.Lease, error) {
	select {
	case l.gate <- struct{}{}:
		return scheduler.Lease{}, nil
	case <-ctx.Done():
		return scheduler.Lease{}, ctx.Err()
	}
}

func (l *retryTestLocker) Renew(context.Context, scheduler.Lease) error { return nil }

func (l *retryTestLocker) Release(context.Context, scheduler.Lease) error {
	<-l.gate
	return l.releaseErr
}

type retryStepFunc func(context.Context, string, string, time.Time) error

func (f retryStepFunc) StepDecision(c context.Context, t, i string, at time.Time) error {
	return f(c, t, i, at)
}

func (f retryStepFunc) CheckRelation(c context.Context, t, i string, at time.Time) error {
	return f(c, t, i, at)
}

func retryWindow(ctx context.Context, _, _ string, run func(context.Context) error) error {
	return run(ctx)
}

func retryCommand(t *testing.T, j *Journal, kind string, d Decision, op string) RetryCommand {
	t.Helper()
	point, e := j.ReadControlPoint(t.Context(), d.TenantID, kind, d.ID)
	if e != nil {
		t.Fatal(e)
	}
	return RetryCommand{TenantID: d.TenantID, Kind: kind, TargetID: d.ID, ExpectedToken: point.Token, OperationID: op, OperatorID: "tester", Reason: "接续原步骤"}
}

func retryController(t *testing.T, j *Journal) *RetryController {
	t.Helper()
	c, e := NewRetryController(j, newRetryTestLocker())
	if e != nil {
		t.Fatal(e)
	}
	return c
}

func requestRetry(t *testing.T, c *RetryController, command RetryCommand) RetryRequest {
	t.Helper()
	r, e := c.Request(t.Context(), command)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func readRetry(t *testing.T, j *Journal, r RetryRequest) RetryRequest {
	t.Helper()
	s, e := j.GetRetry(t.Context(), r.Command.TenantID, r.Command.Kind, r.Command.TargetID, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	return s.Request
}

func TestRetryUsesOriginalDecisionAndDoesNotRepeatCompletedCommand(t *testing.T) {
	f := newExecutorFixture(t)
	startExecutor(t, f)
	c := retryController(t, f.journal)
	command := retryCommand(t, f.journal, "decisions", f.decision, "first")
	request := requestRetry(t, c, command)
	if e := c.Execute(t.Context(), request, retryWindow, f.engine); e != nil {
		t.Fatal(e)
	}
	result := readRetry(t, f.journal, request)
	if result.State != "completed" || result.Result.Outcome != "advanced" || result.Result.Before.Phase != "capturing" || result.Result.After.Phase != "prepared" {
		t.Fatal(result)
	}
	prepared, e := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = c.Request(t.Context(), command); e != nil {
		t.Fatal(e)
	}
	if e = c.Execute(t.Context(), request, retryWindow, retryStepFunc(func(context.Context, string, string, time.Time) error {
		t.Fatal("completed command executed")
		return nil
	})); e != nil {
		t.Fatal(e)
	}
	again, e := f.journal.Get(t.Context(), f.decision.TenantID, f.decision.ID)
	if e != nil || !reflect.DeepEqual(prepared, again) || len(f.action.inputs) != 0 {
		t.Fatal("frozen facts or action gate changed", e)
	}
	changed := command
	changed.Reason = "another intent"
	if _, e = c.Request(t.Context(), changed); !errors.Is(e, policy.ErrConflict) {
		t.Fatal(e)
	}
}

func TestRetryRejectsAutomaticProgressAndPreservesUncertainAttempt(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprint(uncertain), func(t *testing.T) {
			f := newExecutorFixture(t)
			startExecutor(t, f)
			c := retryController(t, f.journal)
			r := requestRetry(t, c, retryCommand(t, f.journal, "decisions", f.decision, "check"))
			if uncertain {
				err := c.Execute(t.Context(), r, retryWindow, retryStepFunc(func(ctx context.Context, tenant, id string, at time.Time) error {
					if e := f.engine.StepDecision(ctx, tenant, id, at); e != nil {
						return e
					}
					f.docs.mu.Lock()
					f.docs.failKind = "merge_requests"
					f.docs.mu.Unlock()
					return nil
				}))
				if err == nil {
					t.Fatal("missing final persistence failure")
				}
				if got := readRetry(t, f.journal, r); got.State != "pending" || got.StartedAt == nil {
					t.Fatal(got)
				}
			} else if e := f.engine.StepDecision(t.Context(), r.Command.TenantID, r.Command.TargetID, time.Now().UTC()); e != nil {
				t.Fatal(e)
			}
			if e := c.Execute(t.Context(), r, retryWindow, retryStepFunc(func(context.Context, string, string, time.Time) error { t.Fatal("old token executed"); return nil })); e != nil {
				t.Fatal(e)
			}
			got := readRetry(t, f.journal, r)
			if got.State != "superseded" || got.PreviousUnconfirmed != uncertain || got.Result.StepAttempted {
				t.Fatal(got)
			}
		})
	}
}

func TestRetryRecordsWindowReleaseFailureWithoutLosingConfirmedProgress(t *testing.T) {
	f := newExecutorFixture(t)
	startExecutor(t, f)
	c := retryController(t, f.journal)
	r := requestRetry(t, c, retryCommand(t, f.journal, "decisions", f.decision, "release-failure"))
	fail := errors.New("lease token lost")
	err := c.Execute(t.Context(), r, func(ctx context.Context, _, _ string, fn func(context.Context) error) error {
		return errors.Join(fn(ctx), fail)
	}, f.engine)
	got := readRetry(t, f.journal, r)
	if !errors.Is(err, fail) || got.State != "failed" || !got.Result.StepAttempted || got.Result.After == nil || got.Result.Before.Token == got.Result.After.Token {
		t.Fatal(got, err)
	}
}

func TestRetryCancellationBusyAndInvalidResultKeepPending(t *testing.T) {
	for _, kind := range []string{"cancel", "busy", "busy-and-release"} {
		t.Run(kind, func(t *testing.T) {
			f := newExecutorFixture(t)
			startExecutor(t, f)
			c := retryController(t, f.journal)
			r := requestRetry(t, c, retryCommand(t, f.journal, "decisions", f.decision, "cancel"))
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			err := c.Execute(ctx, r, func(context.Context, string, string, func(context.Context) error) error {
				if kind == "cancel" {
					cancel()
					return ctx.Err()
				}
				if kind == "busy-and-release" {
					return errors.Join(scheduler.ErrLockBusy, errors.New("release failed"))
				}
				return scheduler.ErrLockBusy
			}, f.engine)
			got := readRetry(t, f.journal, r)
			if err == nil {
				t.Fatal("missing error")
			}
			if kind == "busy-and-release" {
				if got.State != "failed" || RetryCanDefer(err) {
					t.Fatal(got, err)
				}
			} else if got.State != "pending" || got.StartedAt == nil {
				t.Fatal(got)
			}
		})
	}
}

func TestRetryCompletedFailedBusinessDecisionDoesNotRejudge(t *testing.T) {
	j, _ := newJournal(t)
	d := decisionFixture(t)
	d.Outcome = "failed"
	d.FrozenAt = d.Deadline
	s, e := j.Claim(t.Context(), d)
	if e != nil {
		t.Fatal(e)
	}
	for s.Decision.Progress.MemberOffset < len(d.WaitMemberIDs) {
		s, e = j.AdvanceMembers(t.Context(), s, s.Decision.Progress.MemberOffset+1, d.FrozenAt)
		if e != nil {
			t.Fatal(e)
		}
	}
	s, e = j.Complete(t.Context(), s, nil, d.FrozenAt)
	if e != nil {
		t.Fatal(e)
	}
	_, e = j.MarkWindowFinished(t.Context(), s, d.FrozenAt)
	if e != nil {
		t.Fatal(e)
	}
	c := retryController(t, j)
	r := requestRetry(t, c, retryCommand(t, j, "decisions", d, "done"))
	e = c.Execute(t.Context(), r, retryWindow, retryStepFunc(func(context.Context, string, string, time.Time) error {
		t.Fatal("business failure was rejudged")
		return nil
	}))
	got := readRetry(t, j, r)
	if e != nil || got.Result.Reason != "already_complete" || got.Result.StepAttempted || got.Result.After.Outcome != "failed" {
		t.Fatal(got, e)
	}
}

func TestRetryRelationManualCloseOnlyUnlinks(t *testing.T) {
	f := newExecutorFixture(t)
	startExecutor(t, f)
	done := driveExecutor(t, f)
	parent, e := f.repo.GetAlert(t.Context(), f.decision.TenantID, done.Decision.Progress.ParentAlertID)
	if e != nil {
		t.Fatal(e)
	}
	f.parent = parent
	closeRelationParent(t, f.relationFixture)
	actions := len(f.action.inputs)
	c := retryController(t, f.journal)
	r := requestRetry(t, c, retryCommand(t, f.journal, "relations", f.decision, "parent-closed"))
	if e = c.Execute(t.Context(), r, retryWindow, f.engine); e != nil {
		t.Fatal(e)
	}
	got := readRetry(t, f.journal, r)
	if got.Result.After.Phase != "ended" || !got.Result.After.Complete || len(f.action.inputs) != actions {
		t.Fatal(got)
	}
	for _, id := range f.decision.MemberIDs {
		child, e := f.repo.GetAlert(t.Context(), f.decision.TenantID, id)
		if e != nil || child.Alert.Status != domain.AlertStatusActive || child.Alert.Merge.Blocking() || child.Alert.Admission.AdmittedAt != nil {
			t.Fatal("child changed lifecycle or admission", e)
		}
	}
}

func TestRetryConcurrentDuplicateCreatesOneRequestAndExecutesOnce(t *testing.T) {
	f := newExecutorFixture(t)
	startExecutor(t, f)
	c := retryController(t, f.journal)
	command := retryCommand(t, f.journal, "decisions", f.decision, "concurrent")
	var wg sync.WaitGroup
	var succeeded atomic.Int64
	for range 16 {
		wg.Go(func() {
			_, e := c.Request(t.Context(), command)
			if e == nil {
				succeeded.Add(1)
			} else if !RetryCanDefer(e) {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if succeeded.Load() == 0 {
		t.Fatal("no request accepted")
	}
	r := requestRetry(t, c, command)
	var calls atomic.Int64
	step := retryStepFunc(func(context.Context, string, string, time.Time) error { calls.Add(1); return nil })
	for range 16 {
		wg.Go(func() {
			if e := c.Execute(t.Context(), r, retryWindow, step); e != nil && !RetryCanDefer(e) {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatal(calls.Load())
	}
	page, e := f.journal.ListRetries(t.Context(), command.TenantID, command.Kind, command.TargetID, "", 16)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
}

func TestRetryPendingLimitAndScopeValidation(t *testing.T) {
	f := newExecutorFixture(t)
	startExecutor(t, f)
	c := retryController(t, f.journal)
	base := retryCommand(t, f.journal, "decisions", f.decision, "first")
	first := requestRetry(t, c, base)
	for n := 1; n < 1024; n++ {
		command := base
		command.OperationID = fmt.Sprintf("seed-%d", n)
		r := RetryRequest{ID: command.id(), Command: command, WindowID: f.decision.WindowID, State: "pending", CreatedAt: time.Now().UTC()}
		if e := f.journal.saveRetry(t.Context(), r, ""); e != nil {
			t.Fatal(e)
		}
	}
	other := base
	other.OperationID = "over-limit"
	if _, e := c.Request(t.Context(), other); !errors.Is(e, policy.ErrPreviewCapacity) {
		t.Fatal("pending limit bypassed", e)
	}
	replay, e := c.Request(t.Context(), base)
	if e != nil || !reflect.DeepEqual(first, replay) {
		t.Fatal("duplicate rejected at capacity", e)
	}
	page, e := f.journal.ListRetries(t.Context(), base.TenantID, base.Kind, base.TargetID, "", 4)
	if e != nil || len(page.Items) != 4 || page.Next == "" {
		t.Fatal(page, e)
	}
	if _, e = f.journal.ListRetries(t.Context(), "other-tenant", base.Kind, base.TargetID, page.Next, 4); !errors.Is(e, policy.ErrInvalid) {
		t.Fatal("foreign cursor accepted", e)
	}
	changed := first
	changed.Command.ExpectedToken = strings.Repeat("a", 64)
	if e = c.Execute(t.Context(), changed, retryWindow, f.engine); !errors.Is(e, policy.ErrConflict) {
		t.Fatal("queued intent replaced persisted command", e)
	}
	if got := readRetry(t, f.journal, first); got.StartedAt != nil {
		t.Fatal("invalid queued command began")
	}
}

func TestRetryUnreadableTargetCannotExecuteAndUnsafeResultCannotCommit(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(fmt.Sprint(missing), func(t *testing.T) {
			f := newExecutorFixture(t)
			startExecutor(t, f)
			c := retryController(t, f.journal)
			r := requestRetry(t, c, retryCommand(t, f.journal, "decisions", f.decision, "target-read"))
			key, _ := decisionKey(f.decision.TenantID, f.decision.ID)
			f.docs.mu.Lock()
			if missing {
				delete(f.docs.data, "merge_decisions/"+key)
			} else {
				f.docs.data["merge_decisions/"+key] = json.RawMessage(`{"bk_tenant_id":"other"}`)
			}
			f.docs.mu.Unlock()
			if e := c.Execute(t.Context(), r, retryWindow, retryStepFunc(func(context.Context, string, string, time.Time) error {
				t.Fatal("unreadable target executed")
				return nil
			})); e == nil {
				t.Fatal("missing target failure")
			}
			got := readRetry(t, f.journal, r)
			if got.State != "failed" || got.Result.Before != nil || got.Result.StepAttempted {
				t.Fatal(got)
			}
		})
	}
	f := newExecutorFixture(t)
	startExecutor(t, f)
	c := retryController(t, f.journal)
	r := requestRetry(t, c, retryCommand(t, f.journal, "decisions", f.decision, "invalid-result"))
	s, e := f.journal.GetRetry(t.Context(), r.Command.TenantID, r.Command.Kind, r.Command.TargetID, r.ID)
	if e != nil {
		t.Fatal(e)
	}
	s, e = f.journal.StartRetry(t.Context(), s, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	before, e := f.journal.ReadControlPoint(t.Context(), r.Command.TenantID, r.Command.Kind, r.Command.TargetID)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.journal.FinishRetry(t.Context(), s, RetryResult{Outcome: "advanced", Reason: "progressed", CheckedAt: time.Now(), StepAttempted: true, Before: &before, After: &before}); !errors.Is(e, policy.ErrInvalid) {
		t.Fatal("invented progress committed", e)
	}
	if _, e = f.journal.FinishRetry(t.Context(), s, RetryResult{Outcome: "unchanged", Reason: "no_progress", StepAttempted: true, Before: &before, After: &before}); !errors.Is(e, policy.ErrInvalid) {
		t.Fatal("zero time fabricated", e)
	}
}

func TestRetryContinuesFailedWindowReleaseWithoutChangingBusinessVerdict(t *testing.T) {
	f := newExecutorFixture(t)
	d := f.decision
	d.Outcome = "failed"
	d.FrozenAt = d.Deadline
	d.MemberIDs = nil
	if _, e := f.journal.Claim(t.Context(), d); e != nil {
		t.Fatal(e)
	}
	c := retryController(t, f.journal)
	r := requestRetry(t, c, retryCommand(t, f.journal, "decisions", d, "release-original"))
	if e := c.Execute(t.Context(), r, retryWindow, f.engine); e != nil {
		t.Fatal(e)
	}
	got := readRetry(t, f.journal, r)
	if got.Result.Outcome != "advanced" || got.Result.After.Outcome != "failed" || got.Result.After.MemberOffset != 1 || len(f.action.inputs) != 1 {
		t.Fatal("release did not continue original outcome", got)
	}
	if e := c.Execute(t.Context(), r, retryWindow, f.engine); e != nil || len(f.action.inputs) != 1 {
		t.Fatal("same request repeated release", e)
	}
}
