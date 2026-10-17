// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package storage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/policy"
)

func isMergeWork(kind string) bool {
	return kind == "merge_decisions" || kind == "merge_relations"
}

func hasStoredWork(kind string) bool { return hasRequestWork(kind) || isMergeWork(kind) }

func mergeWorkValue(kind string, raw json.RawMessage) (bool, error) {
	var value struct {
		State    string `json:"state"`
		Progress struct {
			Phase          string `json:"phase"`
			WindowFinished bool   `json:"window_finished"`
		} `json:"progress"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return false, err
	}
	if kind == "merge_relations" {
		return value.State != "ended", nil
	}
	return value.Progress.Phase != "completed" || !value.Progress.WindowFinished, nil
}

// ensureMySQLMergeWork 为旧表补可空索引列；NULL 表示尚待后台补齐，不删除或重建业务数据。
func (s *Store) ensureMySQLMergeWork(ctx context.Context, kind string) error {
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name='work'", s.table(kind)).Scan(&count); err != nil {
		return err
	}
	if count != 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, "ALTER TABLE "+s.table(kind)+" ADD COLUMN work TINYINT NULL DEFAULT NULL, ADD KEY idx_merge_work(namespace,work,id)")
	var mysqlErr *driver.MySQLError
	if errors.As(err, &mysqlErr) && mysqlErr.Number == 1060 {
		return nil
	} // 多个控制面同时启动时由先完成的实例创建。
	return err
}

// ListMergeWork 只读取实际待办。旧记录的派生索引每轮补一页，失败/重启继续，不影响 Event 处理。
func (s *Store) ListMergeWork(ctx context.Context, kind, after string, limit int) ([]json.RawMessage, error) {
	maxPage := 16
	if !isMergeWork(kind) || len(after) > 256 || limit < 1 || limit > maxPage {
		return nil, policy.ErrInvalid
	}
	if err := s.backfillMergeWork(ctx, kind); err != nil {
		return nil, err
	}
	if s.db != nil {
		//nolint:gosec // G202: kind 限定为合并集合，值为绑定参数。
		rows, err := s.db.QueryContext(ctx, "SELECT payload FROM "+s.table(kind)+" WHERE namespace=? AND work=1 AND id>? ORDER BY id LIMIT ?", s.namespace, after, limit)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		out := []json.RawMessage{}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return nil, err
			}
			out = append(out, raw)
		}
		return out, rows.Err()
	}
	raw, _ := json.Marshal(map[string]any{"size": limit, "sort": []string{"id"}, "query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"work": true}}, map[string]any{"range": map[string]any{"id": map[string]any{"gt": after}}}}}}})
	status, raw, err := s.request(ctx, http.MethodPost, "/"+s.table(kind)+"/_search", raw)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("list merge work: HTTP %d", status)
	}
	var response struct {
		TimedOut bool `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Hits []struct {
				Source struct {
					Payload json.RawMessage `json:"payload"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if response.TimedOut || response.Shards.Failed > 0 || response.Hits.Hits == nil || len(response.Hits.Hits) > limit {
		return nil, fmt.Errorf("incomplete merge work page")
	}
	result := make([]json.RawMessage, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		result = append(result, hit.Source.Payload)
	}
	return result, nil
}

func (s *Store) backfillMergeWork(ctx context.Context, kind string) error {
	s.mergeWorkMu.Lock()
	defer s.mergeWorkMu.Unlock()
	if s.mergeWorkReady[kind] {
		return nil
	}
	if s.mergeWorkReady == nil {
		s.mergeWorkReady = map[string]bool{}
	}
	if s.db != nil {
		// 1. 只领取尚无派生标记的旧行；普通 Put 已同时保存 work。
		//nolint:gosec // G202: kind 限定为合并集合，作用域使用绑定参数。
		rows, err := s.db.QueryContext(ctx, "SELECT id,payload,version FROM "+s.table(kind)+" WHERE namespace=? AND work IS NULL ORDER BY id LIMIT 16", s.namespace)
		if err != nil {
			return err
		}
		type row struct {
			id      string
			raw     []byte
			version int64
		}
		pending := []row{}
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.raw, &r.version); err != nil {
				_ = rows.Close()
				return err
			}
			pending = append(pending, r)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		if len(pending) == 0 {
			s.mergeWorkReady[kind] = true
			return nil
		}
		// 2. 一次更新本页索引；版本条件避免覆盖并发业务变更，业务版本本身不递增。
		var cases, pairs strings.Builder
		args := []any{}
		for _, r := range pending {
			work, err := mergeWorkValue(kind, r.raw)
			if err != nil {
				return err
			}
			cases.WriteString(" WHEN ? THEN ?")
			args = append(args, r.id, work)
		}
		args = append(args, s.namespace)
		for _, r := range pending {
			if pairs.Len() > 0 {
				pairs.WriteByte(',')
			}
			pairs.WriteString("(?,?)")
			args = append(args, r.id, r.version)
		}
		//nolint:gosec // G202: SQL 结构只按已读取的页长度构造，值为绑定参数。
		_, err = s.db.ExecContext(ctx, "UPDATE "+s.table(kind)+" SET work=CASE id"+cases.String()+" END WHERE namespace=? AND work IS NULL AND (id,version) IN ("+pairs.String()+")", args...)
		return err
	}
	// 1. 旧 ES 的 payload 不参与索引，按缺失 work 分页读取后补齐，不永久扫描全部历史。
	raw := []byte(`{"size":16,"seq_no_primary_term":true,"query":{"bool":{"must_not":{"exists":{"field":"work"}}}}}`)
	status, raw, err := s.request(ctx, http.MethodPost, "/"+s.table(kind)+"/_search", raw)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("read legacy merge work: HTTP %d", status)
	}
	var response struct {
		TimedOut bool `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Hits []struct {
				ID     string `json:"_id"`
				Seq    int64  `json:"_seq_no"`
				Term   int64  `json:"_primary_term"`
				Source struct {
					Payload json.RawMessage `json:"payload"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	if response.TimedOut || response.Shards.Failed > 0 || response.Hits.Hits == nil {
		return fmt.Errorf("incomplete legacy merge work page")
	}
	if len(response.Hits.Hits) == 0 {
		s.mergeWorkReady[kind] = true
		return nil
	}
	// 2. 派生索引按原仓储版本条件更新；冲突交下一页重读，不能覆盖已推进的业务记录。
	var body strings.Builder
	for _, hit := range response.Hits.Hits {
		work, err := mergeWorkValue(kind, hit.Source.Payload)
		if err != nil {
			return err
		}
		meta, _ := json.Marshal(map[string]any{"update": map[string]any{"_id": hit.ID, "if_seq_no": hit.Seq, "if_primary_term": hit.Term}})
		value, _ := json.Marshal(map[string]any{"doc": map[string]any{"work": work}})
		body.Write(meta)
		body.WriteByte('\n')
		body.Write(value)
		body.WriteByte('\n')
	}
	status, raw, err = s.request(ctx, http.MethodPost, "/"+s.table(kind)+"/_bulk?refresh=wait_for", []byte(body.String()))
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("index legacy merge work: HTTP %d", status)
	}
	var written struct {
		Items []struct {
			Update struct {
				Status int `json:"status"`
			} `json:"update"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &written); err != nil {
		return err
	}
	if len(written.Items) != len(response.Hits.Hits) {
		return fmt.Errorf("incomplete legacy merge work write")
	}
	for _, item := range written.Items {
		if item.Update.Status != http.StatusOK && item.Update.Status != http.StatusConflict {
			return fmt.Errorf("index legacy merge work item: HTTP %d", item.Update.Status)
		}
	}
	return nil
}
