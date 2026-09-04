// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package assembly

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	driver "github.com/go-sql-driver/mysql"
	"linkd/internal/config"
	"linkd/internal/store"
	elasticsearchstore "linkd/internal/store/elasticsearch"
	mysqlstore "linkd/internal/store/mysql"
)

const (
	databaseMaxLifetime                = 30 * time.Minute
	minElasticsearchConnectionsPerHost = 4
	maxElasticsearchConnectionsPerHost = 1024
)

// Runtime 持有一个已经完成连接检查和 schema 初始化的 Repository 及其资源。
type Runtime struct {
	Repository store.Repository
	Backend    string
	close      func() error
	closeOnce  sync.Once
	closeErr   error
}

// ElasticsearchManagerRuntime 持有 Elasticsearch 单轮管理操作及其 HTTP 连接资源。
type ElasticsearchManagerRuntime struct {
	Manager   *elasticsearchstore.Manager
	close     func()
	closeOnce sync.Once
}

// ValidateElasticsearchManagerConfig 校验创建 Elasticsearch 管理任务所需的后端配置。
func ValidateElasticsearchManagerConfig(cfg config.Config) error {
	if cfg.Storage == nil ||
		cfg.Storage.Repository != config.RepositoryTypeElasticsearch ||
		cfg.Storage.Elasticsearch == nil {
		return fmt.Errorf("elasticsearch repository config is required")
	}
	return nil
}

// Close 释放索引管理器的 HTTP 空闲连接；重复调用安全。
func (r *ElasticsearchManagerRuntime) Close() {
	if r == nil {
		return
	}
	r.closeOnce.Do(func() {
		if r.close != nil {
			r.close()
		}
	})
}

// Open 根据 storage.repository 选择 MySQL 或 Elasticsearch，并完成启动所需的 schema 初始化。
func Open(
	ctx context.Context,
	cfg config.StorageConfig,
	maxConnections int,
) (*Runtime, error) {
	if ctx == nil {
		return nil, fmt.Errorf("open repository: context must not be nil")
	}
	cfg = cfg.WithDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("open repository: %w", err)
	}
	if maxConnections < 1 {
		return nil, fmt.Errorf("open repository: max connections must be positive")
	}
	switch cfg.Repository {
	case config.RepositoryTypeMySQL:
		return openMySQL(ctx, *cfg.MySQL, maxConnections)
	case config.RepositoryTypeElasticsearch:
		return openElasticsearch(ctx, *cfg.Elasticsearch, maxConnections)
	default:
		return nil, fmt.Errorf("open repository: storage.repository is required")
	}
}

// Close 释放 Repository 持有的连接资源；重复调用安全。
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}
	r.closeOnce.Do(func() {
		if r.close != nil {
			r.closeErr = r.close()
		}
	})
	return r.closeErr
}

func openMySQL(ctx context.Context, cfg config.MySQLConfig, maxConnections int) (*Runtime, error) {
	dsn := driver.NewConfig()
	dsn.User = cfg.Username
	dsn.Passwd = cfg.Password
	dsn.Net = "tcp"
	dsn.Addr = cfg.Address
	dsn.DBName = cfg.Database
	dsn.ParseTime = true
	dsn.Loc = time.UTC
	database, err := sql.Open("mysql", dsn.FormatDSN())
	if err != nil {
		return nil, fmt.Errorf("open mysql repository: %w", err)
	}
	database.SetMaxOpenConns(max(maxConnections, 8))
	database.SetMaxIdleConns(max(maxConnections/2, 2))
	database.SetConnMaxLifetime(databaseMaxLifetime)
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("connect mysql repository: %w", err)
	}
	repository, err := mysqlstore.New(database)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("initialize mysql repository: %w", err)
	}
	if err := repository.EnsureSchema(ctx); err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("initialize mysql repository schema: %w", err)
	}
	return &Runtime{
		Repository: repository,
		Backend:    config.RepositoryTypeMySQL,
		close: func() error {
			if err := database.Close(); err != nil {
				return fmt.Errorf("close mysql repository: %w", err)
			}
			return nil
		},
	}, nil
}

