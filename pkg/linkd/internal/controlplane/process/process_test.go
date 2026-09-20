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
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	elasticsearchstore "linkd/internal/store/elasticsearch"
)

type recordingTaskObserver struct {
	mu        sync.Mutex
	active    []bool
	succeeded []bool
}

func (o *recordingTaskObserver) SetActive(_ context.Context, active bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.active = append(o.active, active)
}

func (o *recordingTaskObserver) RunFinished(_ context.Context, _ time.Duration, succeeded bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.succeeded = append(o.succeeded, succeeded)
}

type recordingElasticsearchManager struct {
	calls              []string
	schemaAndActiveErr error
	bucketErr          error
}

func (m *recordingElasticsearchManager) ReconcileSchemaAndActive(context.Context) error {
	m.calls = append(m.calls, "schema-and-active")
	return m.schemaAndActiveErr
}

func (m *recordingElasticsearchManager) ReconcileBuckets(context.Context) error {
	m.calls = append(m.calls, "buckets")
	return m.bucketErr
}

func TestValidateConfigRequiresManagementTask(t *testing.T) {
	t.Parallel()

	if err := ValidateConfig(config.Config{}); err == nil || !strings.Contains(err.Error(), "no management tasks are enabled") {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	if HasManagementTasks(config.Config{}) {
		t.Fatal("HasManagementTasks() = true")
	}
}

func TestValidateConfigAcceptsElasticsearchStorageManagerTask(t *testing.T) {
	t.Parallel()

	cfg := config.Config{Storage: &config.StorageConfig{
		Repository: config.RepositoryTypeElasticsearch,
		Elasticsearch: &config.ElasticsearchConfig{
			Addresses:   []string{"http://elasticsearch.example.com:9200"},
			IndexPrefix: "linkd-test",
		},
	}}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	if !HasManagementTasks(cfg) {
		t.Fatal("HasManagementTasks() = false")
	}
}

func TestValidateConfigAcceptsRedisStreamManagerWithoutElasticsearch(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Storage: &config.StorageConfig{Redis: &config.RedisConfig{Address: "redis.example.com:6379"}},
		Lifecycle: &config.LifecycleConfig{Output: config.LifecycleOutputConfig{
			Kafka: &config.LifecycleKafkaConfig{
				Brokers: []string{"kafka.example.com:9092"},
				Topic:   "linkd-alerts",
			},
		}},
		ControlPlane: &config.ControlPlaneConfig{
			RedisStream: &config.RedisStreamManagerConfig{},
		},
	}
	if err := ValidateConfig(cfg); err != nil {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
	if !HasManagementTasks(cfg) {
		t.Fatal("HasManagementTasks() = false")
	}
	if err := PrepareDataPlane(t.Context(), cfg); err != nil {
		t.Fatalf("PrepareDataPlane() error = %v", err)
	}
}

func TestValidateConfigRejectsInvalidElasticsearchTaskSchedule(t *testing.T) {
	t.Parallel()
	cfg := config.Config{
		Storage: &config.StorageConfig{
			Repository: config.RepositoryTypeElasticsearch,
			Elasticsearch: &config.ElasticsearchConfig{
				Addresses:   []string{"http://elasticsearch.example.com:9200"},
				IndexPrefix: "linkd-test",
			},
		},
		ControlPlane: &config.ControlPlaneConfig{Elasticsearch: &config.ElasticsearchControlPlaneConfig{
			ArchiveIntervalSeconds: -1,
		}},
	}
	if err := ValidateConfig(cfg); err == nil || !strings.Contains(err.Error(), "archive_interval_seconds") {
		t.Fatalf("ValidateConfig() error = %v", err)
	}
}

func TestPrepareElasticsearchDataPlanePreservesDependencyOrder(t *testing.T) {
	t.Parallel()
	manager := &recordingElasticsearchManager{}
	if err := prepareElasticsearchDataPlane(context.Background(), manager); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(manager.calls, ","); got != "schema-and-active,buckets" {
		t.Fatalf("calls = %q", got)
	}

	manager = &recordingElasticsearchManager{schemaAndActiveErr: fmt.Errorf("schema and active failure")}
	if err := prepareElasticsearchDataPlane(context.Background(), manager); err == nil ||
		!strings.Contains(err.Error(), "prepare elasticsearch schema and active resources") {
		t.Fatalf("prepareElasticsearchDataPlane() error = %v", err)
	}
	if got := strings.Join(manager.calls, ","); got != "schema-and-active" {
		t.Fatalf("calls after schema and active failure = %q", got)
	}
}

func TestPeriodicTaskRecordsImmediateRunAndFailure(t *testing.T) {
	t.Parallel()
	observer := &recordingTaskObserver{}
	wantErr := fmt.Errorf("reconcile failed")
	task := newPeriodicTask(
		slog.New(slog.NewTextHandler(testWriter{t}, nil)),
		"test-task",
		time.Hour,
		observer,
		func(context.Context) error { return wantErr },
	)

	err := task.Run(context.Background())
	if !errors.Is(err, wantErr) {
		t.Fatalf("Run() error = %v, want %v", err, wantErr)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if got := fmt.Sprint(observer.active); got != "[true false]" {
		t.Fatalf("active = %s", got)
	}
	if got := fmt.Sprint(observer.succeeded); got != "[false]" {
		t.Fatalf("succeeded = %s", got)
	}
}

func TestAlertArchiveTaskDrainsBatchesWithoutIntervalDelay(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var requests []elasticsearchstore.ArchiveBatchRequest
	task := newAlertArchiveTask(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		config.ElasticsearchControlPlaneConfig{
			ArchiveIntervalSeconds: 30, ArchiveBatchSize: 1000, ArchiveWorkerCount: 4,
		},
		&recordingTaskObserver{},
		func(context.Context, int, int, int) {},
		func(_ context.Context, request elasticsearchstore.ArchiveBatchRequest) (elasticsearchstore.ArchiveBatchResult, error) {
			requests = append(requests, request)
			switch len(requests) {
			case 1:
				return elasticsearchstore.ArchiveBatchResult{
					Scanned: 1000, Archived: 1000, NextCursor: "alert-1000",
				}, nil
			case 2:
				return elasticsearchstore.ArchiveBatchResult{Scanned: 1, Archived: 1}, nil
			default:
				cancel()
				return elasticsearchstore.ArchiveBatchResult{}, nil
			}
		},
	)
	if err := task.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 3 || requests[0].AfterAlertID != "" ||
		requests[1].AfterAlertID != "alert-1000" || requests[2].AfterAlertID != "" {
		t.Fatalf("archive requests=%#v", requests)
	}
	for _, request := range requests {
		if request.Limit != 1000 || request.WorkerCount != 4 {
			t.Fatalf("archive request=%#v", request)
		}
	}
}

func TestAlertArchiveTaskIsolatesItemFailuresAndWaitsWithoutProgress(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	secondCall := make(chan struct{})
	done := make(chan error, 1)
	calls := 0
	observer := &recordingTaskObserver{}
	task := newAlertArchiveTask(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		config.ElasticsearchControlPlaneConfig{
			ArchiveIntervalSeconds: 30, ArchiveBatchSize: 1000, ArchiveWorkerCount: 4,
		},
		observer,
		func(context.Context, int, int, int) {},
		func(context.Context, elasticsearchstore.ArchiveBatchRequest) (elasticsearchstore.ArchiveBatchResult, error) {
			calls++
			if calls == 1 {
				return elasticsearchstore.ArchiveBatchResult{Scanned: 2, Archived: 1, Failed: 1}, nil
			}
			close(secondCall)
			return elasticsearchstore.ArchiveBatchResult{Scanned: 1, Failed: 1}, nil
		},
	)
	go func() { done <- task.Run(ctx) }()
	select {
	case <-secondCall:
	case <-time.After(time.Second):
		t.Fatal("archive task did not begin the next sweep")
	}
	select {
	case err := <-done:
		t.Fatalf("archive task exited on isolated failure: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	observer.mu.Lock()
	defer observer.mu.Unlock()
	if got := fmt.Sprint(observer.succeeded); got != "[false false]" {
		t.Fatalf("archive outcomes=%s", got)
	}
}

func TestAlertArchiveTaskShrinksOversizedScan(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	limits := make(chan int, 2)
	done := make(chan error, 1)
	task := newAlertArchiveTask(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		config.ElasticsearchControlPlaneConfig{
			ArchiveIntervalSeconds: 30, ArchiveBatchSize: 1000, ArchiveWorkerCount: 4,
		},
		&recordingTaskObserver{},
		func(context.Context, int, int, int) {},
		func(_ context.Context, request elasticsearchstore.ArchiveBatchRequest) (elasticsearchstore.ArchiveBatchResult, error) {
			limits <- request.Limit
			if request.Limit == 1000 {
				return elasticsearchstore.ArchiveBatchResult{}, elasticsearchstore.ErrResponseTooLarge
			}
			return elasticsearchstore.ArchiveBatchResult{}, nil
		},
	)
	go func() { done <- task.Run(ctx) }()
	if first, second := <-limits, <-limits; first != 1000 || second != 500 {
		t.Fatalf("archive limits=%d,%d", first, second)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type testWriter struct{ t *testing.T }

func (w testWriter) Write(data []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(data)))
	return len(data), nil
}
