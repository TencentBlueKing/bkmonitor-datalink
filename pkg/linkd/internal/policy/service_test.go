// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryDocument struct {
	raw     json.RawMessage
	version int
}

type testDocuments struct {
	mu                    sync.Mutex
	rows                  map[string]memoryDocument
	writes                int
	failBefore, failAfter int
	afterWrite            func(string, string, json.RawMessage)
}

var errInjected = errors.New("injected publication interruption")

func newTestDocuments() *testDocuments { return &testDocuments{rows: map[string]memoryDocument{}} }

func (d *testDocuments) Get(ctx context.Context, collection, key string) (json.RawMessage, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.rows[collection+"/"+key]
	if !ok {
		return nil, "", ErrNotFound
	}
	return bytes.Clone(v.raw), fmt.Sprint(v.version), nil
}

func (d *testDocuments) Put(ctx context.Context, collection, key, expected string, raw json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	d.writes++
	n := d.writes
	if n == d.failBefore {
		d.mu.Unlock()
		return errInjected
	}
	full := collection + "/" + key
	v, exists := d.rows[full]
	if (expected == "" && exists) || (expected != "" && (!exists || expected != fmt.Sprint(v.version))) {
		d.mu.Unlock()
		return ErrConflict
	}
	d.rows[full] = memoryDocument{bytes.Clone(raw), v.version + 1}
	fail := n == d.failAfter
	hook := d.afterWrite
	d.mu.Unlock()
	if hook != nil {
		hook(collection, key, raw)
	}
	if fail {
		return errInjected
	}
	return nil
}

