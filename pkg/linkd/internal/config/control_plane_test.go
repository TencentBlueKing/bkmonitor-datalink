// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"strings"
	"testing"
	"time"
)

func TestLoadControlPlaneRedisStreamDefaults(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, `storage:
  redis:
    address: redis.example.com:6379
lifecycle:
  output:
    kafka:
      brokers: [kafka.example.com:9092]
      topic: linkd-alerts
control_plane:
  redis_stream: {}
`)
	cfg, err := load(path, Overrides{}, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ControlPlane == nil || cfg.ControlPlane.RedisStream == nil {
		t.Fatalf("control plane=%#v", cfg.ControlPlane)
	}
	stream := cfg.ControlPlane.RedisStream
	if stream.ReconcileInterval() != time.Minute || stream.OperationTimeout() != 10*time.Second ||
		stream.MaxEntries != 100000 || stream.TrimBatchSize != 10000 {
		t.Fatalf("redis stream defaults=%#v", stream)
	}
}

func TestLoadControlPlaneElasticsearchDefaults(t *testing.T) {
	t.Parallel()
	path := writeConfig(t, `storage:
  repository: elasticsearch
  elasticsearch:
    addresses: [http://elasticsearch.example.com:9200]
control_plane:
  elasticsearch: {}
`)
	cfg, err := load(path, Overrides{}, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ControlPlane == nil || cfg.ControlPlane.Elasticsearch == nil {
		t.Fatalf("control plane=%#v", cfg.ControlPlane)
	}
	manager := cfg.ControlPlane.Elasticsearch
	if manager.SchemaAndActiveReconcileInterval() != time.Hour ||
		manager.BucketReconcileInterval() != 6*time.Hour ||
		manager.ArchiveInterval() != 30*time.Second || manager.ArchiveBatchSize != 1000 ||
		manager.ArchiveWorkerCount != 4 {
		t.Fatalf("elasticsearch control plane defaults=%#v", manager)
	}
}

func TestControlPlaneElasticsearchRequiresRepository(t *testing.T) {
	t.Parallel()
	cfg := Default()
	cfg.ControlPlane = &ControlPlaneConfig{Elasticsearch: &ElasticsearchControlPlaneConfig{}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "storage.elasticsearch repository is required") {
		t.Fatalf("Validate() error=%v", err)
	}
}

func TestElasticsearchControlPlaneConfigValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		config    ElasticsearchControlPlaneConfig
		wantError string
	}{
		{
			name:      "schema and active interval",
			config:    ElasticsearchControlPlaneConfig{SchemaAndActiveReconcileIntervalSeconds: 1},
			wantError: "schema_and_active_reconcile_interval_seconds",
		},
		{
			name:      "bucket interval",
			config:    ElasticsearchControlPlaneConfig{BucketReconcileIntervalSeconds: 1},
			wantError: "bucket_reconcile_interval_seconds",
		},
		{
			name:      "archive interval",
			config:    ElasticsearchControlPlaneConfig{ArchiveIntervalSeconds: -1},
			wantError: "archive_interval_seconds",
		},
		{
			name:      "archive batch",
			config:    ElasticsearchControlPlaneConfig{ArchiveBatchSize: -1},
			wantError: "archive_batch_size",
		},
		{
			name:      "archive batch upper bound",
			config:    ElasticsearchControlPlaneConfig{ArchiveBatchSize: 10001},
			wantError: "archive_batch_size",
		},
		{
			name:      "archive workers",
			config:    ElasticsearchControlPlaneConfig{ArchiveWorkerCount: 65},
			wantError: "archive_worker_count",
		},
		{
			name: "archive workers exceed batch",
			config: ElasticsearchControlPlaneConfig{
				ArchiveBatchSize: 2, ArchiveWorkerCount: 3,
			},
			wantError: "must not exceed archive_batch_size",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.config.Validate(); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error=%v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestControlPlaneRedisStreamRequiresDependencies(t *testing.T) {
	t.Parallel()
	configured := &ControlPlaneConfig{RedisStream: &RedisStreamManagerConfig{}}
	withoutRedis := Default()
	withoutRedis.ControlPlane = configured
	withoutLifecycle := Default()
	withoutLifecycle.ControlPlane = configured
	withoutLifecycle.Storage = &StorageConfig{Redis: &RedisConfig{Address: "redis.example.com:6379"}}
	tests := []struct {
		name      string
		config    Config
		wantError string
	}{
		{
			name:      "redis",
			config:    withoutRedis,
			wantError: "storage.redis is required",
		},
		{
			name:      "lifecycle",
			config:    withoutLifecycle,
			wantError: "lifecycle is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.config.Validate(); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error=%v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestRedisStreamManagerConfigValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		config    RedisStreamManagerConfig
		wantError string
	}{
		{name: "interval", config: RedisStreamManagerConfig{ReconcileIntervalSeconds: 1}, wantError: "reconcile_interval_seconds"},
		{
			name: "timeout",
			config: RedisStreamManagerConfig{
				ReconcileIntervalSeconds: 10,
				OperationTimeoutSeconds:  10,
			},
			wantError: "must be less than",
		},
		{name: "max entries", config: RedisStreamManagerConfig{MaxEntries: -1}, wantError: "max_entries"},
		{name: "trim batch", config: RedisStreamManagerConfig{TrimBatchSize: -1}, wantError: "trim_batch_size"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.config.Validate(); err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error=%v, want containing %q", err, test.wantError)
			}
		})
	}
}
