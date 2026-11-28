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
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	"linkd/internal/config"
	elasticsearchstore "linkd/internal/store/elasticsearch"
	"linkd/internal/taskgroup"
)

func TestTaskCatalogCoversResponsibilitiesAndRedacts(t *testing.T) {
	cfg := config.Config{Storage: &config.StorageConfig{Repository: config.RepositoryTypeElasticsearch, Elasticsearch: &config.ElasticsearchConfig{}, Redis: &config.RedisConfig{Password: "private-secret"}}, Lifecycle: &config.LifecycleConfig{}}
	s := taskCatalog(cfg, 0).Snapshot()
	if len(s.Tasks) != 24 || len(s.Services) != 3 {
		t.Fatalf("catalog %+v", s)
	}
	ids := map[string]bool{}
	for _, task := range s.Tasks {
		ids[task.ID] = true
		if task.ID == "redis-stream-manager" && (!task.Enabled || task.ConfigSource != "default") {
			t.Fatal("default stream setting lost")
		}
		if task.ID == "source-providers" && (task.Enabled || task.DisabledReason == "") {
			t.Fatal("missing provider explanation")
		}
	}
	for _, id := range []string{"scheduler", "source-providers", "elasticsearch-schema-and-active-reconciler", "elasticsearch-bucket-manager", "elasticsearch-alert-archiver", "redis-stream-manager", "active-alert-indexes", "dynamic-config", "policy-publication", "shield-check", "shield-hints", "shield-requests", "suppression-requests", "merge-judge", "merge-decisions", "merge-relations", "merge-requests"} {
		if !ids[id] {
			t.Fatal(id)
		}
	}
	b, err := json.Marshal(s)
	if err != nil || strings.Contains(string(b), "private-secret") {
		t.Fatal("catalog leaks credentials")
	}
	disabled := false
	cfg.ControlPlane = &config.ControlPlaneConfig{RedisStream: &config.RedisStreamManagerConfig{Enabled: &disabled}}
	for _, task := range taskCatalog(cfg, 2).Snapshot().Tasks {
		if task.ID == "redis-stream-manager" && (task.Enabled || !strings.Contains(task.DisabledReason, "false")) {
			t.Fatal("explicit disable missing")
		}
		if task.ID == "source-providers" && !task.Enabled {
			t.Fatal("injected provider omitted")
		}
	}
}

func TestCanceledArchiveRoundDoesNotCountAsFailure(t *testing.T) {
	registry := taskCatalog(config.Config{}, 0)
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{})
	done := make(chan error, 1)
	task := newAlertArchiveTask(slog.New(slog.NewTextHandler(io.Discard, nil)), config.ElasticsearchControlPlaneConfig{}.WithDefaults(), taskObserver{registry, "elasticsearch-alert-archiver", nil}, func(context.Context, elasticsearchstore.ArchiveBatchResult) {}, func(ctx context.Context, _ elasticsearchstore.ArchiveBatchRequest) (elasticsearchstore.ArchiveBatchResult, error) {
		close(started)
		<-ctx.Done()
		return elasticsearchstore.ArchiveBatchResult{}, ctx.Err()
	})
	go func() { done <- task.Run(ctx) }()
	<-started
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, task := range registry.Snapshot().Tasks {
		if task.ID == "elasticsearch-alert-archiver" && (task.Execution.Running || task.Execution.Failed != 0 || task.Execution.Canceled != 1) {
			t.Fatal(task)
		}
	}
}

func TestCollectorRecoveryRegistrationMatchesActivation(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		cfg := config.Config{}
		if enabled {
			cfg.Collector = &config.CollectorConfig{Token: "private-runtime-token", Endpoint: "http://private-runtime", TimeoutSeconds: 35}
		}
		registry := taskCatalog(cfg, 0)
		found := false
		for _, entry := range registry.Snapshot().Tasks {
			if entry.ID == "collector-push-recovery" {
				found = true
				if entry.Enabled != enabled {
					t.Fatal("recovery activation differs from assembly")
				}
				if !enabled && entry.DisabledReason == "" {
					t.Fatal("missing disabled reason")
				}
			}
		}
		if !found {
			t.Fatal("recovery absent from fixed catalog")
		}
		called := false
		task := registry.Wrap("collector-push-recovery", taskgroup.Task{Name: "collector-push-recovery", Run: func(context.Context) error { called = true; return nil }})
		err := task.Run(t.Context())
		if enabled && (err != nil || !called) {
			t.Fatalf("configured recovery rejected: %v", err)
		}
		if !enabled && (err == nil || called) {
			t.Fatal("unconfigured recovery started")
		}
		raw, err := json.Marshal(registry.Snapshot())
		if err != nil || strings.Contains(string(raw), "private-runtime") {
			t.Fatal("task catalog exposes runtime credentials/connection")
		}
	}
}
