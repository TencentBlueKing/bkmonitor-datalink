// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"linkd/internal/policy"
	"linkd/internal/store"
	"linkd/internal/suppressioncleanup"
)

// ReadCleanupBatch 实时批读清理记录；筛选关闭只使用 ES，其他后端保留原单项语义。
func (s *Store) ReadCleanupBatch(ctx context.Context, ids []string) []suppressioncleanup.Document {
	results := make([]suppressioncleanup.Document, len(ids))
	if s.db != nil {
		for i, id := range ids {
			b, v, e := s.Get(ctx, "suppression_cleanups", id)
			results[i] = suppressioncleanup.Document{ID: id, Payload: b, Version: v, Err: e}
		}
		return results
	}
	for start := 0; start < len(ids); start += store.MaxCloseBatchSize {
		end := min(start+store.MaxCloseBatchSize, len(ids))
		docs := []any{}
		for _, id := range ids[start:end] {
			docs = append(docs, map[string]string{"_index": s.table("suppression_cleanups"), "_id": id})
		}
		raw, _ := json.Marshal(map[string]any{"docs": docs})
		code, body, err := s.request(ctx, http.MethodPost, "/_mget?_source_includes=payload", raw)
		var response struct {
			Docs []struct {
				Found  bool  `json:"found"`
				Seq    int64 `json:"_seq_no"`
				Term   int64 `json:"_primary_term"`
				Source struct {
					Payload json.RawMessage `json:"payload"`
				} `json:"_source"`
			} `json:"docs"`
		}
		if err == nil && code != 200 {
			err = fmt.Errorf("read cleanup batch: HTTP %d", code)
		}
		if err == nil {
			err = json.Unmarshal(body, &response)
		}
		for i := start; i < end; i++ {
			row := suppressioncleanup.Document{ID: ids[i], Err: err}
			if err == nil {
				j := i - start
				if j >= len(response.Docs) {
					row.Err = fmt.Errorf("read cleanup batch missing item")
				} else if !response.Docs[j].Found {
					row.Err = policy.ErrNotFound
				} else {
					d := response.Docs[j]
					row.Payload = d.Source.Payload
					row.Version = fmt.Sprintf("%d:%d", d.Seq, d.Term)
				}
			}
			results[i] = row
		}
	}
	return results
}

// WriteCleanupBatch 用 Bulk create/index 保存清理阶段，每项携带原有 CAS 条件并返回新版本。
func (s *Store) WriteCleanupBatch(ctx context.Context, docs []suppressioncleanup.Document) []suppressioncleanup.Document {
	started := time.Now()
	defer func() { store.ObserveBatchPhase(ctx, "storage.suppression_cleanups", time.Since(started)) }()
	results := append([]suppressioncleanup.Document(nil), docs...)
	if s.db != nil {
		for i, d := range docs {
			results[i].Err = s.Put(ctx, "suppression_cleanups", d.ID, d.Version, d.Payload)
			if results[i].Err == nil {
				results[i].Payload, results[i].Version, results[i].Err = s.Get(ctx, "suppression_cleanups", d.ID)
			}
		}
		return results
	}
	var raw bytes.Buffer
	positions := []int{}
	flush := func() {
		if len(positions) == 0 {
			return
		}
		code, body, err := s.request(ctx, http.MethodPost, "/_bulk?refresh=false", raw.Bytes())
		var response struct {
			Items []map[string]struct {
				Status int   `json:"status"`
				Seq    int64 `json:"_seq_no"`
				Term   int64 `json:"_primary_term"`
			} `json:"items"`
		}
		if err == nil && code != 200 {
			err = fmt.Errorf("write cleanup batch: HTTP %d", code)
		}
		if err == nil {
			err = json.Unmarshal(body, &response)
		}
		for j, i := range positions {
			results[i].Err = err
			if err != nil {
				continue
			}
			if j >= len(response.Items) {
				results[i].Err = fmt.Errorf("cleanup bulk response missing item")
				continue
			}
			for _, item := range response.Items[j] {
				if item.Status == 409 {
					results[i].Err = policy.ErrConflict
				} else if item.Status != 200 && item.Status != 201 {
					results[i].Err = fmt.Errorf("cleanup bulk item: HTTP %d", item.Status)
				} else {
					results[i].Version = fmt.Sprintf("%d:%d", item.Seq, item.Term)
				}
			}
		}
		raw.Reset()
		positions = nil
	}
	for i, d := range docs {
		if err := valid("suppression_cleanups", d.ID); err != nil {
			results[i].Err = err
			continue
		}
		op := "create"
		meta := map[string]any{"_index": s.table("suppression_cleanups"), "_id": d.ID}
		if d.Version != "" {
			op = "index"
			var seq, term int64
			if _, err := fmt.Sscanf(d.Version, "%d:%d", &seq, &term); err != nil {
				results[i].Err = err
				continue
			}
			meta["if_seq_no"], meta["if_primary_term"] = seq, term
		}
		head, _ := json.Marshal(map[string]any{op: meta})
		body, err := json.Marshal(map[string]any{"id": d.ID, "payload": d.Payload})
		if err != nil {
			results[i].Err = err
			continue
		}
		if raw.Len() > 0 && (len(positions) >= store.MaxCloseBatchSize || raw.Len()+len(head)+len(body)+2 > 8<<20) {
			flush()
		}
		raw.Write(head)
		raw.WriteByte('\n')
		raw.Write(body)
		raw.WriteByte('\n')
		positions = append(positions, i)
	}
	flush()
	return results
}
