// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package storage 保存动态配置最后有效快照，复用 Linkd 的持久存储连接配置但使用独立集合。
package storage

import (
	"bytes"
	"context"
	"database/sql"
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
	"linkd/internal/dynamicconfig"
	es "linkd/internal/store/elasticsearch"
)

const table = "linkd_dynamic_config_snapshots"

// Store 只保存每个作用域的最后有效快照，初始化失败可由下一次同步重试。
type Store struct {
	db          *sql.DB
	transport   *es.HTTPTransport
	index       string
	initialized bool
}

// New 构造无网络副作用的快照仓储；调用者负责 Close。
func New(c config.StorageConfig) (*Store, error) {
	s := &Store{}
	c = c.WithDefaults()
	switch c.Repository {
	case config.RepositoryTypeMySQL:
		if c.MySQL == nil {
			return nil, fmt.Errorf("snapshot mysql config required")
		}
		x := driver.NewConfig()
		x.Net = "tcp"
		x.Addr = c.MySQL.Address
		x.DBName = c.MySQL.Database
		x.User = c.MySQL.Username
		x.Passwd = c.MySQL.Password
		x.Timeout = 3 * time.Second
		db, err := sql.Open("mysql", x.FormatDSN())
		if err != nil {
			return nil, err
		}
		s.db = db
		db.SetMaxOpenConns(2)
		db.SetMaxIdleConns(1)
		db.SetConnMaxLifetime(30 * time.Minute)
	case config.RepositoryTypeElasticsearch:
		if c.Elasticsearch == nil {
			return nil, fmt.Errorf("snapshot elasticsearch config required")
		}
		x := c.Elasticsearch
		tc := es.HTTPTransportConfig{Addresses: x.Addresses, APIKey: x.APIKey, MaxConnectionsPerHost: 2}
		if x.BasicAuth != nil {
			tc.BasicUsername = x.BasicAuth.Username
			tc.BasicPassword = x.BasicAuth.Password
		}
		t, err := es.NewHTTPTransport(tc)
		if err != nil {
			return nil, err
		}
		s.transport = t
		s.index = x.IndexPrefix + "_dynamic_config_snapshots"
	default:
		return nil, fmt.Errorf("snapshot store requires mysql or elasticsearch")
	}
	return s, nil
}

// EnsureSchema 幂等初始化独立快照集合，不连接任何上游。由控制面串行调用。
func (s *Store) EnsureSchema(ctx context.Context) error {
	if s.initialized {
		return nil
	}
	if s.db != nil {
		_, err := s.db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+table+" (id VARBINARY(64) PRIMARY KEY, version BIGINT NOT NULL, payload LONGBLOB NOT NULL)")
		if err != nil {
			return err
		}
	} else {
		code, b, err := s.request(ctx, http.MethodPut, "/"+s.index, []byte(`{"mappings":{"dynamic":"strict","properties":{"payload":{"type":"object","enabled":false}}}}`))
		if err != nil {
			return err
		}
		var result struct {
			Error struct {
				Type string `json:"type"`
			} `json:"error"`
		}
		exists := code == 400 && json.Unmarshal(b, &result) == nil && result.Error.Type == "resource_already_exists_exception"
		if code != http.StatusOK && !exists {
			return fmt.Errorf("initialize snapshot index: HTTP %d", code)
		}
	}
	s.initialized = true
	return nil
}

// Load 实时读取完整快照与 CAS token。
func (s *Store) Load(ctx context.Context, key string) (dynamicconfig.Record, string, error) {
	var record dynamicconfig.Record
	if err := s.EnsureSchema(ctx); err != nil {
		return record, "", err
	}
	var b []byte
	var token string
	if s.db != nil {
		var version int64
		err := s.db.QueryRowContext(ctx, "SELECT payload,version FROM "+table+" WHERE id=?", key).Scan(&b, &version)
		if errors.Is(err, sql.ErrNoRows) {
			err = dynamicconfig.ErrNotFound
		}
		if err != nil {
			return record, "", err
		}
		token = strconv.FormatInt(version, 10)
	} else {
		code, body, err := s.request(ctx, http.MethodGet, "/"+s.index+"/_doc/"+url.PathEscape(key), nil)
		if err != nil {
			return record, "", err
		}
		if code == 404 {
			return record, "", dynamicconfig.ErrNotFound
		}
		if code != 200 {
			return record, "", fmt.Errorf("read snapshot: HTTP %d", code)
		}
		var result struct {
			Seq    int64 `json:"_seq_no"`
			Term   int64 `json:"_primary_term"`
			Source struct {
				Payload json.RawMessage `json:"payload"`
			} `json:"_source"`
		}
		if err = json.Unmarshal(body, &result); err != nil {
			return record, "", err
		}
		b = result.Source.Payload
		token = fmt.Sprintf("%d:%d", result.Seq, result.Term)
	}
	if len(b) > 1<<20 {
		return record, token, fmt.Errorf("snapshot exceeds size limit")
	}
	if err := json.Unmarshal(b, &record); err != nil {
		return record, token, err
	}
	return record, token, nil
}

// Save 原子保存已校验的整份快照；调用方只能在成功后发布。
func (s *Store) Save(ctx context.Context, key, expected string, r dynamicconfig.Record) error {
	if err := s.EnsureSchema(ctx); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	if len(b) > 1<<20 {
		return fmt.Errorf("snapshot exceeds size limit")
	}
	if s.db != nil {
		var result sql.Result
		if expected == "" {
			result, err = s.db.ExecContext(ctx, "INSERT INTO "+table+"(id,version,payload) VALUES(?,1,?)", key, b)
		} else {
			result, err = s.db.ExecContext(ctx, "UPDATE "+table+" SET version=version+1,payload=? WHERE id=? AND version=?", b, key, expected)
		}
		if err != nil {
			var conflict *driver.MySQLError
			if errors.As(err, &conflict) && conflict.Number == 1062 {
				return dynamicconfig.ErrConflict
			}
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return dynamicconfig.ErrConflict
		}
		return nil
	}
	query := url.Values{}
	if expected == "" {
		query.Set("op_type", "create")
	} else {
		var seq, term int64
		if _, err = fmt.Sscanf(expected, "%d:%d", &seq, &term); err != nil {
			return err
		}
		query.Set("if_seq_no", strconv.FormatInt(seq, 10))
		query.Set("if_primary_term", strconv.FormatInt(term, 10))
	}
	payload, err := json.Marshal(map[string]json.RawMessage{"payload": b})
	if err != nil {
		return err
	}
	code, _, err := s.request(ctx, http.MethodPut, "/"+s.index+"/_doc/"+url.PathEscape(key)+"?"+query.Encode(), payload)
	if err != nil {
		return err
	}
	if code == 409 {
		return dynamicconfig.ErrConflict
	}
	if code != 200 && code != 201 {
		return fmt.Errorf("write snapshot: HTTP %d", code)
	}
	return nil
}

// Close 释放仓储拥有的连接。
func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	if s.transport != nil {
		s.transport.Close()
	}
	return nil
}

func (s *Store) request(ctx context.Context, method, path string, payload []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := s.transport.Perform(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if err == nil && len(b) > 2<<20 {
		err = fmt.Errorf("snapshot response exceeds size limit")
	}
	return response.StatusCode, b, err
}
