// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package suppressioncheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
)

type memoryDocs struct {
	mu         sync.Mutex
	rows       map[string]json.RawMessage
	versions   map[string]int
	failFinish bool
	failStart  bool
}

func newDocs() *memoryDocs {
	return &memoryDocs{rows: map[string]json.RawMessage{}, versions: map[string]int{}}
}

func (d *memoryDocs) Get(ctx context.Context, kind, key string) (json.RawMessage, string, error) {
	if ctx.Err() != nil {
		return nil, "", ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.rows[kind+key]
	if !ok {
		return nil, "", policy.ErrNotFound
	}
	return slices.Clone(b), fmt.Sprint(d.versions[kind+key]), nil
}

func (d *memoryDocs) Put(ctx context.Context, kind, key, expected string, b json.RawMessage) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var r Request
	if json.Unmarshal(b, &r) != nil {
		return policy.ErrInvalid
	}
	if (d.failStart && r.State == "pending") || (d.failFinish && r.State == "completed") {
		return errors.New("storage failed")
	}
	key = kind + key
	n := d.versions[key]
	if (n == 0 && expected != "") || (n > 0 && expected != fmt.Sprint(n)) {
		return policy.ErrConflict
	}
	d.rows[key] = slices.Clone(b)
	d.versions[key]++
	return nil
}

