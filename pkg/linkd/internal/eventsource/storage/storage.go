// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package storage 将来源的两个文档集合映射到 ES/MySQL 单对象存储。
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
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/config"
	"linkd/internal/eventsource"
	es "linkd/internal/store/elasticsearch"
)

// Store 持有来源管理专用的有界连接资源。
type Store struct {
	db        *sql.DB
	transport *es.HTTPTransport
	namespace string
}

// Open 初始化来源的两个集合，不清理历史或业务数据。
func Open(ctx context.Context, c config.StorageConfig, deployment string) (*Store, error) {
	hash := sha256.Sum256([]byte(deployment))
	s := &Store{namespace: hex.EncodeToString(hash[:])}
	c = c.WithDefaults()
	if c.Repository == config.RepositoryTypeMySQL && c.MySQL != nil {
		x := driver.NewConfig()
		x.User = c.MySQL.Username
		x.Passwd = c.MySQL.Password
		x.Net = "tcp"
		x.Addr = c.MySQL.Address
		x.DBName = c.MySQL.Database
		db, e := sql.Open("mysql", x.FormatDSN())
		if e != nil {
			return nil, e
		}
		s.db = db
		db.SetMaxOpenConns(8)
		db.SetMaxIdleConns(4)
		db.SetConnMaxLifetime(30 * time.Minute)
		for _, name := range []string{"records", "releases"} {
			_, e = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.table(name)+" (namespace VARBINARY(256) NOT NULL, id VARBINARY(256) NOT NULL, version BIGINT NOT NULL, payload LONGBLOB NOT NULL, PRIMARY KEY(namespace,id))")
			if e != nil {
				_ = db.Close()
				return nil, e
			}
		}
	} else if c.Repository == config.RepositoryTypeElasticsearch && c.Elasticsearch != nil {
		x := c.Elasticsearch
		tc := es.HTTPTransportConfig{Addresses: x.Addresses, APIKey: x.APIKey, MaxConnectionsPerHost: 8}
		if x.BasicAuth != nil {
			tc.BasicUsername = x.BasicAuth.Username
			tc.BasicPassword = x.BasicAuth.Password
		}
		t, e := es.NewHTTPTransport(tc)
		if e != nil {
			return nil, e
		}
		s.transport = t
		for _, name := range []string{"records", "releases"} {
			code, response, e := s.request(ctx, http.MethodPut, "/"+s.table(name), json.RawMessage(`{"mappings":{"dynamic":"strict","properties":{"id":{"type":"keyword"},"payload":{"type":"object","enabled":false}}}}`))
			if e != nil {
				t.Close()
				return nil, e
			}
			exists := false
			if code == 400 {
				var detail struct {
					Error struct {
						Type string `json:"type"`
					} `json:"error"`
				}
				if json.Unmarshal(response, &detail) == nil {
					exists = detail.Error.Type == "resource_already_exists_exception"
				}
			}
			if code != 200 && !exists {
				t.Close()
				return nil, fmt.Errorf("initialize source index: HTTP %d", code)
			}
		}
	} else {
		return nil, fmt.Errorf("source store requires configured ES/MySQL")
	}
	return s, nil
}

func (s *Store) table(kind string) string {
	if s.db != nil {
		return "linkd_event_source_" + kind
	}
	return "linkd_event_source_" + s.namespace + "_" + kind
}

func valid(kind, id string) error {
	if kind != "records" && kind != "releases" {
		return fmt.Errorf("invalid source collection")
	}
	if len(id) > 256 {
		return fmt.Errorf("source identity too long")
	}
	return nil
}

// Close 释放连接。
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	if s.transport != nil {
		s.transport.Close()
	}
	return nil
}

// Get 按 ID 实时读取，token 是后端专属条件写版本。
func (s *Store) Get(ctx context.Context, kind, id string) (json.RawMessage, string, error) {
	if e := valid(kind, id); e != nil {
		return nil, "", e
	}
	if s.db != nil {
		var b []byte
		var v int64
		e := s.db.QueryRowContext(ctx, "SELECT payload,version FROM "+s.table(kind)+" WHERE namespace=? AND id=?", s.namespace, id).Scan(&b, &v)
		if errors.Is(e, sql.ErrNoRows) {
			e = eventsource.ErrNotFound
		}
		return b, strconv.FormatInt(v, 10), e
	}
	code, b, e := s.request(ctx, http.MethodGet, "/"+s.table(kind)+"/_doc/"+url.PathEscape(id), nil)
	if e != nil {
		return nil, "", e
	}
	if code == 404 {
		return nil, "", eventsource.ErrNotFound
	}
	if code != 200 {
		return nil, "", fmt.Errorf("read source: HTTP %d", code)
	}
	var r struct {
		Seq    int64 `json:"_seq_no"`
		Term   int64 `json:"_primary_term"`
		Source struct {
			Payload json.RawMessage `json:"payload"`
		} `json:"_source"`
	}
	e = json.Unmarshal(b, &r)
	return r.Source.Payload, fmt.Sprintf("%d:%d", r.Seq, r.Term), e
}

