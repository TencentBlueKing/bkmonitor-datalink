// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License").

package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"linkd/internal/policy"
)

// GetMany 实时批读合并成员快照或关系反查引用；缺失 ID 不返回，存储失败不作为缺失处理。
func (s *Store) GetMany(ctx context.Context, kind string, ids []string) (map[string]json.RawMessage, error) {
	if (kind != "merge_members" && kind != "merge_relation_refs") || len(ids) > 128 {
		return nil, policy.ErrInvalid
	}
	result := make(map[string]json.RawMessage, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	for _, id := range ids {
		if err := valid(kind, id); err != nil {
			return nil, err
		}
	}
	if s.db != nil {
		args := []any{s.namespace}
		for _, id := range ids {
			args = append(args, id)
		}
		//nolint:gosec // G202: kind 限定为合并成员或关系引用表，ID 使用绑定参数。
		rows, err := s.db.QueryContext(ctx, "SELECT id,payload FROM "+s.table(kind)+" WHERE namespace=? AND id IN ("+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+")", args...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			var raw []byte
			if err := rows.Scan(&id, &raw); err != nil {
				return nil, err
			}
			result[id] = raw
		}
		return result, rows.Err()
	}
	raw, err := json.Marshal(map[string]any{"ids": ids})
	if err != nil {
		return nil, err
	}
	status, raw, err := s.request(ctx, http.MethodPost, "/"+s.table(kind)+"/_mget?realtime=true", raw)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("read merge snapshots: HTTP %d", status)
	}
	var response struct {
		Docs []struct {
			ID     string          `json:"_id"`
			Found  bool            `json:"found"`
			Error  json.RawMessage `json:"error"`
			Source struct {
				Payload json.RawMessage `json:"payload"`
			} `json:"_source"`
		} `json:"docs"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	if len(response.Docs) != len(ids) {
		return nil, fmt.Errorf("incomplete merge snapshot batch")
	}
	for i, doc := range response.Docs {
		//nolint:gosec // G602: 循环前已确认 Docs 与 ids 长度相等。
		id := ids[i]
		if doc.ID != id || len(doc.Error) != 0 {
			return nil, fmt.Errorf("read merge snapshot item %s failed", id)
		}
		if doc.Found {
			result[doc.ID] = doc.Source.Payload
		}
	}
	return result, nil
}

// CreateMany 批量保存首次快照；已存在的文档保持原值，其余单项失败必须向上返回。
func (s *Store) CreateMany(ctx context.Context, kind string, documents map[string]json.RawMessage) error {
	return s.writeMergeDocuments(ctx, kind, documents, false)
}

// PutReferences 幂等写入由固定关系派生的查询映射；逐项写成功即确认，无需读回。
// 只有引用可覆盖，成员快照和批次进度仍保留首次创建及原 CAS 语义。
func (s *Store) PutReferences(ctx context.Context, documents map[string]json.RawMessage) error {
	return s.writeMergeDocuments(ctx, "merge_relation_refs", documents, true)
}

func (s *Store) writeMergeDocuments(ctx context.Context, kind string, documents map[string]json.RawMessage, replace bool) error {
	if (kind != "merge_members" && kind != "merge_relation_refs") || len(documents) > 128 {
		return policy.ErrInvalid
	}
	if len(documents) == 0 {
		return nil
	}
	ids := make([]string, 0, len(documents))
	for id, raw := range documents {
		if err := valid(kind, id); err != nil {
			return err
		}
		if len(raw) > 8<<20 {
			return policy.ErrInvalid
		}
		ids = append(ids, id)
	}
	slices.Sort(ids)
	if s.db != nil {
		args := make([]any, 0, 3*len(ids))
		for _, id := range ids {
			args = append(args, s.namespace, id, []byte(documents[id]))
		}
		columns, values := "namespace,id,version,payload", "(?,?,1,?),"
		duplicate := "id=id"
		if replace {
			duplicate = "payload=VALUES(payload)"
		}
		//nolint:gosec // G202: kind 限定为合并集合，业务值使用绑定参数。
		_, err := s.db.ExecContext(ctx, "INSERT INTO "+s.table(kind)+"("+columns+") VALUES "+strings.TrimSuffix(strings.Repeat(values, len(ids)), ",")+" ON DUPLICATE KEY UPDATE "+duplicate, args...)
		return err
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	action := "create"
	if replace {
		action = "index"
	}
	for _, id := range ids {
		if err := encoder.Encode(map[string]any{action: map[string]any{"_id": id}}); err != nil {
			return err
		}
		doc := map[string]any{"id": id, "payload": documents[id]}
		if err := encoder.Encode(doc); err != nil {
			return err
		}
	}
	status, raw, err := s.request(ctx, http.MethodPost, "/"+s.table(kind)+"/_bulk?refresh=false", body.Bytes())
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("create merge snapshots: HTTP %d", status)
	}
	var response struct {
		Items []map[string]struct {
			ID     string `json:"_id"`
			Status int    `json:"status"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return err
	}
	if len(response.Items) != len(ids) {
		return fmt.Errorf("incomplete merge snapshot write batch")
	}
	for i, item := range response.Items {
		result := item[action]
		ok := result.Status == http.StatusCreated || (!replace && result.Status == http.StatusConflict) || (replace && result.Status == http.StatusOK)
		if result.ID != ids[i] || !ok {
			return fmt.Errorf("write merge document %s: HTTP %d", ids[i], result.Status)
		}
	}
	return nil
}
