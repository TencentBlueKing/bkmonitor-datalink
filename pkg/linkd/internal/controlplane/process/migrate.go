// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"fmt"
	"time"

	"linkd/internal/config"
	sourcestore "linkd/internal/eventsource/storage"
	"linkd/internal/redisclient"
	repositoryassembly "linkd/internal/store/assembly"
)

// Migrate 执行控制面的有界一次性初始化：校验配置与认证、检查 Redis、初始化 Repository 和来源集合。
// 可能部分生效，失败后可重试已有幂等初始化；不启动 API、任务、归档或调度，不申请中心资格，
// 不导入来源或重建 Redis 协调历史。已有业务数据不会被清空；这不是历史 schema 自动升级器。
func Migrate(ctx context.Context, cfg config.Config) error {
	if ctx == nil {
		return fmt.Errorf("migrate: context must not be nil")
	}
	// 即使库调用者没有传入 deadline，也不允许初始化无限等待。
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	return runMigration(ctx, cfg, migrationSteps{
		checkRedis:        checkMigrationRedis,
		prepareRepository: prepareMigrationRepository,
		prepareSources:    prepareMigrationSources,
	})
}

type migrationSteps struct {
	checkRedis        func(context.Context, config.Config) error
	prepareRepository func(context.Context, config.Config) error
	prepareSources    func(context.Context, config.Config) error
}

func runMigration(ctx context.Context, cfg config.Config, steps migrationSteps) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("migrate configuration: %w", err)
	}
	if err := ValidateConfig(cfg); err != nil {
		return fmt.Errorf("migrate control plane: %w", err)
	}
	if err := validateDispatchConfig(cfg); err != nil {
		return fmt.Errorf("migrate control plane: %w", err)
	}
	if cfg.Storage.Repository != config.RepositoryTypeMySQL && cfg.Storage.Repository != config.RepositoryTypeElasticsearch {
		return fmt.Errorf("migrate: storage.repository must select mysql or elasticsearch")
	}
	// Redis 连通性先检查；失败时不开始持久存储初始化。各阶段共享同一个总 deadline。
	for _, step := range []struct {
		name string
		run  func(context.Context, config.Config) error
	}{
		{"check Redis", steps.checkRedis},
		{"prepare repository", steps.prepareRepository},
		{"prepare source collections", steps.prepareSources},
	} {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("migrate %s: %w", step.name, err)
		}
		if err := step.run(ctx, cfg); err != nil {
			return fmt.Errorf("migrate %s: %w", step.name, err)
		}
	}
	return nil
}

func checkMigrationRedis(ctx context.Context, cfg config.Config) (runErr error) {
	options := cfg.Storage.Redis.ClientOptions()
	options.ContextTimeoutEnabled = true
	client, err := redisclient.New(options)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, client.Close()) }()
	return client.Ping(ctx).Err()
}

func prepareMigrationRepository(ctx context.Context, cfg config.Config) error {
	if hasElasticsearchTask(cfg) {
		runtime, err := repositoryassembly.OpenElasticsearchManager(ctx, *cfg.Storage.Elasticsearch, elasticsearchPrepareConnections)
		if err != nil {
			return err
		}
		defer runtime.Close()
		return prepareElasticsearchDataPlane(ctx, runtime.Manager)
	}
	// MySQL 使用与正式 Repository 启动相同的 EnsureSchema，不另建迁移表或 DDL 副本。
	runtime, err := repositoryassembly.Open(ctx, *cfg.Storage, elasticsearchPrepareConnections)
	if err != nil {
		return err
	}
	return runtime.Close()
}

func prepareMigrationSources(ctx context.Context, cfg config.Config) error {
	docs, err := sourcestore.Open(ctx, *cfg.Storage, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		return err
	}
	return docs.Close()
}

// validateDispatchConfig 与正式控制面共用启动认证前提，不能让 Job 使用有效但不可启动的配置。
func validateDispatchConfig(cfg config.Config) error {
	d := cfg.Dispatch.WithDefaults()
	if d.APIToken == "" || d.WorkerToken == "" || d.APIToken == d.WorkerToken {
		return fmt.Errorf("dispatch requires distinct api_token and worker_token")
	}
	if cfg.Storage == nil || cfg.Storage.Redis == nil {
		return fmt.Errorf("dispatch requires source storage and Redis")
	}
	return nil
}
