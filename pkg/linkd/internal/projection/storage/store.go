// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package storage 持久化可靠投影任务及待办索引；不与策略缓存共用生命周期或过期清理。
package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/projection"
	es "linkd/internal/store/elasticsearch"
)

// Store 使用单对象 CAS 保存任务，payload 和 work 标记同次写入，不自动回收失败记录。
type Store struct {
	db               *sql.DB
	transport        *es.HTTPTransport
	namespace, index string
}

// Open 初始化独立投影任务集合；每个部署独立命名，不读取或清理其他部署数据。
func Open(ctx context.Context, c config.StorageConfig, deployment string) (*Store, error) {
	if strings.TrimSpace(deployment) == "" || len(deployment) > 256 {
		return nil, projection.ErrInvalid
	}
	c = c.WithDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(deployment))
	s := &Store{namespace: hex.EncodeToString(sum[:])}
	if c.Repository == config.RepositoryTypeMySQL {
		cfg := driver.NewConfig()
		cfg.Net = "tcp"
		cfg.Addr = c.MySQL.Address
		cfg.DBName = c.MySQL.Database
		cfg.User = c.MySQL.Username
		cfg.Passwd = c.MySQL.Password
		db, err := sql.Open("mysql", cfg.FormatDSN())
		if err != nil {
			return nil, err
		}
		s.db = db
		db.SetMaxOpenConns(8)
		db.SetMaxIdleConns(4)
		db.SetConnMaxLifetime(30 * time.Minute)
		_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS linkd_projection_tasks(
            namespace VARBINARY(64) NOT NULL,
            bk_tenant_id VARBINARY(64) NOT NULL,
            id VARBINARY(64) NOT NULL,
            version BIGINT UNSIGNED NOT NULL,
            work TINYINT NOT NULL,
            payload LONGBLOB NOT NULL,
            PRIMARY KEY(namespace,bk_tenant_id,id),
            KEY idx_projection_work(namespace,work,id),
            KEY idx_projection_tenant_work(namespace,bk_tenant_id,work,id),
            KEY idx_projection_tenant(namespace,bk_tenant_id,id)
        ) ENGINE=InnoDB`)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		return s, nil
	}
	if c.Repository != config.RepositoryTypeElasticsearch {
		return nil, projection.ErrInvalid
	}
	x := c.Elasticsearch
	tc := es.HTTPTransportConfig{Addresses: x.Addresses, APIKey: x.APIKey, MaxConnectionsPerHost: 8}
	if x.BasicAuth != nil {
		tc.BasicUsername = x.BasicAuth.Username
		tc.BasicPassword = x.BasicAuth.Password
	}
	tr, err := es.NewHTTPTransport(tc)
	if err != nil {
		return nil, err
	}
	s.transport = tr
	s.index = x.IndexPrefix + "-projection-" + s.namespace
	body := []byte(`{"mappings":{"dynamic":"strict","properties":{"id":{"type":"keyword"},"bk_tenant_id":{"type":"keyword"},"work":{"type":"boolean"},"payload":{"type":"object","enabled":false}}}}`)
	code, raw, err := s.request(ctx, http.MethodPut, "/"+s.index, body)
	if err != nil {
		tr.Close()
		return nil, err
	}
	if code != 200 {
		var failure struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		exists := code == 400 && json.Unmarshal(raw, &failure) == nil && failure.Error.Type == "resource_already_exists_exception"
		if !exists {
			tr.Close()
			return nil, fmt.Errorf("initialize projection tasks: HTTP %d", code)
		}
	}
	return s, nil
}

// Close 释放连接池，不删除任务或业务数据。
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	if s.transport != nil {
		s.transport.Close()
	}
	return nil
}

func identity(tenant, id string) error {
	b, err := hex.DecodeString(id)
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || err != nil || len(b) != 32 || hex.EncodeToString(b) != id {
		return projection.ErrInvalid
	}
	return nil
}

// Get 使用显式租户与任务 ID 实时读取，并核对载荷和后端索引身份。
func (s *Store) Get(ctx context.Context, tenant, id string) (projection.StoredTask, error) {
	if err := ctx.Err(); err != nil {
		return projection.StoredTask{}, err
	}
	if err := identity(tenant, id); err != nil {
		return projection.StoredTask{}, err
	}
	if s.db != nil {
		var raw []byte
		var version uint64
		var work bool
		err := s.db.QueryRowContext(ctx, `SELECT payload,version,work FROM linkd_projection_tasks WHERE namespace=? AND bk_tenant_id=? AND id=?`, s.namespace, tenant, id).Scan(&raw, &version, &work)
		if errors.Is(err, sql.ErrNoRows) {
			return projection.StoredTask{}, projection.ErrNotFound
		}
		if err != nil {
			return projection.StoredTask{}, err
		}
		return decode(raw, tenant, id, strconv.FormatUint(version, 10), work)
	}
	code, raw, err := s.request(ctx, http.MethodGet, "/"+s.index+"/_doc/"+tenant+"-"+id, nil)
	if err != nil {
		return projection.StoredTask{}, err
	}
	if code == 404 {
		return projection.StoredTask{}, projection.ErrNotFound
	}
	if code != 200 {
		return projection.StoredTask{}, fmt.Errorf("read projection task: HTTP %d", code)
	}
	var hit esHit
	if json.Unmarshal(raw, &hit) != nil {
		return projection.StoredTask{}, projection.ErrInvalid
	}
	return s.decodeHit(hit, tenant, id)
}

type esDocument struct {
	ID       string          `json:"id"`
	TenantID string          `json:"bk_tenant_id"`
	Work     bool            `json:"work"`
	Payload  json.RawMessage `json:"payload"`
}

type esHit struct {
	ID     string     `json:"_id"`
	Index  string     `json:"_index"`
	Seq    int64      `json:"_seq_no"`
	Term   int64      `json:"_primary_term"`
	Source esDocument `json:"_source"`
}

func (s *Store) decodeHit(h esHit, tenant, id string) (projection.StoredTask, error) {
	if h.Index != s.index || h.ID != tenant+"-"+id || h.Source.ID != id || h.Source.TenantID != tenant || h.Seq < 0 || h.Term < 1 {
		return projection.StoredTask{}, projection.ErrInvalid
	}
	return decode(h.Source.Payload, tenant, id, fmt.Sprintf("%d:%d", h.Seq, h.Term), h.Source.Work)
}

func decode(raw []byte, tenant, id, version string, work bool) (projection.StoredTask, error) {
	var t projection.Task
	if len(raw) > projection.MaxTaskBytes || json.Unmarshal(raw, &t) != nil || t.Validate() != nil || t.ID != id || t.Request.TenantID != tenant || t.HasWork() != work || version == "" || version == "0" {
		return projection.StoredTask{}, projection.ErrInvalid
	}
	return projection.StoredTask{Task: t, Version: version}, nil
}

// Put 在条件写边界验证完整转换，失败不改动旧任务；空 expected 为 create-only。
func (s *Store) Put(ctx context.Context, t projection.Task, expected string) (projection.StoredTask, error) {
	if err := ctx.Err(); err != nil {
		return projection.StoredTask{}, err
	}
	if err := identity(t.Request.TenantID, t.ID); err != nil {
		return projection.StoredTask{}, err
	}
	var current *projection.Task
	if expected != "" {
		row, err := s.Get(ctx, t.Request.TenantID, t.ID)
		if err != nil {
			return projection.StoredTask{}, err
		}
		if row.Version != expected {
			return projection.StoredTask{}, projection.ErrConflict
		}
		current = &row.Task
	}
	if err := projection.ValidateWrite(current, t); err != nil {
		return projection.StoredTask{}, err
	}
	raw, err := json.Marshal(t)
	if err != nil || len(raw) > projection.MaxTaskBytes {
		return projection.StoredTask{}, projection.ErrInvalid
	}
	if s.db != nil {
		var result sql.Result
		version := uint64(1)
		if expected == "" {
			result, err = s.db.ExecContext(ctx, `INSERT INTO linkd_projection_tasks(namespace,bk_tenant_id,id,version,work,payload) VALUES(?,?,?,1,?,?)`, s.namespace, t.Request.TenantID, t.ID, t.HasWork(), raw)
		} else {
			version, err = strconv.ParseUint(expected, 10, 64)
			if err != nil || version == ^uint64(0) {
				return projection.StoredTask{}, projection.ErrInvalid
			}
			result, err = s.db.ExecContext(ctx, `UPDATE linkd_projection_tasks SET version=version+1,work=?,payload=? WHERE namespace=? AND bk_tenant_id=? AND id=? AND version=?`, t.HasWork(), raw, s.namespace, t.Request.TenantID, t.ID, version)
			version++
		}
		if err != nil {
			var e *driver.MySQLError
			if errors.As(err, &e) && e.Number == 1062 {
				return projection.StoredTask{}, projection.ErrConflict
			}
			return projection.StoredTask{}, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return projection.StoredTask{}, err
		}
		if n != 1 {
			return projection.StoredTask{}, projection.ErrConflict
		}
		return projection.StoredTask{Task: t.Clone(), Version: strconv.FormatUint(version, 10)}, nil
	}
	query := url.Values{"refresh": {"wait_for"}}
	if expected == "" {
		query.Set("op_type", "create")
	} else {
		var seq, term int64
		if _, err := fmt.Sscanf(expected, "%d:%d", &seq, &term); err != nil || seq < 0 || term < 1 || fmt.Sprintf("%d:%d", seq, term) != expected {
			return projection.StoredTask{}, projection.ErrInvalid
		}
		query.Set("if_seq_no", strconv.FormatInt(seq, 10))
		query.Set("if_primary_term", strconv.FormatInt(term, 10))
	}
	body, err := json.Marshal(esDocument{ID: t.ID, TenantID: t.Request.TenantID, Work: t.HasWork(), Payload: raw})
	if err != nil {
		return projection.StoredTask{}, err
	}
	code, response, err := s.request(ctx, http.MethodPut, "/"+s.index+"/_doc/"+t.Request.TenantID+"-"+t.ID+"?"+query.Encode(), body)
	if err != nil {
		return projection.StoredTask{}, err
	}
	if code == 409 {
		return projection.StoredTask{}, projection.ErrConflict
	}
	if code != 200 && code != 201 {
		return projection.StoredTask{}, fmt.Errorf("write projection task: HTTP %d", code)
	}
	var h esHit
	if json.Unmarshal(response, &h) != nil || h.Index != s.index || h.ID != t.Request.TenantID+"-"+t.ID || h.Seq < 0 || h.Term < 1 {
		return projection.StoredTask{}, projection.ErrInvalid
	}
	return projection.StoredTask{Task: t.Clone(), Version: fmt.Sprintf("%d:%d", h.Seq, h.Term)}, nil
}

// List 只读取最多 16 条完整任务；work-only 通过同次 CAS 的派生索引跳过全部已完成和失败历史。
// 按 ID 单调游标扫描，新增到旧游标之前的记录下一轮发现，不宣称跨对象一致快照。
func (s *Store) List(ctx context.Context, q projection.Query) ([]projection.StoredTask, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := []projection.StoredTask{}
	if s.db != nil {
		statement := `SELECT bk_tenant_id,id,payload,version,work FROM linkd_projection_tasks WHERE namespace=? AND id>?`
		args := []any{s.namespace, q.After}
		if q.TenantID != "" {
			statement += " AND bk_tenant_id=?"
			args = append(args, q.TenantID)
		}
		if q.WorkOnly {
			statement += " AND work=1"
		}
		statement += " ORDER BY id LIMIT ?"
		args = append(args, q.Limit)
		rows, err := s.db.QueryContext(ctx, statement, args...)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var tenant, id string
			var raw []byte
			var version uint64
			var work bool
			if err := rows.Scan(&tenant, &id, &raw, &version, &work); err != nil {
				return nil, err
			}
			row, err := decode(raw, tenant, id, strconv.FormatUint(version, 10), work)
			if err != nil {
				return nil, err
			}
			result = append(result, row)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	} else {
		filters := []any{map[string]any{"range": map[string]any{"id": map[string]any{"gt": q.After}}}}
		if q.TenantID != "" {
			filters = append(filters, map[string]any{"term": map[string]any{"bk_tenant_id": q.TenantID}})
		}
		if q.WorkOnly {
			filters = append(filters, map[string]any{"term": map[string]any{"work": true}})
		}
		body, _ := json.Marshal(map[string]any{"size": q.Limit, "sort": []string{"id"}, "seq_no_primary_term": true, "track_total_hits": false, "query": map[string]any{"bool": map[string]any{"filter": filters}}})
		code, raw, err := s.request(ctx, http.MethodPost, "/"+s.index+"/_search", body)
		if err != nil {
			return nil, err
		}
		if code != 200 {
			return nil, fmt.Errorf("list projection tasks: HTTP %d", code)
		}
		var response struct {
			TimedOut bool `json:"timed_out"`
			Shards   struct {
				Failed int `json:"failed"`
			} `json:"_shards"`
			Hits struct {
				Hits []esHit `json:"hits"`
			} `json:"hits"`
		}
		if json.Unmarshal(raw, &response) != nil || response.TimedOut || response.Shards.Failed > 0 || response.Hits.Hits == nil || len(response.Hits.Hits) > q.Limit {
			return nil, projection.ErrInvalid
		}
		for _, h := range response.Hits.Hits {
			row, err := s.decodeHit(h, h.Source.TenantID, h.Source.ID)
			if err != nil {
				return nil, err
			}
			result = append(result, row)
		}
	}
	previous := q.After
	for _, row := range result {
		t := row.Task
		if t.ID <= previous || q.TenantID != "" && t.Request.TenantID != q.TenantID || q.WorkOnly && !t.HasWork() {
			return nil, projection.ErrInvalid
		}
		previous = t.ID
	}
	return result, nil
}

func (s *Store) request(ctx context.Context, method, path string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := s.transport.Perform(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 32<<20+1))
	if len(raw) > 32<<20 {
		return response.StatusCode, nil, projection.ErrInvalid
	}
	return response.StatusCode, raw, err
}

var _ projection.Store = (*Store)(nil)