func openElasticsearch(ctx context.Context, cfg config.ElasticsearchConfig, maxConnections int) (*Runtime, error) {
	repository, router, transport, err := newElasticsearchComponents(cfg, maxConnections)
	if err != nil {
		return nil, err
	}
	manager, err := newElasticsearchManager(repository, router, cfg)
	if err != nil {
		transport.Close()
		return nil, err
	}
	if err := manager.VerifyReady(ctx); err != nil {
		transport.Close()
		return nil, fmt.Errorf("verify elasticsearch data plane: %w", err)
	}
	return &Runtime{
		Repository: repository,
		Backend:    config.RepositoryTypeElasticsearch,
		close: func() error {
			transport.Close()
			return nil
		},
	}, nil
}

// OpenElasticsearchManager 创建 Schema 与 Active 资源对账、时间桶维护和 Alert 异步归档共享的运行时。
func OpenElasticsearchManager(
	ctx context.Context,
	cfg config.ElasticsearchConfig,
	maxConnections int,
) (*ElasticsearchManagerRuntime, error) {
	if ctx == nil {
		return nil, fmt.Errorf("open elasticsearch manager: context must not be nil")
	}
	if maxConnections < 1 {
		return nil, fmt.Errorf("open elasticsearch manager: max connections must be positive")
	}
	repository, router, transport, err := newElasticsearchComponents(cfg, maxConnections)
	if err != nil {
		return nil, err
	}
	manager, err := newElasticsearchManager(repository, router, cfg)
	if err != nil {
		transport.Close()
		return nil, err
	}
	return &ElasticsearchManagerRuntime{Manager: manager, close: transport.Close}, nil
}

func newElasticsearchComponents(
	cfg config.ElasticsearchConfig,
	maxConnections int,
) (*elasticsearchstore.Repository, *elasticsearchstore.BucketRouter, *elasticsearchstore.HTTPTransport, error) {
	cfg.TimePartition = cfg.TimePartition.WithDefaults()
	transportConfig := elasticsearchstore.HTTPTransportConfig{
		Addresses:             append([]string(nil), cfg.Addresses...),
		APIKey:                cfg.APIKey,
		MaxConnectionsPerHost: elasticsearchConnectionBudget(maxConnections),
	}
	if cfg.BasicAuth != nil {
		transportConfig.BasicUsername = cfg.BasicAuth.Username
		transportConfig.BasicPassword = cfg.BasicAuth.Password
	}
	transport, err := elasticsearchstore.NewHTTPTransport(transportConfig)
	if err != nil {
		return nil, nil, nil, err
	}
	router, err := elasticsearchstore.NewBucketRouter(cfg.IndexPrefix, elasticsearchstore.BucketConfig{
		EventBucketDays:            cfg.TimePartition.EventBucketDays,
		AlertHistoryBucketDays:     cfg.TimePartition.AlertHistoryBucketDays,
		AlertLogBucketDays:         cfg.TimePartition.AlertLogBucketDays,
		MaxFutureSkew:              cfg.TimePartition.MaxFutureSkew(),
		ActiveAlertRefreshInterval: cfg.ActiveAlertRefreshInterval(),
		NumberOfReplicas:           cfg.NumberOfReplicas,
	})
	if err != nil {
		transport.Close()
		return nil, nil, nil, err
	}
	repository, err := elasticsearchstore.New(transport, router, elasticsearchstore.DefaultConfig())
	if err != nil {
		transport.Close()
		return nil, nil, nil, fmt.Errorf("initialize elasticsearch repository: %w", err)
	}
	return repository, router, transport, nil
}

func elasticsearchConnectionBudget(requested int) int {
	return min(max(requested, minElasticsearchConnectionsPerHost), maxElasticsearchConnectionsPerHost)
}

func newElasticsearchManager(
	repository *elasticsearchstore.Repository,
	router *elasticsearchstore.BucketRouter,
	cfg config.ElasticsearchConfig,
) (*elasticsearchstore.Manager, error) {
	partition := cfg.TimePartition.WithDefaults()
	return elasticsearchstore.NewManager(repository, router, elasticsearchstore.ManagerConfig{
		PrecreatePastBuckets:   partition.PrecreatePastBuckets,
		PrecreateFutureBuckets: partition.PrecreateFutureBuckets,
		MaxBucketsPerEntity:    partition.MaxBucketsPerEntity,
	})
}

// JoinCloseError 将关闭失败追加到进程已有返回错误，便于 defer 保留两类失败。
func JoinCloseError(runErr *error, runtime *Runtime) {
	if runtime == nil || runErr == nil {
		return
	}
	if err := runtime.Close(); err != nil {
		*runErr = errors.Join(*runErr, err)
	}
}
