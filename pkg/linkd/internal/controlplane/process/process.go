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
	"log/slog"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	controlplaneredisstream "linkd/internal/controlplane/redisstream"
	repositoryassembly "linkd/internal/store/assembly"
	elasticsearchstore "linkd/internal/store/elasticsearch"
	"linkd/internal/taskgroup"
	"linkd/internal/telemetry"
)

const startupTimeout = 10 * time.Second

type runtime struct {
	tasks    []taskgroup.Task
	closeAll func() error
}

// Run 装配并监督当前控制面的全部管理任务。
// 新任务应加入 openRuntime，而不是新增常驻 command 或绕过控制面独立启动。
func Run(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	telemetryRuntime *telemetry.Runtime,
) (runErr error) {
	if ctx == nil || logger == nil || telemetryRuntime == nil {
		return fmt.Errorf("run control plane: context, logger and telemetry runtime are required")
	}
	if err := ValidateConfig(cfg); err != nil {
		return err
	}

	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	runtime, err := openRuntime(startupCtx, cfg, logger, telemetryRuntime)
	cancel()
	if err != nil {
		return err
	}
	defer func() {
		if err := runtime.closeAll(); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}()

	logger.InfoContext(ctx, "linkd control plane started", "management_tasks", taskNames(runtime.tasks))
	defer logger.InfoContext(context.Background(), "linkd control plane stopped")
	if err := taskgroup.Run(ctx, runtime.tasks); err != nil {
		return fmt.Errorf("run control plane management tasks: %w", err)
	}
	return nil
}

// ValidateConfig 校验当前已经实现的控制面任务所需配置。
func ValidateConfig(cfg config.Config) error {
	if !HasManagementTasks(cfg) {
		return fmt.Errorf("run control plane: no management tasks are enabled")
	}
	if hasElasticsearchTask(cfg) {
		if err := repositoryassembly.ValidateElasticsearchManagerConfig(cfg); err != nil {
			return fmt.Errorf("run control plane: %w", err)
		}
		if err := elasticsearchTaskSettings(cfg).Validate(); err != nil {
			return fmt.Errorf("run control plane: control_plane.elasticsearch.%w", err)
		}
	}
	if hasRedisStreamTask(cfg) {
		if cfg.Storage == nil || cfg.Storage.Redis == nil {
			return fmt.Errorf("run control plane: storage.redis is required for redis stream management")
		}
		if cfg.Lifecycle == nil {
			return fmt.Errorf("run control plane: lifecycle config is required for redis stream management")
		}
		if err := cfg.Storage.Redis.Validate(); err != nil {
			return fmt.Errorf("run control plane: storage.redis.%w", err)
		}
		if err := cfg.ControlPlane.Validate(); err != nil {
			return fmt.Errorf("run control plane: %w", err)
		}
		if err := cfg.Lifecycle.Validate(); err != nil {
			return fmt.Errorf("run control plane: %w", err)
		}
	}
	return nil
}

// HasManagementTasks 报告当前配置是否启用了至少一个已经实现的控制面管理任务。
func HasManagementTasks(cfg config.Config) bool {
	return hasElasticsearchTask(cfg) || hasRedisStreamTask(cfg)
}

func hasElasticsearchTask(cfg config.Config) bool {
	return cfg.Storage != nil &&
		cfg.Storage.Repository == config.RepositoryTypeElasticsearch &&
		cfg.Storage.Elasticsearch != nil
}

func hasRedisStreamTask(cfg config.Config) bool {
	return cfg.ControlPlane != nil && cfg.ControlPlane.RedisStream != nil
}

func elasticsearchTaskSettings(cfg config.Config) config.ElasticsearchControlPlaneConfig {
	if cfg.ControlPlane != nil && cfg.ControlPlane.Elasticsearch != nil {
		return cfg.ControlPlane.Elasticsearch.WithDefaults()
	}
	return (config.ElasticsearchControlPlaneConfig{}).WithDefaults()
}

// PrepareDataPlane 在 All-in-one 接管消息前按依赖顺序同步完成 Schema、Active 资源和时间桶对账。
// Alert 归档不属于数据面启动条件，由独立控制面任务执行。
func PrepareDataPlane(ctx context.Context, cfg config.Config) error {
	if !hasElasticsearchTask(cfg) {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("prepare control plane data dependencies: context must not be nil")
	}
	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	runtime, err := repositoryassembly.OpenElasticsearchManager(startupCtx, *cfg.Storage.Elasticsearch)
	if err != nil {
		return fmt.Errorf("initialize elasticsearch storage management task: %w", err)
	}
	defer runtime.Close()
	if err := prepareElasticsearchDataPlane(startupCtx, runtime.Manager); err != nil {
		return err
	}
	return nil
}

