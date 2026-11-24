// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package storage

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// 两个后端都使用现有隔离测试资源，构造缺 work 的旧行并在回填中途重开。
func TestElasticsearchLegacyMergeWorkMigration(t *testing.T) {
	WithElasticsearchTestStore(t, runLegacyMergeWorkMigration)
}

func TestMySQLLegacyMergeWorkMigration(t *testing.T) {
	WithMySQLTestStore(t, runLegacyMergeWorkMigration)
}

func TestElasticsearchMergeSnapshotBatchContract(t *testing.T) {
	WithElasticsearchTestStore(t, runMergeSnapshotBatchContract)
}

func TestMySQLMergeSnapshotBatchContract(t *testing.T) {
	WithMySQLTestStore(t, runMergeSnapshotBatchContract)
}

func runMergeSnapshotBatchContract(t *testing.T, s *Store, reopen func() *Store) {
	for _, kind := range []string{"merge_members", "merge_relation_refs"} {
		t.Run(kind, func(t *testing.T) { runMergeBatchKindContract(t, s, reopen, kind) })
	}
}

func runMergeBatchKindContract(t *testing.T, s *Store, reopen func() *Store, kind string) {
	t.Helper()
	documents := map[string]json.RawMessage{}
	ids := []string{}
	for i := range 17 {
		id := fmt.Sprintf("snapshot-%03d", i)
		ids = append(ids, id)
		documents[id] = json.RawMessage(fmt.Sprintf(`{"title":"first-%d"}`, i))
	}
	if err := s.CreateMany(t.Context(), kind, documents); err != nil {
		t.Fatal(err)
	}
	documents[ids[0]] = json.RawMessage(`{"title":"replacement"}`)
	if err := s.CreateMany(t.Context(), kind, documents); err != nil {
		t.Fatal("create-only retry failed", err)
	}
	actual, err := reopen().GetMany(t.Context(), kind, append(ids, "missing"))
	if err != nil || len(actual) != 17 || string(actual[ids[0]]) != `{"title":"first-0"}` {
		t.Fatal("batch overwrote first snapshot or lost rows", err)
	}
	for _, id := range ids[1:] {
		if string(actual[id]) != string(documents[id]) {
			t.Fatal("batch snapshot changed", id)
		}
	}
}

func runLegacyMergeWorkMigration(t *testing.T, s *Store, reopen func() *Store) {
	t.Helper()
	kind := "merge_relations"
	if s.db != nil {
		// 本次独立数据库的新表还没有业务行，移除新字段以构造真实旧 schema。
		if _, err := s.db.ExecContext(t.Context(), "ALTER TABLE "+s.table(kind)+" DROP INDEX idx_shield_request_work, DROP COLUMN work"); err != nil {
			t.Fatal(err)
		}
	}
	active := json.RawMessage(`{"state":"ready","payload_marker":"unchanged"}`)
	for i := range 65 {
		id := fmt.Sprintf("history-%03d", i)
		raw := json.RawMessage(`{"state":"ended"}`)
		if i == 64 {
			id = "zz-active"
			raw = active
		}
		if s.db != nil {
			//nolint:gosec // G202: 表名固定为本轮隔离库的 merge_relations，数据使用绑定参数。
			if _, err := s.db.ExecContext(t.Context(), "INSERT INTO "+s.table(kind)+"(namespace,id,version,payload) VALUES(?,?,1,?)", s.namespace, id, []byte(raw)); err != nil {
				t.Fatal(err)
			}
		} else {
			doc, _ := json.Marshal(map[string]any{"id": id, "payload": raw})
			status, _, err := s.request(t.Context(), http.MethodPut, "/"+s.table(kind)+"/_doc/"+id+"?refresh=false", doc)
			if err != nil || status != 201 {
				t.Fatal("create isolated legacy fixture", status, err)
			}
		}
	}
	refreshMergeFixture(t, s, kind)
	first := reopen()
	if _, err := first.ListMergeWork(t.Context(), kind, "", 16); err != nil {
		t.Fatal(err)
	}
	resumed := reopen()
	found := false
	for range 5 {
		rows, err := resumed.ListMergeWork(t.Context(), kind, "", 16)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range rows {
			if string(raw) != string(active) {
				t.Fatal("completed history returned as work", string(raw))
			}
			found = true
		}
	}
	if !found {
		t.Fatal("legacy active relation lost after interrupted migration")
	}
	stored, _, err := resumed.Get(t.Context(), kind, "zz-active")
	if err != nil || string(stored) != string(active) {
		t.Fatal("migration changed business payload", string(stored), err)
	}
	if !resumed.mergeWorkReady[kind] {
		t.Fatal("legacy migration never completed")
	}
	if s.db != nil {
		var count int
		if err := s.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+s.table(kind)+" WHERE namespace=?", s.namespace).Scan(&count); err != nil || count != 65 {
			t.Fatal("migration discarded history", count, err)
		}
	} else {
		refreshMergeFixture(t, s, kind)
		count := 0
		after := ""
		for {
			rows, err := resumed.List(t.Context(), kind, "", after, 16)
			if err != nil {
				t.Fatal(err)
			}
			count += len(rows)
			if len(rows) < 16 {
				break
			}
			// 身份在写入顺序中固定；65 行跨过至少两轮后台回填。
			if count < 65 {
				after = fmt.Sprintf("history-%03d", count-1)
			} else {
				after = "zz-active"
			}
		}
		if count != 65 {
			t.Fatal("migration discarded history", count)
		}
	}
}
