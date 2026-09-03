// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"linkd/internal/config"
	"linkd/internal/telemetry"
)

func TestValidateConfigRequiresLifecycleDependencies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    config.Config
		wantError string
	}{
		{name: "lifecycle", config: config.Config{}, wantError: "lifecycle config is required"},
		{
			name:      "storage",
			config:    config.Config{Lifecycle: validLifecycleConfig()},
			wantError: "storage config is required",
		},
		{
			name: "repository",
			config: config.Config{
				Lifecycle: validLifecycleConfig(),
				Storage:   &config.StorageConfig{Redis: validRedisConfig()},
			},
			wantError: "storage.repository is required",
		},
		{
			name: "redis",
			config: config.Config{
				Lifecycle: validLifecycleConfig(),
				Storage: &config.StorageConfig{
					Repository: config.RepositoryTypeMySQL,
					MySQL:      validMySQLConfig(),
				},
			},
			wantError: "storage.redis is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateConfig(test.config)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("ValidateConfig() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestRunRejectsCanceledContextBeforeConnecting(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := Run(
		ctx,
		config.Config{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		&telemetry.Runtime{},
	)
	if err == nil || !strings.Contains(err.Error(), "lifecycle config is required") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestNewConsumerName(t *testing.T) {
	t.Parallel()

	got := newConsumerName("linkd lifecycle", "host/name", 42)
	if got != "linkd-lifecycle-host-name-42" {
		t.Fatalf("newConsumerName() = %q", got)
	}
	long := newConsumerName(strings.Repeat("x", 300), "host", 42)
	if len(long) > consumerNameLimit {
		t.Fatalf("newConsumerName() length = %d", len(long))
	}
}

func validLifecycleConfig() *config.LifecycleConfig {
	value := config.LifecycleConfig{
		Output: config.LifecycleOutputConfig{
			Kafka: &config.LifecycleKafkaConfig{
				Brokers: []string{"kafka.example.com:9092"},
				Topic:   "alerts",
			},
		},
	}.WithDefaults()
	return &value
}

func validMySQLConfig() *config.MySQLConfig {
	return &config.MySQLConfig{
		Address:  "mysql.example.com:3306",
		Database: "linkd",
		Username: "linkd",
	}
}

func validRedisConfig() *config.RedisConfig {
	return &config.RedisConfig{Address: "redis.example.com:6379"}
}