type elasticsearchManager interface {
	ReconcileSchemaAndActive(context.Context) error
	ReconcileBuckets(context.Context) error
}

func prepareElasticsearchDataPlane(ctx context.Context, manager elasticsearchManager) error {
	if err := manager.ReconcileSchemaAndActive(ctx); err != nil {
		return fmt.Errorf("prepare elasticsearch schema and active resources: %w", err)
	}
	if err := manager.ReconcileBuckets(ctx); err != nil {
		return fmt.Errorf("prepare elasticsearch buckets: %w", err)
	}
	return nil
}

func openRuntime(
	ctx context.Context,
	cfg config.Config,
	logger *slog.Logger,
	telemetryRuntime *telemetry.Runtime,
) (*runtime, error) {
	tasks := make([]taskgroup.Task, 0, 4)
	closers := make([]func() error, 0, 2)
	closeAll := func() error {
		var closeErrors []error
		for index := len(closers) - 1; index >= 0; index-- {
			if err := closers[index](); err != nil {
				closeErrors = append(closeErrors, err)
			}
		}
		return errors.Join(closeErrors...)
	}
	fail := func(err error) (*runtime, error) {
		return nil, errors.Join(err, closeAll())
	}

	if hasElasticsearchTask(cfg) {
		elasticsearchRuntime, err := repositoryassembly.OpenElasticsearchManager(ctx, *cfg.Storage.Elasticsearch)
		if err != nil {
			return fail(fmt.Errorf("initialize elasticsearch storage management task: %w", err))
		}
		closers = append(closers, func() error {
			elasticsearchRuntime.Close()
			return nil
		})
		if err := prepareElasticsearchDataPlane(ctx, elasticsearchRuntime.Manager); err != nil {
			return fail(err)
		}
		settings := elasticsearchTaskSettings(cfg)
		tasks = append(tasks,
			newPeriodicTask(
				logger,
				"elasticsearch-schema-and-active-reconciler",
				settings.SchemaAndActiveReconcileInterval(),
				telemetryRuntime.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskElasticsearchSchemaAndActiveReconciler),
				elasticsearchRuntime.Manager.ReconcileSchemaAndActive,
			),
			newPeriodicTask(
				logger,
				"elasticsearch-bucket-manager",
				settings.BucketReconcileInterval(),
				telemetryRuntime.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskElasticsearchBucketManager),
				elasticsearchRuntime.Manager.ReconcileBuckets,
			),
			newAlertArchiveTask(
				logger,
				settings,
				telemetryRuntime.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskElasticsearchAlertArchiver),
				telemetryRuntime.ObserveElasticsearchArchiveBatch,
				elasticsearchRuntime.Manager.ArchiveTerminalAlerts,
			),
		)
	}

	if hasRedisStreamTask(cfg) {
		redisConfig := cfg.Storage.Redis
		client := redis.NewClient(&redis.Options{
			Addr: redisConfig.Address, Username: redisConfig.Username,
			Password: redisConfig.Password, DB: redisConfig.Database,
		})
		closers = append(closers, func() error {
			if err := client.Close(); err != nil {
				return fmt.Errorf("close control plane redis: %w", err)
			}
			return nil
		})
		if err := client.Ping(ctx).Err(); err != nil {
			return fail(fmt.Errorf("connect control plane redis: %w", err))
		}
		settings := cfg.ControlPlane.RedisStream.WithDefaults()
		lifecycleConfig := cfg.Lifecycle.WithDefaults()
		manager, err := controlplaneredisstream.NewManager(client, controlplaneredisstream.Config{
			Stream: lifecycleConfig.Signal.Stream, ExpectedGroup: lifecycleConfig.Signal.Group,
			ReconcileInterval: settings.ReconcileInterval(), OperationTimeout: settings.OperationTimeout(),
			MaxEntries: settings.MaxEntries, TrimBatchSize: settings.TrimBatchSize,
		}, telemetryRuntime.RedisStreamObserver())
		if err != nil {
			return fail(fmt.Errorf("initialize redis stream management task: %w", err))
		}
		taskName := "redis-stream-manager"
		taskObserver := telemetryRuntime.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskRedisStreamManager)
		tasks = append(tasks, taskgroup.Task{
			Name: taskName,
			Run: func(taskCtx context.Context) error {
				taskObserver.SetActive(taskCtx, true)
				defer taskObserver.SetActive(context.Background(), false)
				logger.InfoContext(taskCtx, "control plane management task started", "task", taskName)
				defer logger.InfoContext(context.Background(), "control plane management task stopped", "task", taskName)
				return manager.Run(taskCtx)
			},
		})
	}

	return &runtime{tasks: tasks, closeAll: closeAll}, nil
}

