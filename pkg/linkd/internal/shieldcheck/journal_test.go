// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package shieldcheck

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

	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store"
)

type memoryDocs struct {
	mu       sync.Mutex
	rows     map[string]json.RawMessage
	versions map[string]int
	full     bool
}

func newDocs() *memoryDocs {
	return &memoryDocs{rows: map[string]json.RawMessage{}, versions: map[string]int{}}
}

func (d *memoryDocs) Get(ctx context.Context, kind, key string) (json.RawMessage, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key = kind + "/" + key
	b, ok := d.rows[key]
	if !ok {
		return nil, "0", policy.ErrNotFound
	}
	return slices.Clone(b), fmt.Sprint(d.versions[key]), nil
}

func (d *memoryDocs) Put(ctx context.Context, kind, key, version string, b json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	key = kind + "/" + key
	n := d.versions[key]
	if (n == 0 && version != "") || (n > 0 && version != fmt.Sprint(n)) {
		return policy.ErrConflict
	}
	d.rows[key] = slices.Clone(b)
	d.versions[key] = n + 1
	return nil
}

func (d *memoryDocs) List(ctx context.Context, kind, prefix, after string, limit int) ([]json.RawMessage, error) {
	return d.page(ctx, kind, prefix, after, limit, false)
}

func (d *memoryDocs) ListShieldCheckRequests(ctx context.Context, prefix, after string, limit int) ([]json.RawMessage, error) {
	return d.page(ctx, "shield_requests", prefix, after, limit, true)
}

func (d *memoryDocs) CountShieldCheckRequests(ctx context.Context, prefix string, limit int) (int, error) {
	if d.full {
		return limit, nil
	}
	r, err := d.page(ctx, "shield_requests", prefix, "", limit, true)
	return len(r), err
}

