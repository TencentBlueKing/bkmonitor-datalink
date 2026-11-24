// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"linkd/internal/policy"
)

func TestElasticsearchMergeProgressCASContract(t *testing.T) {
	WithElasticsearchTestStore(t, runMergeProgressContract)
}

func TestMySQLMergeProgressCASContract(t *testing.T) { WithMySQLTestStore(t, runMergeProgressContract) }

func runMergeProgressContract(t *testing.T, s *Store, reopen func() *Store) {
	ctx := t.Context()
	// 派生引用允许按固定 key 重放；快照与进度不使用这个覆盖写入口。
	refs := map[string]json.RawMessage{"scope:ref": json.RawMessage(`{"bk_tenant_id":"one","alert_id":"a","relation_id":"r","role":"member"}`)}
	for range 2 {
		if err := s.PutReferences(ctx, refs); err != nil {
			t.Fatal(err)
		}
	}
	gotRefs, err := reopen().GetMany(ctx, "merge_relation_refs", []string{"scope:ref"})
	if err != nil || string(gotRefs["scope:ref"]) != string(refs["scope:ref"]) {
		t.Fatal("reference replay", err)
	}
	for _, kind := range []string{"merge_decisions", "merge_relations"} {
		raw := json.RawMessage(`{"bk_tenant_id":"one","state":"preparing","progress":{"phase":"linking","window_finished":false},"members":[{"alert_id":"a","state":"pending"},{"alert_id":"b","state":"pending"}],"fixed":{"nested":null},"wait_member_ids":["a","b"]}`)
		if err := s.Put(ctx, kind, "scope:pool", "", raw); err != nil {
			t.Fatal(err)
		}
		_, old, err := s.Get(ctx, kind, "scope:pool")
		if err != nil {
			t.Fatal(err)
		}
		fields := map[string]any{"state": "ready", "end_started_at": nil}
		if kind == "merge_decisions" {
			fields = map[string]any{"progress": map[string]any{"phase": "completed", "window_finished": true}}
		}
		version, err := s.CompareAndSetMergeProgress(ctx, kind, "scope:pool", old, fields, map[int]string{1: "linked"}, false)
		if err != nil || version == old {
			t.Fatal("partial CAS", version, err)
		}
		stored, actual, err := reopen().Get(ctx, kind, "scope:pool")
		if err != nil || actual != version {
			t.Fatal("returned version not persisted", actual, version, err)
		}
		var document struct {
			Fixed   map[string]any `json:"fixed"`
			Members []struct {
				ID    string `json:"alert_id"`
				State string `json:"state"`
			} `json:"members"`
			Wait []string `json:"wait_member_ids"`
		}
		if err := json.Unmarshal(stored, &document); err != nil {
			t.Fatal(err)
		}
		if len(document.Fixed) != 1 || document.Fixed["nested"] != nil || len(document.Wait) != 2 || document.Members[0].ID != "a" || document.Members[0].State != "pending" || document.Members[1].State != "linked" {
			t.Fatal("partial update lost fixed fields", string(stored))
		}
		if _, err := s.CompareAndSetMergeProgress(ctx, kind, "scope:pool", old, fields, nil, true); !errors.Is(err, policy.ErrConflict) {
			t.Fatal("stale holder overwrote progress", err)
		}
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := s.CompareAndSetMergeProgress(canceled, kind, "scope:pool", version, fields, nil, true); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled update", err)
		}
		var wins atomic.Int32
		var wg sync.WaitGroup
		start := make(chan struct{})
		for range 8 {
			wg.Go(func() {
				<-start
				_, err := s.CompareAndSetMergeProgress(ctx, kind, "scope:pool", version, fields, nil, true)
				if err == nil {
					wins.Add(1)
				} else if !errors.Is(err, policy.ErrConflict) {
					t.Error(err)
				}
			})
		}
		close(start)
		wg.Wait()
		if wins.Load() != 1 {
			t.Fatal("concurrent old versions both committed", wins.Load())
		}
	}
}