func (d *memoryDocs) List(ctx context.Context, kind, prefix, after string, limit int) ([]json.RawMessage, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	keys := []string{}
	for k := range d.rows {
		if strings.HasPrefix(k, kind+prefix) && k > kind+after {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	out := []json.RawMessage{}
	for _, k := range keys[:min(len(keys), limit)] {
		out = append(out, slices.Clone(d.rows[k]))
	}
	return out, nil
}

func (d *memoryDocs) CountSuppressionCheckRequests(ctx context.Context, prefix string, limit int) (int, error) {
	rows, e := d.ListSuppressionCheckRequests(ctx, prefix, "", limit)
	return len(rows), e
}

func (d *memoryDocs) ListSuppressionCheckRequests(ctx context.Context, prefix, after string, limit int) ([]json.RawMessage, error) {
	rows, e := d.List(ctx, "suppression_requests", prefix, after, 10000)
	if e != nil {
		return nil, e
	}
	out := []json.RawMessage{}
	for _, raw := range rows {
		var r Request
		if json.Unmarshal(raw, &r) != nil {
			return nil, policy.ErrInvalid
		}
		if r.State == "pending" {
			out = append(out, raw)
		}
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

type testLease struct {
	mu         sync.Mutex
	busy       bool
	releaseErr error
}

func (l *testLease) Acquire(ctx context.Context, _ string) (scheduler.Lease, error) {
	if ctx.Err() != nil {
		return scheduler.Lease{}, ctx.Err()
	}
	if l.busy || !l.mu.TryLock() {
		return scheduler.Lease{}, scheduler.ErrLockBusy
	}
	return scheduler.Lease{}, nil
}

func (l *testLease) Renew(context.Context, scheduler.Lease) error { return nil }

func (l *testLease) Release(ctx context.Context, _ scheduler.Lease) error {
	l.mu.Unlock()
	return errors.Join(ctx.Err(), l.releaseErr)
}

func TestPersistentRequestReplayAndUncertainResume(t *testing.T) {
	command, w, a := fixture(t, "clip")
	a.Alert.Status = domain.AlertStatusClosed
	a.Alert.EndAt = &a.Alert.UpdateAt
	a.Alert.EndType = domain.AlertEndTypeUser
	e := makeEngine(t, w, a, nil, nil)
	d := newDocs()
	j, err := NewJournal(d)
	if err != nil {
		t.Fatal(err)
	}
	ctl, err := NewController(j, &testLease{}, e)
	if err != nil {
		t.Fatal(err)
	}
	r, err := ctl.Request(t.Context(), command)
	if err != nil || r.State != "pending" {
		t.Fatal(r, err)
	}
	same, err := ctl.Request(t.Context(), command)
	if err != nil || !reflect.DeepEqual(r, same) {
		t.Fatal("duplicate changed command", err)
	}
	changed := command
	changed.Reason = "changed"
	if _, err := ctl.Request(t.Context(), changed); !errors.Is(err, policy.ErrConflict) {
		t.Fatal(err)
	}
	d.failFinish = true
	if err := ctl.Execute(t.Context(), r); err == nil {
		t.Fatal("finish failure swallowed")
	}
	pending, err := j.Get(t.Context(), command.TenantID, command.Kind, command.WindowID, r.ID)
	if err != nil || pending.Request.State != "pending" || pending.Request.StartedAt == nil {
		t.Fatal(pending, err)
	}
	d.failFinish = false
	j, err = NewJournal(d)
	if err != nil {
		t.Fatal(err)
	}
	ctl, err = NewController(j, &testLease{}, e)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctl.Execute(t.Context(), r); err != nil {
		t.Fatal(err)
	}
	done, err := ctl.Request(t.Context(), command)
	if err != nil || done.State != "completed" || !done.PreviousUnconfirmed || done.Result.Outcome != "absent" || w.deletes != 1 {
		t.Fatal(done, err)
	}
	before := w.reads
	if err := ctl.Execute(t.Context(), r); err != nil || w.reads != before {
		t.Fatal("final reexecuted", err)
	}
	if _, err := j.Get(t.Context(), "other", command.Kind, command.WindowID, r.ID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal(err)
	}
	if p, err := j.List(t.Context(), command.TenantID, command.Kind, command.WindowID, "", 1); err != nil || len(p.Items) != 1 || p.Next == "" {
		t.Fatal(p, err)
	}
	if p, err := j.Work(t.Context(), "", 16); err != nil || len(p.Items) != 0 {
		t.Fatal("completed still in work", p, err)
	}
}

func TestRequestBusyCancellationAndScopeRemainSafe(t *testing.T) {
	command, w, a := fixture(t, "clip")
	d := newDocs()
	j, _ := NewJournal(d)
	lock := &testLease{}
	engine := makeEngine(t, w, a, nil, nil)
	ctl, _ := NewController(j, lock, engine)
	r, err := ctl.Request(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	lock.busy = true
	if err := ctl.Execute(t.Context(), r); !errors.Is(err, scheduler.ErrLockBusy) || w.reads != 0 {
		t.Fatal(err)
	}
	lock.busy = false
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ctl.Execute(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	command.ExpectedEpoch = "new"
	command.OperationID = "stale"
	stale, err := ctl.Request(t.Context(), command)
	if err != nil {
		t.Fatal(err)
	}
	if err := ctl.Execute(t.Context(), stale); err != nil {
		t.Fatal(err)
	}
	stored, err := j.Get(t.Context(), command.TenantID, command.Kind, command.WindowID, stale.ID)
	if err != nil || stored.Request.State != "superseded" || w.deletes != 0 {
		t.Fatal(stored, err)
	}
}

func TestRequestConcurrentDedupeAndCapacity(t *testing.T) {
	command, w, a := fixture(t, "clip")
	d := newDocs()
	j, _ := NewJournal(d)
	ctl, _ := NewController(j, &testLease{}, makeEngine(t, w, a, nil, nil))
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if _, err := ctl.Request(t.Context(), command); err != nil && !CanDefer(err) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if rows, err := j.List(t.Context(), command.TenantID, command.Kind, command.WindowID, "", 16); err != nil || len(rows.Items) != 1 {
		t.Fatal("dedupe lost", rows, err)
	}
	for n := 1; n < 1024; n++ {
		c := command
		c.OperationID = fmt.Sprint(n)
		r := Request{ID: c.id(), Command: c, State: "pending", CreatedAt: time.Now().UTC()}
		if err := j.save(t.Context(), r, ""); err != nil {
			t.Fatal(err)
		}
	}
	fresh := command
	fresh.OperationID = "full"
	if _, err := ctl.Request(t.Context(), fresh); !errors.Is(err, policy.ErrPreviewCapacity) {
		t.Fatal("capacity ignored", err)
	}
	if _, err := ctl.Request(t.Context(), command); err != nil {
		t.Fatal("full queue rejected same command", err)
	}
}

func TestFinishNormalizesWallClockAndRejectsAbsentTimestamp(t *testing.T) {
	command, _, _ := fixture(t, "clip")
	j, _ := NewJournal(newDocs())
	row, err := j.Enqueue(t.Context(), command, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	row, err = j.Start(t.Context(), row, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.Finish(t.Context(), row, Check{Outcome: "absent", Reason: "window_missing"}); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("missing check time fabricated", err)
	}
	at := time.Now()
	done, err := j.Finish(t.Context(), row, Check{CheckedAt: at, Outcome: "absent", Reason: "window_missing"})
	if err != nil || done.Request.State != "completed" || !done.Request.Result.CheckedAt.Equal(at) || done.Request.Result.CheckedAt.Location() != time.UTC {
		t.Fatal("successful persisted time reported conflicting", done, err)
	}
}
