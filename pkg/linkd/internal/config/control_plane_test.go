// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
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
	for _, controlPlane := range []string{
		"",
		"control_plane: {}\n",
		"control_plane:\n  redis_stream: {}\n",
		"control_plane:\n  active_index: {}\n",
	} {
		t.Run(controlPlane, func(t *testing.T) {
			t.Parallel()
			path := writeConfig(t, `storage:
  redis:
    address: redis.example.com:6379
lifecycle: {}
`+controlPlane)
			cfg, err := load(path, Overrides{}, mapLookup(nil))
			if err != nil {
				t.Fatal(err)
			}
			if cfg.ControlPlane == nil || cfg.ControlPlane.RedisStream == nil {
				t.Fatalf("control plane=%#v", cfg.ControlPlane)
			}
			stream := cfg.ControlPlane.RedisStream
			if !stream.IsEnabled() || stream.ReconcileInterval() != 10*time.Second || stream.OperationTimeout() != 3*time.Second ||
				stream.MaxEntries != 100000 || stream.TrimBatchSize != 10000 || stream.MaxTrimEntriesPerCycle != 100000 {
				t.Fatalf("redis stream defaults=%#v", stream)
			}
			printed, err := MarshalRedacted(cfg)
			if err != nil || !strings.Contains(string(printed), "enabled: true") {
				t.Fatalf("MarshalRedacted()=%s, error=%v", printed, err)
			}
		})
	}
}

func TestLoadRedisStreamActivation(t *testing.T) {
	t.Parallel()
	const dependencies = "storage:\n  redis:\n    address: redis.example.com:6379\nlifecycle: {}\n"
	for _, test := range []struct {
		name           string
		content        string
		wantConfigured bool
		wantEnabled    bool
		wantError      string
	}{
		{name: "no dependencies", content: "{}\n"},
		{name: "redis only", content: "storage:\n  redis:\n    address: redis.example.com:6379\n"},
		{name: "lifecycle only", content: "lifecycle: {}\n"},
		{name: "explicitly disabled", content: dependencies + "control_plane:\n  redis_stream:\n    enabled: false\n", wantConfigured: true},
		{name: "disabled without dependencies", content: "control_plane:\n  redis_stream:\n    enabled: false\n", wantConfigured: true},
		{name: "explicitly enabled", content: dependencies + "control_plane:\n  redis_stream:\n    enabled: true\n", wantConfigured: true, wantEnabled: true},
		{name: "enabled without redis", content: "lifecycle: {}\ncontrol_plane:\n  redis_stream: {}\n", wantError: "storage.redis is required"},
		{name: "enabled without lifecycle", content: "storage:\n  redis:\n    address: redis.example.com:6379\ncontrol_plane:\n  redis_stream: {}\n", wantError: "lifecycle is required"},
		{name: "invalid budget", content: dependencies + "control_plane:\n  redis_stream:\n    max_entries: -1\n", wantError: "max_entries"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := load(writeConfig(t, test.content), Overrides{}, mapLookup(nil))
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("load() error=%v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			settings := cfg.RedisStreamSettings()
			if (settings != nil) != test.wantConfigured || (settings != nil && settings.IsEnabled()) != test.wantEnabled {
				t.Fatalf("RedisStreamSettings()=%#v", settings)
			}
		})
	}
}

func TestLoadRedisStreamPreservesOverrides(t *testing.T) {
	t.Parallel()
	cfg, err := load(writeConfig(t, `storage:
  redis:
    address: redis.example.com:6379
lifecycle: {}
control_plane:
  redis_stream:
    reconcile_interval_seconds: 30
    operation_timeout_seconds: 5
    max_entries: 50000
    trim_batch_size: 500
`), Overrides{}, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	settings := cfg.RedisStreamSettings()
	if !settings.IsEnabled() || settings.ReconcileIntervalSeconds != 30 || settings.OperationTimeoutSeconds != 5 || settings.MaxEntries != 50000 || settings.TrimBatchSize != 500 || settings.MaxTrimEntriesPerCycle != 5000 {
		t.Fatalf("RedisStreamSettings()=%#v", settings)
	}
	*settings.Enabled = false
	if !cfg.ControlPlane.RedisStream.IsEnabled() {
		t.Fatal("effective settings share the input enabled pointer")
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
		manager.ArchiveInterval() != 5*time.Second || manager.ArchiveBatchSize != 1000 ||
		manager.ArchiveWorkerCount != 1 {
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
		{
			name: "cycle trim below batch",
			config: RedisStreamManagerConfig{
				TrimBatchSize:          100,
				MaxTrimEntriesPerCycle: 99,
			},
			wantError: "must not be less than trim_batch_size",
		},
		{
			name: "cycle trim exceeds command budget",
			config: RedisStreamManagerConfig{
				TrimBatchSize:          100,
				MaxTrimEntriesPerCycle: 10001,
			},
			wantError: "must not exceed 100 times trim_batch_size",
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

func TestRedisStreamManagerConfigDefaultsDeriveCycleTrimBudget(t *testing.T) {
	t.Parallel()
	defaults := (RedisStreamManagerConfig{}).WithDefaults()
	if defaults.ReconcileIntervalSeconds != 10 || defaults.OperationTimeoutSeconds != 3 ||
		defaults.MaxEntries != 100000 || defaults.TrimBatchSize != 10000 || defaults.MaxTrimEntriesPerCycle != 100000 {
		t.Fatalf("defaults=%#v", defaults)
	}

	custom := (RedisStreamManagerConfig{TrimBatchSize: 250}).WithDefaults()
	if custom.MaxTrimEntriesPerCycle != 2500 {
		t.Fatalf("derived max trim entries per cycle=%d, want 2500", custom.MaxTrimEntriesPerCycle)
	}
}
