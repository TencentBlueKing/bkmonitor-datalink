// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
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
	"time"

	"linkd/internal/config"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich/assembly"
	"linkd/internal/telemetry"
)

func TestOneModelTransportRejectsExcessiveConnectionBudget(t *testing.T) {
	t.Parallel()
	dataSource := &config.EnrichElasticsearchDataSource{Addresses: []string{"http://127.0.0.1:9200"}}
	if _, err := newOneModelTransport(dataSource, 1025, time.Second); err == nil {
		t.Fatal("newOneModelTransport() accepted an excessive connection budget")
	}
}

func TestOneModelTransportUsesConnectionBudget(t *testing.T) {
	t.Parallel()
	dataSource := &config.EnrichElasticsearchDataSource{Addresses: []string{"http://127.0.0.1:9200"}}
	transport, err := newOneModelTransport(dataSource, 36, 30*time.Second)
	if err != nil {
		t.Fatalf("newOneModelTransport() error = %v", err)
	}
	transport.Close()
}

func TestReleaseRequiresMySQLDataSource(t *testing.T) {
	t.Parallel()
	source := config.EventSource{
		EventSourceID: "built_in_bk",
		Enrich:        config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "source"}}},
	}
	if err := validateEnricherConfig(source); err == nil || !strings.Contains(err.Error(), "enrich.datasources.mysql") {
		t.Fatalf("validateEnricherConfig() error=%v", err)
	}
	source.Enrich.DataSources = &config.EnrichDataSources{MySQL: validEnrichMySQLDataSource()}
	if err := validateEnricherConfig(source); err != nil {
		t.Fatalf("complete source dependencies: %v", err)
	}
}

func TestDisplayRequiresMySQLDataSource(t *testing.T) {
	t.Parallel()
	source := config.EventSource{
		EventSourceID: "built_in_bk",
		Enrich:        config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "display"}}},
	}
	if err := validateEnricherConfig(source); err == nil || !strings.Contains(err.Error(), "enrich.datasources.mysql") {
		t.Fatalf("mysql requirement error=%v", err)
	}
	source.Enrich.DataSources = &config.EnrichDataSources{MySQL: validEnrichMySQLDataSource()}
	if err := validateEnricherConfig(source); err != nil {
		t.Fatalf("complete display dependencies: %v", err)
	}
}

func TestResourceRequiresMySQLAndElasticsearch(t *testing.T) {
	t.Parallel()
	source := config.EventSource{
		EventSourceID: "built_in_bk",
		Enrich: config.EnrichConfig{
			Processors:  []config.EnrichProcessorConfig{{Type: "resource"}},
			DataSources: &config.EnrichDataSources{},
		},
	}
	if err := validateEnricherConfig(source); err == nil || !strings.Contains(err.Error(), "enrich.datasources.mysql") {
		t.Fatalf("validateEnricherConfig() error=%v", err)
	}
	source.Enrich.DataSources.MySQL = validEnrichMySQLDataSource()
	if err := validateEnricherConfig(source); err == nil || !strings.Contains(err.Error(), "enrich.datasources.elasticsearch") {
		t.Fatalf("validateEnricherConfig() error=%v", err)
	}
}

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
	value := config.LifecycleConfig{}.WithDefaults()
	return &value
}

func validMySQLConfig() *config.MySQLConfig {
	return &config.MySQLConfig{
		Address: "mysql.example.com:3306", Database: "linkd", Username: "linkd",
	}
}

func validEnrichMySQLDataSource() *config.EnrichMySQLDataSource {
	return &config.EnrichMySQLDataSource{
		Address:  "mysql.example.com:3306",
		Database: "linkd",
		Username: "linkd",
	}
}

func validRedisConfig() *config.RedisConfig {
	return &config.RedisConfig{Address: "redis.example.com:6379"}
}

func TestReleaseEnrichRoutesAreIndependent(t *testing.T) {
	t.Parallel()
	source := config.EventSource{EventSourceID: "dynamic-source", Version: 1}
	firstRuntime, err := openEnrichRuntime(context.Background(), source, 8, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = firstRuntime.Close() }()
	first, err := firstRuntime.router(source, nil)
	if err != nil {
		t.Fatal(err)
	}
	source.Version = 2
	source.Enrich.Processors = []config.EnrichProcessorConfig{{Type: "source"}}
	if err := validateEnricherConfig(source); err == nil {
		t.Fatal("missing data source accepted")
	}
	source.Enrich.DataSources = &config.EnrichDataSources{MySQL: validEnrichMySQLDataSource()}
	if err := validateEnricherConfig(source); err != nil {
		t.Fatal(err)
	}
	source.Enrich.Processors[0].Type = "unknown"
	if err := validateEnricherConfig(source); err == nil {
		t.Fatal("unknown processor accepted")
	}
	if first.(*assembly.Router).EnrichChainKind(source.EventSourceID) != lifecycle.EnrichChainNoop {
		t.Fatal("new release changed an existing route")
	}
}

func TestValidateConfigDoesNotLoadYAMLSourceRoutes(t *testing.T) {
	cfg := config.Config{Lifecycle: validLifecycleConfig(), Storage: &config.StorageConfig{Repository: config.RepositoryTypeMySQL, MySQL: validMySQLConfig(), Redis: validRedisConfig()}, EventSources: []config.EventSource{{EventSourceID: "yaml-only", Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "unknown"}}}}}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("startup used YAML source route: %v", err)
	}
}
