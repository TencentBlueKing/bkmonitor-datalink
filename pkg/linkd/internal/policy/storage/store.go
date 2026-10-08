// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package storage 将策略配置和有界控制记录映射到 ES/MySQL 单对象存储。
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
	"linkd/internal/policy"
	"linkd/internal/shieldcheck"
	es "linkd/internal/store/elasticsearch"
)

// Store 持有策略管理专用的有界连接资源。
type Store struct {
	db          *sql.DB
	transport   *es.HTTPTransport
	namespace   string
	indexPrefix string
}

// Open 初始化策略配置、合并运行、屏蔽复查及抑制清理记录集合，不清理历史或业务数据。
func Open(ctx context.Context, c config.StorageConfig, deployment string) (*Store, error) {
	hash := sha256.Sum256([]byte(deployment))
	s := &Store{namespace: hex.EncodeToString(hash[:])}
	c = c.WithDefaults()
	if c.Elasticsearch != nil {
		s.indexPrefix = c.Elasticsearch.IndexPrefix
	}
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
		for _, name := range policyCollections() {
			extra := ""
			if hasRequestWork(name) {
				extra = ", work TINYINT NOT NULL, KEY idx_shield_request_work(namespace,work,id)"
			}
			_, e = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.table(name)+" (namespace VARBINARY(256) NOT NULL, id VARBINARY(256) NOT NULL, version BIGINT NOT NULL, payload LONGBLOB NOT NULL, PRIMARY KEY(namespace,id)"+extra+")")
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
		for _, name := range policyCollections() {
			properties := map[string]any{"id": map[string]any{"type": "keyword"}, "payload": map[string]any{"type": "object", "enabled": false}}
			if hasRequestWork(name) {
				properties["work"] = map[string]any{"type": "boolean"}
			}
			mapping, _ := json.Marshal(map[string]any{"mappings": map[string]any{"dynamic": "strict", "properties": properties}})
			code, response, e := s.request(ctx, http.MethodPut, "/"+s.table(name), mapping)
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
				return nil, fmt.Errorf("initialize policy index: HTTP %d", code)
			}
		}
	} else {
		return nil, fmt.Errorf("policy store requires configured ES/MySQL")
	}
	return s, nil
}

func (s *Store) table(kind string) string {
	if s.db != nil {
		return "linkd_policy_" + kind
	}
	return s.indexPrefix + "-policy-" + s.namespace + "-" + kind
}

func valid(kind, id string) error {
	if kind != "records" && kind != "releases" && kind != "operations" && kind != "merge_decisions" && kind != "merge_members" && kind != "merge_relations" && kind != "merge_relation_refs" && kind != "shield_checks" && kind != "shield_requests" && kind != "suppression_cleanups" && kind != "suppression_requests" && kind != "merge_requests" {
		return fmt.Errorf("invalid policy collection")
	}
	if len(id) > 256 {
		return fmt.Errorf("policy identity too long")
	}
	return nil
}