func (d *testDocuments) List(ctx context.Context, collection, prefix, after string, limit int) ([]json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	keys := []string{}
	for key := range d.rows {
		if strings.HasPrefix(key, collection+"/"+prefix) && strings.TrimPrefix(key, collection+"/") > after {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	result := make([]json.RawMessage, 0, len(keys))
	for _, key := range keys {
		result = append(result, bytes.Clone(d.rows[key].raw))
	}
	return result, nil
}

func testSpec(name string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"name":%q,"is_enable":true,"updated_at":"2026-09-30T00:00:00Z","space_code":"bkcc__2","activate_times":[{"period":"everyday","open_clock_time":"00:00:00","close_clock_time":"23:59:59"}],"policy":{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"source"}},"scheme":[{"type":"clip","count":2,"duration":60,"duration_type":"second"}]}`, name))
}

func testRequest() ApplyRequest {
	return ApplyRequest{Scope: Scope{TenantID: "tenant", Kind: Suppression}, SchemaVersion: 1, ID: "p1", ExpectedVersion: 0, OperationID: "op-create", Spec: testSpec("first")}
}

func mustApply(t *testing.T, s *Service, r ApplyRequest) Release {
	t.Helper()
	v, err := s.Apply(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestPublicationVersionsIdentityAndScope(t *testing.T) {
	d := newTestDocuments()
	s := NewService(d)
	r := testRequest()
	first := mustApply(t, s, r)
	if first.Version != 1 || first.Compiled.Digest == "" {
		t.Fatalf("incomplete release: %+v", first)
	}
	again := mustApply(t, s, r)
	if !again.CreatedAt.Equal(first.CreatedAt) || again.RequestDigest != first.RequestDigest {
		t.Fatal("retry changed immutable release")
	}
	next := r
	next.ExpectedVersion = 1
	next.OperationID = "op-edit"
	next.Spec = testSpec("second")
	second := mustApply(t, s, next)
	if second.Version != 2 {
		t.Fatal(second.Version)
	}
	old := mustApply(t, s, r)
	if old.Version != 1 {
		t.Fatal("old operation returned latest release")
	}
	deleted, err := s.Delete(t.Context(), r.Scope, r.ID, 2, "op-delete")
	if err != nil || !deleted.Deleted || deleted.Version != 3 {
		t.Fatalf("delete: %+v %v", deleted, err)
	}
	newer := next
	newer.ExpectedVersion = 3
	newer.OperationID = "op-recreate"
	mustApply(t, s, newer)
	retried, err := s.Delete(t.Context(), r.Scope, r.ID, 2, "op-delete")
	if err != nil || retried.Version != 3 {
		t.Fatalf("old delete: %+v %v", retried, err)
	}
	for _, scope := range []Scope{{TenantID: "other", Kind: Suppression}, {TenantID: r.TenantID, Kind: Shield}} {
		if _, err := s.Get(t.Context(), scope, r.ID); !errors.Is(err, ErrNotFound) {
			t.Fatalf("scope leaked: %v", err)
		}
		rows, err := s.List(t.Context(), scope, "", 10)
		if err != nil || len(rows) != 0 {
			t.Fatalf("scope list leaked: %+v %v", rows, err)
		}
	}
	other := r
	other.TenantID = "other"
	mustApply(t, s, other)
	rows, err := s.List(t.Context(), r.Scope, "", 10)
	if err != nil || len(rows) != 1 || rows[0].Published != 4 {
		t.Fatalf("list: %+v %v", rows, err)
	}
}

func TestPublicationRejectsConflictsAndInvalidRequests(t *testing.T) {
	s := NewService(newTestDocuments())
	r := testRequest()
	mustApply(t, s, r)
	for _, change := range []func(*ApplyRequest){func(r *ApplyRequest) { r.Spec = testSpec("changed") }, func(r *ApplyRequest) { r.ExpectedVersion = 1 }, func(r *ApplyRequest) { r.OperationID = "other" }} {
		bad := r
		change(&bad)
		if _, err := s.Apply(t.Context(), bad); !errors.Is(err, ErrConflict) {
			t.Fatalf("expected conflict: %v", err)
		}
	}
	for _, change := range []func(*ApplyRequest){func(r *ApplyRequest) { r.TenantID = "" }, func(r *ApplyRequest) { r.Kind = "unknown" }, func(r *ApplyRequest) { r.SchemaVersion = 0 }, func(r *ApplyRequest) { r.OperationID = "bad\n" }, func(r *ApplyRequest) { r.ExpectedVersion = 1 << 53 }, func(r *ApplyRequest) { r.Spec = json.RawMessage(`{}`) }} {
		bad := r
		change(&bad)
		if _, err := s.Apply(t.Context(), bad); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid: %v", err)
		}
	}
	if _, err := s.Delete(t.Context(), r.Scope, r.ID, 0, "delete"); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	for _, n := range []int{0, 101} {
		if _, err := s.List(t.Context(), r.Scope, "", n); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
		if _, err := s.RecoverPage(t.Context(), "", n); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := s.Apply(ctx, r); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPublicationEveryWriteFailureCanRetry(t *testing.T) {
	for _, after := range []bool{false, true} {
		for n := 1; n <= 4; n++ {
			t.Run(fmt.Sprintf("after=%t/write=%d", after, n), func(t *testing.T) {
				d := newTestDocuments()
				if after {
					d.failAfter = n
				} else {
					d.failBefore = n
				}
				s := NewService(d)
				r := testRequest()
				if _, err := s.Apply(t.Context(), r); !errors.Is(err, errInjected) {
					t.Fatalf("no injected failure: %v", err)
				}
				result := mustApply(t, s, r)
				record, err := s.Get(t.Context(), r.Scope, r.ID)
				if err != nil || result.Version != 1 || record.Published != 1 || record.Revision != 1 || record.Pending != nil {
					t.Fatalf("retry incomplete: %+v %v", record, err)
				}
				d.mu.Lock()
				count := len(d.rows)
				d.mu.Unlock()
				if count != 3 {
					t.Fatalf("duplicated publication objects: %d", count)
				}
			})
		}
	}
}

func TestPendingPublicationRecoveryAndPagination(t *testing.T) {
	d := newTestDocuments()
	s := NewService(d)
	for i := 0; i < 3; i++ {
		r := testRequest()
		r.ID = fmt.Sprintf("p%d", i)
		d.mu.Lock()
		d.failBefore = d.writes + 3
		d.mu.Unlock()
		if _, err := s.Apply(t.Context(), r); !errors.Is(err, errInjected) {
			t.Fatal(err)
		}
	}
	var cursor string
	for i := 0; i < 3; i++ {
		next, err := s.RecoverPage(t.Context(), cursor, 1)
		if err != nil || next == "" || next <= cursor {
			t.Fatalf("page %s: %v", next, err)
		}
		cursor = next
	}
	next, err := s.RecoverPage(t.Context(), cursor, 1)
	if err != nil || next != "" {
		t.Fatalf("wrap %s %v", next, err)
	}
	for i := 0; i < 3; i++ {
		r, err := s.Get(t.Context(), testRequest().Scope, fmt.Sprintf("p%d", i))
		if err != nil || r.Pending != nil || r.Published != 1 {
			t.Fatalf("unrecovered: %+v %v", r, err)
		}
	}
}

func TestPublicationConcurrentEditsAndSameOperation(t *testing.T) {
	for _, same := range []bool{false, true} {
		t.Run(fmt.Sprint(same), func(t *testing.T) {
			d := newTestDocuments()
			s := NewService(d)
			base := testRequest()
			mustApply(t, s, base)
			start := make(chan struct{})
			var wg sync.WaitGroup
			successes := make(chan Release, 12)
			failures := make(chan error, 12)
			for i := 0; i < 12; i++ {
				wg.Go(func() {
					r := base
					r.ExpectedVersion = 1
					r.OperationID = "edit"
					if !same {
						r.OperationID = fmt.Sprintf("edit-%d", i)
					}
					<-start
					v, err := s.Apply(t.Context(), r)
					if same && errors.Is(err, ErrConflict) {
						v, err = s.Apply(t.Context(), r)
					}
					if err == nil {
						successes <- v
					} else {
						failures <- err
					}
				})
			}
			close(start)
			wg.Wait()
			close(successes)
			close(failures)
			count := 0
			for v := range successes {
				count++
				if v.Version != 2 {
					t.Fatal(v.Version)
				}
			}
			for err := range failures {
				if !errors.Is(err, ErrConflict) {
					t.Fatal(err)
				}
			}
			if (!same && count != 1) || (same && count != 12) {
				t.Fatalf("successes=%d", count)
			}
			record, err := s.Get(t.Context(), base.Scope, base.ID)
			if err != nil || record.Published != 2 || record.Pending != nil {
				t.Fatalf("record %+v %v", record, err)
			}
		})
	}
}

func TestPublicationDoesNotReturnConcurrentlyNewerRelease(t *testing.T) {
	d := newTestDocuments()
	s := NewService(d)
	r := testRequest()
	called := false
	d.afterWrite = func(collection, key string, raw json.RawMessage) {
		if called || collection != "records" {
			return
		}
		called = true
		if _, err := s.Recover(t.Context(), r.Scope, r.ID); err != nil {
			t.Fatal(err)
		}
		next := r
		next.OperationID = "next"
		next.ExpectedVersion = 1
		mustApply(t, s, next)
	}
	release := mustApply(t, s, r)
	if release.Version != 1 {
		t.Fatalf("returned newer version %d", release.Version)
	}
}

func TestPublicationRecoveryIgnoresJSONFormatting(t *testing.T) {
	d := newTestDocuments()
	d.failBefore = 4
	s := NewService(d)
	r := testRequest()
	if _, err := s.Apply(t.Context(), r); !errors.Is(err, errInjected) {
		t.Fatal(err)
	}
	d.mu.Lock()
	key := "releases/" + releaseKey(r.Scope, r.ID, 1)
	v := d.rows[key]
	var formatted bytes.Buffer
	if err := json.Indent(&formatted, v.raw, "", " "); err != nil {
		t.Fatal(err)
	}
	v.raw = formatted.Bytes()
	d.rows[key] = v
	d.mu.Unlock()
	now := time.Now().Add(time.Hour)
	s.now = func() time.Time { return now }
	release, err := s.Recover(t.Context(), r.Scope, r.ID)
	if err != nil || release.CreatedAt.Equal(now) {
		t.Fatalf("recovery %+v %v", release, err)
	}
}