func (d *memoryDocs) page(ctx context.Context, kind, prefix, after string, limit int, work bool) ([]json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	keys := []string{}
	for key, b := range d.rows {
		if !strings.HasPrefix(key, kind+"/") {
			continue
		}
		id := strings.TrimPrefix(key, kind+"/")
		if id <= after || !strings.HasPrefix(id, prefix) {
			continue
		}
		var r Request
		if work && (json.Unmarshal(b, &r) != nil || r.State != "pending") {
			continue
		}
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := []json.RawMessage{}
	for _, key := range keys[:min(limit, len(keys))] {
		out = append(out, slices.Clone(d.rows[key]))
	}
	return out, nil
}

func fixtureCommand() Command {
	return Command{TenantID: "tenant", AlertID: "alert", OperationID: "operation", ExpectedRevision: 2, OperatorID: "tester", Reason: "验证屏蔽条件"}
}

func fixtureCheck() Check {
	at := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	return Check{TenantID: "tenant", AlertID: "alert", Trigger: "timer", StartedAt: at, FinishedAt: at.Add(time.Second), Report: lifecycle.ShieldCheckReport{ObservedRevision: 2, ResultRevision: 2, CheckedAt: at, Outcome: "retained", RemainingBindings: 1}}
}

func TestJournalImmutableRequestsScopeAndLatestOrdering(t *testing.T) {
	d := newDocs()
	j, _ := NewJournal(d)
	c := fixtureCommand()
	at := fixtureCheck().StartedAt
	first, err := j.Enqueue(t.Context(), c, at)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := j.Enqueue(t.Context(), c, at.Add(time.Minute))
	if err != nil || !retry.Request.CreatedAt.Equal(at) || retry.Version != first.Version {
		t.Fatal("retry changed command", err)
	}
	changed := c
	changed.Reason = "different"
	if _, err := j.Enqueue(t.Context(), changed, at); !errors.Is(err, policy.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := j.GetRequest(t.Context(), "other", c.AlertID, first.Request.ID); !errors.Is(err, policy.ErrNotFound) {
		t.Fatal("scope leak", err)
	}
	work, err := j.Work(t.Context(), "", 1)
	if err != nil || len(work.Items) != 1 || work.Next == "" {
		t.Fatal(work, err)
	}
	result := fixtureCheck()
	result.Trigger = "request"
	result.RequestID = first.Request.ID
	final, err := j.Finish(t.Context(), first, result)
	if err != nil || final.Request.State != "completed" {
		t.Fatal(final, err)
	}
	if _, err := j.Finish(t.Context(), first, result); !errors.Is(err, policy.ErrConflict) {
		t.Fatal("stale CAS", err)
	}
	retry, err = j.Enqueue(t.Context(), c, at.Add(time.Hour))
	if err != nil || retry.Request.State != "completed" {
		t.Fatal("finished replay", err)
	}
	work, err = j.Work(t.Context(), "", 16)
	if err != nil || len(work.Items) != 0 {
		t.Fatal("terminal in work", err)
	}
	page, err := j.List(t.Context(), c.TenantID, c.AlertID, "", 1)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	if _, err := j.List(t.Context(), "other", c.AlertID, page.Next, 1); !errors.Is(err, policy.ErrInvalid) {
		t.Fatal("cursor scope", err)
	}
	if err := j.SaveLatest(t.Context(), result); err != nil {
		t.Fatal("missing SQL token must be create", err)
	}
	newer := result
	newer.StartedAt = newer.StartedAt.Add(time.Minute)
	newer.FinishedAt = newer.StartedAt.Add(time.Second)
	if err := j.SaveLatest(t.Context(), newer); err != nil {
		t.Fatal(err)
	}
	if err := j.SaveLatest(t.Context(), result); err != nil {
		t.Fatal(err)
	}
	got, err := j.Latest(t.Context(), c.TenantID, c.AlertID)
	if err != nil || !got.StartedAt.Equal(newer.StartedAt) {
		t.Fatal("slow old check replaced latest", err)
	}
	d.full = true
	if _, err := j.Enqueue(t.Context(), c, at); err != nil {
		t.Fatal("full budget blocked retry", err)
	}
	c.OperationID = "new"
	if _, err := j.Enqueue(t.Context(), c, at); !errors.Is(err, policy.ErrPreviewCapacity) {
		t.Fatal(err)
	}
}

func TestJournalRejectsMalformedDiagnosticAndCrossScopePayload(t *testing.T) {
	for _, mutate := range []func(*Check){func(c *Check) { c.Report.Changed = true }, func(c *Check) { c.Report.Outcome = "changed" }, func(c *Check) { c.Report.Outcome = "superseded"; c.ErrorCode = "dependency_failed" }, func(c *Check) { c.FinishedAt = c.StartedAt.Add(-time.Second) }, func(c *Check) { c.ErrorCode = "private stack" }, func(c *Check) { c.Report.ObservedRevision = 0 }, func(c *Check) { c.Report.RemainingBindings = 17 }} {
		c := fixtureCheck()
		mutate(&c)
		if c.Validate() == nil {
			t.Fatalf("invalid diagnostic accepted %+v", c)
		}
	}
	d := newDocs()
	j, _ := NewJournal(d)
	c := fixtureCheck()
	if err := j.SaveLatest(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	c.TenantID = "other"
	b, _ := json.Marshal(c)
	d.rows["shield_checks/"+alertPrefix("tenant", "alert")+"latest"] = b
	if _, err := j.Latest(t.Context(), "tenant", "alert"); !errors.Is(err, policy.ErrAccess) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := j.SaveLatest(ctx, fixtureCheck()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestLatestConcurrentAttemptsNeverReplaceNewerStart(t *testing.T) {
	j, _ := NewJournal(newDocs())
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			c := fixtureCheck()
			c.StartedAt = c.StartedAt.Add(time.Duration(i) * time.Second)
			c.FinishedAt = c.StartedAt.Add(time.Second)
			if err := j.SaveLatest(t.Context(), c); err != nil && !errors.Is(err, policy.ErrConflict) {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	newest := fixtureCheck()
	newest.StartedAt = newest.StartedAt.Add(15 * time.Second)
	newest.FinishedAt = newest.StartedAt.Add(time.Second)
	if err := j.SaveLatest(t.Context(), newest); err != nil {
		t.Fatal(err)
	}
	got, err := j.Latest(t.Context(), "tenant", "alert")
	if err != nil || !got.StartedAt.Equal(newest.StartedAt) {
		t.Fatal(got, err)
	}
}

func TestLatestKeepsReleasedBindingAndCandidateDiagnostics(t *testing.T) {
	c := fixtureCheck()
	c.Trigger = "hint"
	c.Report.Outcome = "changed"
	c.Report.Changed = true
	c.Report.ResultRevision = c.Report.ObservedRevision + 1
	c.Report.RemainingBindings = 0
	ref := store.PolicyReleaseRef{Kind: "shield", ID: "policy", Version: 1, Digest: strings.Repeat("a", 64)}
	c.Report.Decision = &store.ShieldDecision{Severity: "warning", Steps: []store.ShieldStep{{Policy: ref, FromBinding: true, BindingID: strings.Repeat("b", 64), Outcome: "released", ReasonCode: "dependency_main_ended"}, {Policy: ref, Outcome: "not_matched", ReasonCode: "timer_does_not_rebind"}}}
	j, _ := NewJournal(newDocs())
	if err := j.SaveLatest(t.Context(), c); err != nil {
		t.Fatal("real release diagnostic rejected", err)
	}
	got, err := j.Latest(t.Context(), c.TenantID, c.AlertID)
	if err != nil || !reflect.DeepEqual(got, c) {
		t.Fatal("candidate diagnostics lost", err)
	}
	for range 255 {
		c.Report.Decision.Steps = append(c.Report.Decision.Steps, store.ShieldStep{Policy: ref, Outcome: "skipped", ReasonCode: "release_unavailable"})
	}
	c.Report.Outcome = "partial"
	if err := c.Validate(); err != nil {
		t.Fatal("full bounded candidate decision rejected", err)
	}
	c.Report.Decision.Steps = append(c.Report.Decision.Steps, store.ShieldStep{Policy: ref, Outcome: "not_matched"})
	if err := c.Validate(); err == nil {
		t.Fatal("candidate budget exceeded")
	}
}
