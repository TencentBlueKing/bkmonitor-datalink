// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package suppressioncleanup

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
	var r Record
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

func terminalCause() Cause {
	return Cause{TenantID: "tenant", SourceID: "source", Fingerprint: "fp", Trigger: "alert_terminal", AlertID: "alert", Revision: 2, Status: domain.AlertStatusClosed}
}

func success(context.Context) (Result, error) {
	return Result{Clip: Confirmed([]Window{{ID: strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64), Epoch: "first"}, {ID: strings.Repeat("a", 64) + ":" + strings.Repeat("c", 64), Epoch: "second"}}), Aggregation: Confirmed([]Window{{ID: strings.Repeat("d", 64), Epoch: "agg-first"}})}, nil
}

func journal(t *testing.T, d Documents) *Journal {
	t.Helper()
	j, e := NewJournal(d, time.Now)
	if e != nil {
		t.Fatal(e)
	}
	return j
}

func TestCleanupPersistsBeforeEffectAndReplaysExactCause(t *testing.T) {
	d := newDocs()
	j := journal(t, d)
	c := terminalCause()
	calls := 0
	run := func(ctx context.Context) (Result, error) {
		calls++
		r, _, e := j.Get(ctx, c.TenantID, c.ID())
		if e != nil || r.State != "pending" || r.Result != nil {
			t.Fatal("effect without intent", e)
		}
		return success(ctx)
	}
	if e := j.Run(t.Context(), c, run); e != nil {
		t.Fatal(e)
	}
	first, _, e := j.Get(t.Context(), c.TenantID, c.ID())
	if e != nil || first.State != "completed" || first.PreviousUnconfirmed || *first.Result.Clip.Removed != 2 {
		t.Fatal(first, e)
	}
	if e := journal(t, d).Run(t.Context(), c, run); e != nil || calls != 1 {
		t.Fatal("replay executed", calls, e)
	}
	got, _, _ := j.Get(t.Context(), c.TenantID, c.ID())
	if !reflect.DeepEqual(first, got) {
		t.Fatal("replay rewrote record")
	}
	c.Fingerprint = "changed"
	if e := j.Run(t.Context(), c, run); !errors.Is(e, policy.ErrConflict) {
		t.Fatal("identity reused with different scope", e)
	}
	if _, _, e := j.Get(t.Context(), "other", c.ID()); !errors.Is(e, policy.ErrNotFound) {
		t.Fatal(e)
	}
}

func TestCleanupFailedPersistenceAndCancellationKeepUncertainty(t *testing.T) {
	for _, mode := range []string{"before", "after", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			d := newDocs()
			j := journal(t, d)
			c := terminalCause()
			calls := 0
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			d.failStart = mode == "before"
			d.failFinish = mode == "after"
			e := j.Run(ctx, c, func(ctx context.Context) (Result, error) {
				calls++
				if mode == "cancel" {
					cancel()
				}
				return success(ctx)
			})
			if e == nil {
				t.Fatal("failure ignored")
			}
			if mode == "before" && calls != 0 {
				t.Fatal("cleared without durable intent")
			}
			if mode != "before" {
				r, _, e := j.Get(t.Context(), c.TenantID, c.ID())
				if e != nil || r.State != "pending" || r.Result != nil {
					t.Fatal("uncertain effect called completed", r, e)
				}
			}
			d.failStart = false
			d.failFinish = false
			if e := journal(t, d).Run(t.Context(), c, func(context.Context) (Result, error) {
				return Result{Clip: Confirmed(nil), Aggregation: Confirmed(nil)}, nil
			}); e != nil {
				t.Fatal(e)
			}
			r, _, e := j.Get(t.Context(), c.TenantID, c.ID())
			if e != nil || r.PreviousUnconfirmed != (mode != "before") {
				t.Fatal("lost uncertain earlier attempt", r, e)
			}
		})
	}
}

