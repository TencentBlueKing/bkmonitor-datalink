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
	"reflect"
	"strings"
	"testing"

	"linkd/internal/config"
)

func migrationTestConfig() config.Config {
	cfg := config.Default()
	storage := (config.StorageConfig{
		Repository: config.RepositoryTypeMySQL,
		MySQL:      &config.MySQLConfig{Address: "127.0.0.1:3306", Database: "linkd", Username: "test"},
		Redis:      &config.RedisConfig{Address: "127.0.0.1:6379"},
	}).WithDefaults()
	cfg.Storage = &storage
	cfg.Dispatch.APIToken = "test-api-token"
	cfg.Dispatch.WorkerToken = "test-worker-token"
	return cfg
}

func TestMigrationStages(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("test dependency failure")
	for _, failedAt := range []string{"", "redis", "repository", "sources"} {
		t.Run("failure_"+failedAt, func(t *testing.T) {
			var calls []string
			makeStep := func(name string) func(context.Context, config.Config) error {
				return func(ctx context.Context, _ config.Config) error {
					if ctx == nil {
						t.Fatal("missing context")
					}
					calls = append(calls, name)
					if name == failedAt {
						return sentinel
					}
					return nil
				}
			}
			steps := migrationSteps{makeStep("redis"), makeStep("repository"), makeStep("sources")}
			err := runMigration(t.Context(), migrationTestConfig(), steps)
			expected := []string{"redis", "repository", "sources"}
			if failedAt != "" {
				for i, name := range expected {
					if name == failedAt {
						expected = expected[:i+1]
						break
					}
				}
				if !errors.Is(err, sentinel) {
					t.Fatalf("lost failure chain: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(calls, expected) {
				t.Fatalf("calls: %v, want %v", calls, expected)
			}
			if failedAt == "" {
				// 一次性入口不持有进程状态，重复执行仍按相同顺序调用幂等初始化。
				if err := runMigration(t.Context(), migrationTestConfig(), steps); err != nil {
					t.Fatal(err)
				}
				if len(calls) != 6 {
					t.Fatal("second initialization not executed")
				}
			}
		})
	}
}

func TestMigrationChecksConfigurationBeforeIO(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*config.Config)
	}{
		{"missing API token", func(c *config.Config) { c.Dispatch.APIToken = "" }},
		{"same tokens", func(c *config.Config) { c.Dispatch.WorkerToken = c.Dispatch.APIToken }},
		{"missing Redis", func(c *config.Config) { c.Storage.Redis = nil }},
		{"missing storage", func(c *config.Config) { c.Storage = nil }},
		{"invalid repository", func(c *config.Config) { c.Storage.Repository = "invalid" }},
		{"invalid worker config", func(c *config.Config) { c.Worker.MaxConcurrency = -1 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := migrationTestConfig()
			tc.change(&cfg)
			invoked := false
			step := func(context.Context, config.Config) error { invoked = true; return nil }
			err := runMigration(t.Context(), cfg, migrationSteps{step, step, step})
			if err == nil || invoked {
				t.Fatalf("error=%v, invoked=%v", err, invoked)
			}
			if strings.Contains(err.Error(), "test-api-token") {
				t.Fatal("credential leaked")
			}
		})
	}
}

func TestMigrationCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	first := func(context.Context, config.Config) error { calls++; cancel(); return nil }
	later := func(context.Context, config.Config) error { calls++; return nil }
	err := runMigration(ctx, migrationTestConfig(), migrationSteps{first, later, later})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("error=%v calls=%d", err, calls)
	}
	//nolint:staticcheck // SA1012: 专门验证公开入口拒绝 nil Context 的失败路径。
	if err := Migrate(nil, migrationTestConfig()); err == nil {
		t.Fatal("nil context accepted")
	}
}