func taskNames(tasks []taskgroup.Task) []string {
	names := make([]string, len(tasks))
	for index, task := range tasks {
		names[index] = task.Name
	}
	return names
}

func newPeriodicTask(
	logger *slog.Logger,
	name string,
	interval time.Duration,
	observer periodicTaskObserver,
	runOnce func(context.Context) error,
) taskgroup.Task {
	return taskgroup.Task{
		Name: name,
		Run: func(ctx context.Context) error {
			observer.SetActive(ctx, true)
			defer observer.SetActive(context.Background(), false)
			logger.InfoContext(ctx, "control plane management task started", "task", name, "interval", interval)
			defer logger.InfoContext(context.Background(), "control plane management task stopped", "task", name)
			if err := runPeriodicTaskOnce(ctx, observer, runOnce); err != nil {
				return err
			}
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
					if err := runPeriodicTaskOnce(ctx, observer, runOnce); err != nil {
						return err
					}
				}
			}
		},
	}
}

type periodicTaskObserver interface {
	SetActive(context.Context, bool)
	RunFinished(context.Context, time.Duration, bool)
}

func runPeriodicTaskOnce(
	ctx context.Context,
	observer periodicTaskObserver,
	runOnce func(context.Context) error,
) error {
	startedAt := time.Now()
	err := runOnce(ctx)
	observer.RunFinished(ctx, time.Since(startedAt), err == nil)
	return err
}

type archiveBatchRunner func(
	context.Context,
	elasticsearchstore.ArchiveBatchRequest,
) (elasticsearchstore.ArchiveBatchResult, error)

type archiveBatchObserver func(context.Context, int, int, int)

func newAlertArchiveTask(
	logger *slog.Logger,
	settings config.ElasticsearchControlPlaneConfig,
	observer periodicTaskObserver,
	observeBatch archiveBatchObserver,
	runBatch archiveBatchRunner,
) taskgroup.Task {
	const taskName = "elasticsearch-alert-archiver"
	return taskgroup.Task{
		Name: taskName,
		Run: func(ctx context.Context) error {
			observer.SetActive(ctx, true)
			defer observer.SetActive(context.Background(), false)
			logger.InfoContext(
				ctx,
				"control plane management task started",
				"task", taskName,
				"idle_interval", settings.ArchiveInterval(),
				"batch_size", settings.ArchiveBatchSize,
				"worker_count", settings.ArchiveWorkerCount,
			)
			defer logger.InfoContext(context.Background(), "control plane management task stopped", "task", taskName)

			cursor := ""
			effectiveLimit := settings.ArchiveBatchSize
			sweepArchived := 0
			for {
				startedAt := time.Now()
				result, err := runBatch(ctx, elasticsearchstore.ArchiveBatchRequest{
					Limit: effectiveLimit, WorkerCount: min(settings.ArchiveWorkerCount, effectiveLimit), AfterAlertID: cursor,
				})
				if ctx.Err() != nil {
					return nil
				}
				observer.RunFinished(ctx, time.Since(startedAt), err == nil && result.Failed == 0)
				if err != nil {
					if errors.Is(err, elasticsearchstore.ErrResponseTooLarge) && effectiveLimit > 1 {
						effectiveLimit = max(1, effectiveLimit/2)
						logger.WarnContext(
							ctx,
							"alert archive scan exceeded response limit; reducing batch",
							"task", taskName,
							"effective_batch_size", effectiveLimit,
							"error", err,
						)
						continue
					}
					logger.ErrorContext(ctx, "alert archive batch failed", "task", taskName, "error", err)
					if !waitForArchiveRetry(ctx, settings.ArchiveInterval()) {
						return nil
					}
					continue
				}

				observeBatch(ctx, result.Scanned, result.Archived, result.Failed)
				if result.Scanned > 0 {
					logger.DebugContext(
						ctx,
						"alert archive batch finished",
						"task", taskName,
						"scanned", result.Scanned,
						"archived", result.Archived,
						"failed", result.Failed,
					)
				}
				if result.Failed > 0 {
					logger.WarnContext(
						ctx,
						"alert archive batch contains isolated failures",
						"task", taskName,
						"failed", result.Failed,
						"failure_samples", result.FailureItems,
					)
				}
				sweepArchived += result.Archived
				if result.NextCursor != "" {
					cursor = result.NextCursor
					continue
				}

				cursor = ""
				effectiveLimit = settings.ArchiveBatchSize
				if sweepArchived > 0 {
					sweepArchived = 0
					continue
				}
				if !waitForArchiveRetry(ctx, settings.ArchiveInterval()) {
					return nil
				}
			}
		},
	}
}

func waitForArchiveRetry(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
