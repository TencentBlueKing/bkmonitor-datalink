// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package assembly 为 OneModel 消费方装配独立、有界的只读连接。
package assembly

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/config"
	"linkd/internal/onemodel"
	elasticsearchstore "linkd/internal/store/elasticsearch"
)

// Connections 统一管理 OneModel 读取使用的 SQL 与 ES 连接；停止调用后幂等关闭。
type Connections struct {
	es   *elasticsearchstore.HTTPTransport
	db   *sql.DB
	once sync.Once
	err  error
}

// Close 关闭所拥有的两类连接，不要求未使用的后端可连接。
func (c *Connections) Close() error {
	if c == nil {
		return nil
	}
	c.once.Do(func() {
		if c.es != nil {
			c.es.Close()
		}
		if c.db != nil {
			c.err = c.db.Close()
		}
	})
	return c.err
}

// Open 创建读取器和统一关闭句柄；仅装配，不迁移 schema 或探测启动依赖。
func Open(resource *config.OneModelResource, maxConnections int, timeout time.Duration) (*onemodel.Client, *Connections, error) {
	if resource == nil || maxConnections < 1 || maxConnections > 1024 || timeout <= 0 {
		return nil, nil, fmt.Errorf("invalid onemodel resource or budget")
	}
	if err := (config.ResourcesConfig{OneModel: resource}).Validate(); err != nil {
		return nil, nil, err
	}
	connections := &Connections{}
	if resource.Backend != "doris" || len(resource.Addresses) > 0 {
		transport, err := NewTransport(resource, maxConnections, timeout)
		if err != nil {
			return nil, nil, err
		}
		connections.es = transport
	}
	if resource.Backend != "doris" {
		client, err := onemodel.NewClient(onemodel.ClientConfig{Transport: connections.es, IndexPrefix: resource.IndexPrefix})
		if err != nil {
			_ = connections.Close()
			return nil, nil, err
		}
		return client, connections, nil
	}
	timeout = min(timeout, time.Minute)
	cfg := resource.Doris
	dsn := driver.NewConfig()
	dsn.Net = "tcp"
	dsn.Addr = cfg.Address
	dsn.DBName = cfg.Database
	dsn.User = cfg.Username
	dsn.Passwd = cfg.Password
	dsn.Timeout = timeout
	dsn.ReadTimeout = timeout
	dsn.WriteTimeout = timeout
	dsn.InterpolateParams = true
	dsn.ParseTime = true
	dsn.Loc = time.UTC
	db, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		_ = connections.Close()
		return nil, nil, fmt.Errorf("invalid onemodel Doris connection")
	}
	connections.db = db
	db.SetMaxOpenConns(maxConnections)
	db.SetMaxIdleConns(maxConnections)
	db.SetConnMaxLifetime(30 * time.Minute)
	client, err := onemodel.NewDorisClient(onemodel.DorisConfig{Reader: db, InstanceTable: cfg.InstanceTable, EdgeTable: cfg.EdgeTable, Timeout: timeout, TopologyTransport: connections.es, IndexPrefix: resource.IndexPrefix})
	if err != nil {
		_ = connections.Close()
		return nil, nil, err
	}
	return client, connections, nil
}

// NewTransport 为每个消费方创建有界且支持取消的 ES HTTP 连接池。
func NewTransport(resource *config.OneModelResource, maxConnections int, timeout time.Duration) (*elasticsearchstore.HTTPTransport, error) {
	if resource == nil {
		return nil, fmt.Errorf("resources.onemodel is required")
	}
	cfg := elasticsearchstore.HTTPTransportConfig{Addresses: append([]string(nil), resource.Addresses...), APIKey: resource.APIKey, Timeout: timeout, MaxConnectionsPerHost: maxConnections}
	if resource.BasicAuth != nil {
		cfg.BasicUsername = resource.BasicAuth.Username
		cfg.BasicPassword = resource.BasicAuth.Password
	}
	return elasticsearchstore.NewHTTPTransport(cfg)
}