// Put 执行 create-only 或单对象 CAS，不先读再无条件写。
func (s *Store) Put(ctx context.Context, kind, id, expected string, b json.RawMessage) error {
	if e := valid(kind, id); e != nil {
		return e
	}
	if len(b) > 1<<20 {
		return fmt.Errorf("source document exceeds 1 MiB")
	}
	if s.db != nil {
		var res sql.Result
		var e error
		if expected == "" {
			//nolint:gosec // G202: kind 已限制为两个固定表名，所有业务值使用参数绑定。
			res, e = s.db.ExecContext(ctx, "INSERT INTO "+s.table(kind)+" (namespace,id,version,payload) VALUES(?,?,1,?)", s.namespace, id, []byte(b))
		} else {
			//nolint:gosec // G202: kind 已限制为两个固定表名，所有业务值使用参数绑定。
			res, e = s.db.ExecContext(ctx, "UPDATE "+s.table(kind)+" SET version=version+1,payload=? WHERE namespace=? AND id=? AND version=?", []byte(b), s.namespace, id, expected)
		}
		if e != nil {
			var de *driver.MySQLError
			if errors.As(e, &de) && de.Number == 1062 {
				return eventsource.ErrConflict
			}
			return e
		}
		n, e := res.RowsAffected()
		if e == nil && n != 1 {
			return eventsource.ErrConflict
		}
		return e
	}
	path := "/" + s.table(kind) + "/_doc/" + url.PathEscape(id)
	q := url.Values{"refresh": {"wait_for"}}
	if expected == "" {
		q.Set("op_type", "create")
	} else {
		var seq, term int64
		if _, e := fmt.Sscanf(expected, "%d:%d", &seq, &term); e != nil {
			return e
		}
		q.Set("if_seq_no", strconv.FormatInt(seq, 10))
		q.Set("if_primary_term", strconv.FormatInt(term, 10))
	}
	payload, e := json.Marshal(map[string]any{"id": id, "payload": b})
	if e != nil {
		return e
	}
	code, _, e := s.request(ctx, http.MethodPut, path+"?"+q.Encode(), payload)
	if e != nil {
		return e
	}
	if code == 409 {
		return eventsource.ErrConflict
	}
	if code != 200 && code != 201 {
		return fmt.Errorf("write source: HTTP %d", code)
	}
	return nil
}

// List 返回有界 ID 顺序页；上层周期对账弥补并发列表变化。
func (s *Store) List(ctx context.Context, kind, after string, limit int) ([]json.RawMessage, error) {
	if e := valid(kind, after); e != nil {
		return nil, e
	}
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("source page size must be 1..1000")
	}
	if s.db != nil {
		//nolint:gosec // G202: kind 已限制为两个固定表名，游标和 namespace 均绑定参数。
		rows, e := s.db.QueryContext(ctx, "SELECT payload FROM "+s.table(kind)+" WHERE namespace=? AND id>? ORDER BY id LIMIT ?", s.namespace, after, limit)
		if e != nil {
			return nil, e
		}
		defer func() { _ = rows.Close() }()
		var result []json.RawMessage
		for rows.Next() {
			var b []byte
			if e = rows.Scan(&b); e != nil {
				return nil, e
			}
			result = append(result, b)
		}
		return result, rows.Err()
	}
	req := map[string]any{"size": limit, "sort": []string{"id"}, "query": map[string]any{"range": map[string]any{"id": map[string]any{"gt": after}}}}
	b, e := json.Marshal(req)
	if e != nil {
		return nil, e
	}
	code, b, e := s.request(ctx, http.MethodPost, "/"+s.table(kind)+"/_search", b)
	if e != nil {
		return nil, e
	}
	if code != 200 {
		return nil, fmt.Errorf("list source: HTTP %d", code)
	}
	var r struct {
		Hits struct {
			Hits []struct {
				Source struct {
					Payload json.RawMessage `json:"payload"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return nil, e
	}
	result := make([]json.RawMessage, 0, len(r.Hits.Hits))
	for _, h := range r.Hits.Hits {
		result = append(result, h.Source.Payload)
	}
	return result, nil
}

func (s *Store) request(ctx context.Context, method, path string, b []byte) (int, []byte, error) {
	r, e := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(b))
	if e != nil {
		return 0, nil, e
	}
	r.Header.Set("Content-Type", "application/json")
	response, e := s.transport.Perform(r)
	if e != nil {
		return 0, nil, e
	}
	defer func() { _ = response.Body.Close() }()
	data, e := io.ReadAll(io.LimitReader(response.Body, 16<<20+1))
	if len(data) > 16<<20 {
		return response.StatusCode, nil, fmt.Errorf("source response exceeds limit")
	}
	return response.StatusCode, data, e
}