func policyCollections() []string {
	return []string{"records", "releases", "operations", "merge_decisions", "merge_members", "merge_relations", "merge_relation_refs", "shield_checks", "shield_requests", "suppression_cleanups", "suppression_requests", "merge_requests"}
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
			e = policy.ErrNotFound
		}
		return b, strconv.FormatInt(v, 10), e
	}
	code, b, e := s.request(ctx, http.MethodGet, "/"+s.table(kind)+"/_doc/"+url.PathEscape(id), nil)
	if e != nil {
		return nil, "", e
	}
	if code == 404 {
		return nil, "", policy.ErrNotFound
	}
	if code != 200 {
		return nil, "", fmt.Errorf("read policy: HTTP %d", code)
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
// 合并记录只等待持久化确认，后续步骤按实时 Get 推进；其 List 允许 ES 刷新延迟，由周期扫描补读。
// 配置/发布记录仍等待搜索可见，不能让配置消费者把已经发布的策略误判为消失。
func (s *Store) Put(ctx context.Context, kind, id, expected string, b json.RawMessage) error {
	if e := valid(kind, id); e != nil {
		return e
	}
	if kind == "suppression_cleanups" && len(b) > 2<<20 {
		return policy.ErrInvalid
	}
	if len(b) > 8<<20 {
		return fmt.Errorf("policy document exceeds 8 MiB")
	}
	if (kind == "shield_checks" || hasRequestWork(kind)) && len(b) > controlRecordLimit(kind) {
		return fmt.Errorf("control check record exceeds byte budget")
	}
	work := false
	if hasRequestWork(kind) {
		var request struct {
			State string `json:"state"`
		}
		if json.Unmarshal(b, &request) != nil || (request.State != "pending" && request.State != "completed" && request.State != "failed" && request.State != "superseded") {
			return fmt.Errorf("%w: invalid control request state", policy.ErrInvalid)
		}
		work = request.State == "pending"
	}
	if s.db != nil {
		var res sql.Result
		var e error
		if hasRequestWork(kind) {
			if expected == "" {
				//nolint:gosec // G202: kind 已限制为固定请求集合，业务值使用参数绑定。
				res, e = s.db.ExecContext(ctx, "INSERT INTO "+s.table(kind)+"(namespace,id,version,payload,work) VALUES(?,?,1,?,?)", s.namespace, id, []byte(b), work)
			} else {
				//nolint:gosec // G202: kind 已限制为固定请求集合，业务值使用参数绑定。
				res, e = s.db.ExecContext(ctx, "UPDATE "+s.table(kind)+" SET version=version+1,payload=?,work=? WHERE namespace=? AND id=? AND version=?", []byte(b), work, s.namespace, id, expected)
			}
		} else if expected == "" {
			//nolint:gosec // G202: kind 已限制为固定集合表名，所有业务值使用参数绑定。
			res, e = s.db.ExecContext(ctx, "INSERT INTO "+s.table(kind)+" (namespace,id,version,payload) VALUES(?,?,1,?)", s.namespace, id, []byte(b))
		} else {
			//nolint:gosec // G202: kind 已限制为固定集合表名，所有业务值使用参数绑定。
			res, e = s.db.ExecContext(ctx, "UPDATE "+s.table(kind)+" SET version=version+1,payload=? WHERE namespace=? AND id=? AND version=?", []byte(b), s.namespace, id, expected)
		}
		if e != nil {
			var de *driver.MySQLError
			if errors.As(e, &de) && de.Number == 1062 {
				return policy.ErrConflict
			}
			return e
		}
		n, e := res.RowsAffected()
		if e == nil && n != 1 {
			return policy.ErrConflict
		}
		return e
	}
	path := "/" + s.table(kind) + "/_doc/" + url.PathEscape(id)
	q := url.Values{"refresh": {"wait_for"}}
	if strings.HasPrefix(kind, "merge_") {
		// 每个成员等待一次刷新会把有界任务的期限消耗在搜索可见性上；业务确认始终使用实时 GET。
		q.Set("refresh", "false")
	}
	if kind == "suppression_cleanups" || kind == "shield_checks" || (hasRequestWork(kind) && !work) {
		q.Set("refresh", "false")
	}
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
	doc := map[string]any{"id": id, "payload": b}
	if hasRequestWork(kind) {
		doc["work"] = work
	}
	payload, e := json.Marshal(doc)
	if e != nil {
		return e
	}
	code, _, e := s.request(ctx, http.MethodPut, path+"?"+q.Encode(), payload)
	if e != nil {
		return e
	}
	if code == 409 {
		return policy.ErrConflict
	}
	if code != 200 && code != 201 {
		return fmt.Errorf("write policy: HTTP %d", code)
	}
	return nil
}

// List 返回有界 ID 顺序页；上层周期对账弥补并发列表变化。
func (s *Store) List(ctx context.Context, kind, prefix, after string, limit int) ([]json.RawMessage, error) {
	if e := valid(kind, after); e != nil {
		return nil, e
	}
	if e := valid(kind, prefix); e != nil {
		return nil, e
	}
	if limit < 1 || limit > policy.MaxPageSize {
		return nil, fmt.Errorf("policy page size must be 1..16")
	}
	if s.db != nil {
		//nolint:gosec // G202: kind 已限制为固定集合表名，游标和 namespace 均绑定参数。
		rows, e := s.db.QueryContext(ctx, "SELECT payload FROM "+s.table(kind)+" WHERE namespace=? AND id>? AND id>=? AND id<? ORDER BY id LIMIT ?", s.namespace, after, prefix, prefix+string([]byte{255}), limit)
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
	req := map[string]any{"size": limit, "sort": []string{"id"}, "query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"range": map[string]any{"id": map[string]any{"gt": after}}}, map[string]any{"prefix": map[string]any{"id": prefix}}}}}}
	b, e := json.Marshal(req)
	if e != nil {
		return nil, e
	}
	code, b, e := s.request(ctx, http.MethodPost, "/"+s.table(kind)+"/_search", b)
	if e != nil {
		return nil, e
	}
	if code != 200 {
		return nil, fmt.Errorf("list policy: HTTP %d", code)
	}
	var r struct {
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
	if e = json.Unmarshal(b, &r); e != nil {
		return nil, e
	}
	// 部分结果不能参与策略配置对账，否则会将已有策略误判为消失。
	if r.TimedOut || r.Shards.Failed > 0 || r.Hits.Hits == nil || len(r.Hits.Hits) > limit {
		return nil, fmt.Errorf("list policy: incomplete search timed_out=%t failed_shards=%d", r.TimedOut, r.Shards.Failed)
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
	data, e := io.ReadAll(io.LimitReader(response.Body, 128<<20+1))
	if len(data) > 128<<20 {
		return response.StatusCode, nil, fmt.Errorf("policy response exceeds limit")
	}
	return response.StatusCode, data, e
}

func hasRequestWork(kind string) bool {
	return kind == "shield_requests" || kind == "suppression_requests" || kind == "merge_requests"
}

// 屏蔽复查包含原绑定及新时间规则的有界诊断，其余控制请求保留原来的 64 KiB 上限。
func controlRecordLimit(kind string) int {
	if kind == "shield_checks" || kind == "shield_requests" {
		return shieldcheck.MaxDocumentBytes
	}
	return 64 << 10
}
