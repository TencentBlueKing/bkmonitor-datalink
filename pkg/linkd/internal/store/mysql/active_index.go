// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package mysqlstore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ScanActiveIndex 在单条 SELECT 中读取身份和所需原始字段；没有逐行 SQL 或 Enrich 解码。
func (r *Repository) ScanActiveIndex(ctx context.Context, q store.ActiveIndexQuery, consume func([]store.ActiveIndexRow) error) error {
	if err := q.Validate(); err != nil {
		return err
	}
	query := "SELECT bk_tenant_id,event_source_id,alert_id,fingerprint"
	args := []any{}
	for _, field := range q.Fields {
		path := "$." + field
		if k, ok := strings.CutPrefix(field, "labels."); ok {
			quoted, _ := json.Marshal(k)
			path = "$.labels." + string(quoted)
		}
		if k, ok := strings.CutPrefix(field, "dimensions."); ok {
			quoted, _ := json.Marshal(k)
			path = "$.dimensions." + string(quoted)
		}
		query += ",JSON_EXTRACT(payload,?)"
		args = append(args, path)
	}
	query += " FROM linkd_alerts WHERE status='active' AND event_source_id IN (" + placeholders(len(q.Sources)) + ")"
	for _, source := range q.Sources {
		args = append(args, source)
	}
	if q.BKTenantID != "" {
		query += " AND bk_tenant_id=?"
		args = append(args, q.BKTenantID)
	}
	if len(q.Fingerprints) > 0 {
		//nolint:gosec // 这里只拼接占位符，fingerprint、来源、租户及 JSON 路径均使用绑定参数。
		query += " AND fingerprint IN (" + placeholders(len(q.Fingerprints)) + ")"
		for _, fp := range q.Fingerprints {
			args = append(args, fp)
		}
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	page := make([]store.ActiveIndexRow, 0, q.PageSize)
	bytes := 0
	for rows.Next() {
		var row store.ActiveIndexRow
		raw := make([][]byte, len(q.Fields))
		dest := []any{&row.BKTenantID, &row.EventSourceID, &row.AlertID, &row.Fingerprint}
		for i := range raw {
			dest = append(dest, &raw[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		for i, path := range q.Fields {
			if len(raw[i]) == 0 || string(raw[i]) == "null" {
				continue
			}
			switch path {
			case "subject_system":
				err = json.Unmarshal(raw[i], &row.SubjectSystem)
			case "subject_type":
				err = json.Unmarshal(raw[i], &row.SubjectType)
			case "subject_id":
				err = json.Unmarshal(raw[i], &row.SubjectID)
			default:
				var v domain.Scalar
				err = json.Unmarshal(raw[i], &v)
				if k, ok := strings.CutPrefix(path, "labels."); ok {
					if row.Labels == nil {
						row.Labels = domain.DimensionMap{}
					}
					row.Labels[k] = v
				}
				if k, ok := strings.CutPrefix(path, "dimensions."); ok {
					if row.Dimensions == nil {
						row.Dimensions = domain.DimensionMap{}
					}
					row.Dimensions[k] = v
				}
			}
			if err != nil {
				return fmt.Errorf("decode active index field %s: %w", path, err)
			}
		}
		encoded, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if len(encoded) > q.MaxBytes {
			return fmt.Errorf("active index row exceeds page byte limit")
		}
		if len(page) > 0 && (len(page) >= q.PageSize || bytes+len(encoded) > q.MaxBytes) {
			if err := consume(page); err != nil {
				return err
			}
			page = make([]store.ActiveIndexRow, 0, q.PageSize)
			bytes = 0
		}
		page = append(page, row)
		bytes += len(encoded)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(page) > 0 {
		return consume(page)
	}
	return nil
}