func TestCleanupUnavailableIsNotZeroAndEventHasNoAggregation(t *testing.T) {
	d := newDocs()
	j := journal(t, d)
	c := Cause{TenantID: "tenant", SourceID: "source", Fingerprint: "fp", Trigger: "event_terminal", EventID: "resolved"}
	if e := j.Run(t.Context(), c, func(context.Context) (Result, error) {
		return Result{Clip: Outcome{State: "unavailable"}, Aggregation: Outcome{State: "not_applicable"}}, nil
	}); e != nil {
		t.Fatal(e)
	}
	r, _, e := j.Get(t.Context(), c.TenantID, c.ID())
	if e != nil || r.Result.Clip.Removed != nil || r.Result.Clip.State != "unavailable" {
		t.Fatal("failure became zero", r, e)
	}
	for _, bad := range []Cause{{}, {TenantID: "tenant", SourceID: "source", Fingerprint: "fp", Trigger: "alert_terminal", AlertID: "active", Revision: 1, Status: domain.AlertStatusActive}} {
		if e := j.Run(t.Context(), bad, success); !errors.Is(e, policy.ErrInvalid) {
			t.Fatal(e)
		}
	}
}

func TestCleanupConcurrentIndependentRecordsAndScopedPagination(t *testing.T) {
	d := newDocs()
	j := journal(t, d)
	var wg sync.WaitGroup
	for n := range 20 {
		wg.Go(func() {
			c := terminalCause()
			c.AlertID = fmt.Sprint(n)
			if n%2 == 0 {
				c.TenantID = "other"
			}
			if e := j.Run(t.Context(), c, success); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	after := ""
	count := 0
	for range 6 {
		p, e := j.List(t.Context(), "tenant", after, 3)
		if e != nil {
			t.Fatal(e)
		}
		for _, r := range p.Items {
			if r.Cause.TenantID != "tenant" {
				t.Fatal("tenant leak")
			}
		}
		count += len(p.Items)
		if p.Next == "" {
			break
		}
		if p.Next <= after {
			t.Fatal("cursor regressed")
		}
		after = p.Next
	}
	if count != 10 {
		t.Fatal("lost rows", count)
	}
}

func TestCleanupUnencodableClockStopsBeforeEffect(t *testing.T) {
	j, err := NewJournal(newDocs(), func() time.Time { return time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) })
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := j.Run(t.Context(), terminalCause(), func(context.Context) (Result, error) { calls++; return Result{}, nil }); !errors.Is(err, policy.ErrInvalid) || calls != 0 {
		t.Fatal("invalid record reached effect", err, calls)
	}
}

func TestCleanupCompleteDetailBudgetAndWindowFilter(t *testing.T) {
	c := terminalCause()
	at := time.Now()
	clip := []Window{}
	aggregation := []Window{}
	for i := range 512 {
		clip = append(clip, Window{ID: strings.Repeat("a", 64) + ":" + fmt.Sprintf("%064x", i), Epoch: strings.Repeat("\x01", 160)})
		aggregation = append(aggregation, Window{ID: fmt.Sprintf("%064x", i), Epoch: strings.Repeat("\x01", 160)})
	}
	r := Record{ID: c.ID(), Cause: c, State: "completed", StartedAt: at, FinishedAt: &at, Result: &Result{Clip: Confirmed(clip), Aggregation: Confirmed(aggregation)}}
	raw, err := json.Marshal(r)
	if err != nil || r.Validate() != nil || len(raw) > MaxRecordBytes {
		t.Fatal("document budget insufficient for complete bounded detail", len(raw), err)
	}
	if !WindowMatches(r, "clip", clip[17].ID, clip[17].Epoch) || WindowMatches(r, "clip", clip[17].ID, "other") || WindowMatches(r, "aggregation", clip[17].ID, "") {
		t.Fatal("window filter leaked")
	}
	clip[0].Epoch = "changed"
	if r.Result.Clip.Windows[0].Epoch == "changed" {
		t.Fatal("confirmed result aliased input")
	}
	r.Result.Clip.Windows[0].Epoch = ""
	if r.Validate() == nil {
		t.Fatal("unknown epoch without missing flag accepted")
	}
}
