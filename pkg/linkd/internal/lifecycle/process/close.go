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
	"errors"
	"log/slog"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/activeindex"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/recentalert"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy/redisstate"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/redisclient"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/suppressioncleanup"
	"linkd/internal/telemetry"
)

// CloseError 提供管理接口的错误分类；不向客户端暴露基础设施错误和凭据。
type CloseError struct {
	Status  int
	Code    string
	Message string
	cause   error
}

func (e *CloseError) Error() string { return e.Message }

// Unwrap 保留内部依赖错误链；HTTP 入口只返回安全的 Message。
func (e *CloseError) Unwrap() error { return e.cause }

type closeSourceReader interface {
	Get(context.Context, string) (eventsource.Record, error)
	GetRelease(context.Context, string, int64) (eventsource.Release, error)
}

// AlertProjectionSynchronizer 确认主动关闭已经写入 KAC，成功不保证搜索立即可见；失败不能回滚关闭事实。
type AlertProjectionSynchronizer interface {
	ProjectNow(context.Context, string, string) error
}

// AlertCloser 为用户或系统关闭装配当前已发布来源的 Hook，复用正式 Lifecycle 的 CAS 和流水逻辑。
// 重试必须携带相同命令，终态部分成功不等同于未执行。
type AlertCloser struct {
	logger        *slog.Logger
	metrics       *telemetry.Runtime
	resources     *AlertProcessorResources
	sources       closeSourceReader
	withProcessor func(context.Context, store.StoredAlert, func(*lifecycle.Processor) error) error
	batchLocker   func(string) (closeBatchLocker, error)
	run           func(context.Context, lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error)
	read          func(context.Context, string, string) (domain.Alert, error)
	projection    AlertProjectionSynchronizer
}

// UseResources 在接收关闭请求前注入控制面的共用连接和来源 Hook。
func (s *AlertCloser) UseResources(resources *AlertProcessorResources) { s.resources = resources }

// NewAlertCloser 创建控制面专用的有界关闭入口，不启动消费者或修改存储 schema。
func NewAlertCloser(cfg config.Config, sources closeSourceReader, severity *runtimeconfig.Severity, logger *slog.Logger, metrics *telemetry.Runtime, projection AlertProjectionSynchronizer) *AlertCloser {
	closer := &AlertCloser{projection: projection, logger: logger, metrics: metrics, sources: sources}
	closer.batchLocker = func(source string) (closeBatchLocker, error) {
		lockConfig := cfg.Lifecycle.ForSource(cfg.Dispatch.WithDefaults().Deployment, source).SchedulerConfig()
		lockConfig.LockTTL = max(lockConfig.LockTTL, 30*time.Second)
		return scheduler.NewRedisLocker(closer.resources.client, lockConfig)
	}
	prepare := newAlarmdClosePreparation(cfg.Resources.Alarmd, logger)
	configure := func(store.Repository, *redisstate.Store) []lifecycle.ProcessorOption {
		return []lifecycle.ProcessorOption{lifecycle.WithClosePreparation(prepare)}
	}
	closer.withProcessor = func(ctx context.Context, identity store.StoredAlert, run func(*lifecycle.Processor) error) error {
		return withAlertProcessor(ctx, cfg, sources, severity, logger, metrics, identity.Alert.BKTenantID, identity.Alert.AlertID, configure, run, identity)
	}
	closer.run = func(ctx context.Context, command lifecycle.CloseAlertCommand) (result lifecycle.CloseAlertResult, err error) {
		ctx = closer.resources.context(ctx)
		err = withAlertProcessor(ctx, cfg, sources, severity, logger, metrics, command.BKTenantID, command.AlertID, configure, func(p *lifecycle.Processor) error {
			var err error
			result, err = p.CloseAlert(ctx, command)
			return err
		})
		return result, err
	}
	closer.read = func(ctx context.Context, tenantID, alertID string) (alert domain.Alert, readErr error) {
		if cfg.Storage == nil {
			return alert, &CloseError{Status: 503, Code: "unavailable", Message: "storage is not configured"}
		}
		runtime, err := storeassembly.OpenExisting(ctx, *cfg.Storage, 4)
		if err != nil {
			return alert, err
		}
		defer storeassembly.JoinCloseError(&readErr, runtime)
		reader := runtime.Repository.GetAlert
		if current, ok := runtime.Repository.(store.LifecycleAlertStore); ok {
			reader = current.GetAlertCurrent
		}
		stored, err := reader(ctx, tenantID, alertID)
		if err != nil {
			return alert, err
		}
		return stored.Alert.Clone(), nil
	}
	return closer
}

// withAlertProcessor 为人工关闭、屏蔽检查和合并步骤共用实时读取、租户校验、Hook 与同一租约边界。
func withAlertProcessor(ctx context.Context, cfg config.Config, sources closeSourceReader, severity *runtimeconfig.Severity, logger *slog.Logger, metrics *telemetry.Runtime, tenant, alertID string, configure func(store.Repository, *redisstate.Store) []lifecycle.ProcessorOption, run func(*lifecycle.Processor) error, identity ...store.StoredAlert) (runErr error) {
	started := time.Now()
	if cfg.Storage == nil || cfg.Lifecycle == nil || cfg.Storage.Redis == nil {
		return &CloseError{Status: 503, Code: "unavailable", Message: "lifecycle is not configured"}
	}
	shared, _ := ctx.Value(controlResourcesKey{}).(*AlertProcessorResources)
	var runtime *storeassembly.Runtime
	var err error
	if shared != nil {
		runtime = shared.repository
	} else {
		runtime, err = storeassembly.OpenExisting(ctx, *cfg.Storage, 4)
		if err != nil {
			return err
		}
		defer storeassembly.JoinCloseError(&runErr, runtime)
	}
	reader := runtime.Repository.GetAlert
	if current, ok := runtime.Repository.(store.LifecycleAlertStore); ok {
		reader = current.GetAlertCurrent
	}
	var stored store.StoredAlert
	if len(identity) > 0 {
		stored = identity[0]
	} else {
		stored, err = reader(ctx, tenant, alertID)
		if err != nil {
			return err
		}
	}

	source, err := readControlSource(ctx, sources, stored.Alert.EventSourceID)
	if err != nil {
		return err
	}
	if source.Published <= 0 {
		return &CloseError{Status: 409, Code: "source_unpublished", Message: "event source has no published configuration"}
	}
	version := source.Published
	if source.Deleted {
		version = stored.Alert.EventSourceVersion
	}
	var release eventsource.Release
	if shared != nil {
		release, err = shared.sourceRelease(ctx, sources, source.ID, version)
	} else {
		release, err = sources.GetRelease(ctx, source.ID, version)
	}
	if err != nil {
		return err
	}
	if release.Spec.RelatedTenantID != "" && release.Spec.RelatedTenantID != tenant {
		return &CloseError{Status: 403, Code: "tenant_mismatch", Message: "event source tenant mismatch"}
	}
	var hooks []lifecycle.NamedFinalHook
	var closeHooks func() error
	if shared != nil {
		hooks, closeHooks, err = shared.acquireHooks(release, cfg, metrics, severity)
	} else {
		hooks, closeHooks, err = openHooksWithSeverity(kacHooks(release.Spec.Hooks, cfg.Plugins.KACEnabled()), metrics, severity)
	}
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, closeHooks()) }()
	var client *redis.Client
	if shared != nil {
		client = shared.client
	} else {
		options := cfg.Storage.Redis.ClientOptions()
		options.ContextTimeoutEnabled = true
		client, err = redisclient.New(options)
		if err != nil {
			return err
		}
		defer func() { runErr = errors.Join(runErr, client.Close()) }()
	}
	lockConfig := cfg.Lifecycle.ForSource(cfg.Dispatch.WithDefaults().Deployment, stored.Alert.EventSourceID).SchedulerConfig()
	// 筛选关闭最多二十秒，lease 至少三十秒；不启动额外续租 goroutine。
	// 与 Worker 共用 tenant/source/fingerprint 锁，避免旧终态缓存覆盖新生命周期。
	lockConfig.LockTTL = max(lockConfig.LockTTL, 30*time.Second)
	locker, err := scheduler.NewRedisLocker(client, lockConfig)
	if err != nil {
		return err
	}
	cache := lifecycle.RecentAlertCache(lifecycle.NoopRecentAlertCache{})
	if runtime.Backend == config.RepositoryTypeElasticsearch {
		cache, err = recentalert.NewStore(client, recentalert.Config{KeyPrefix: cfg.Lifecycle.WithDefaults().Mailbox.KeyPrefix + ":recent-alert", RefreshInterval: cfg.Storage.Elasticsearch.ActiveAlertRefreshInterval()}, metrics.RecentAlertCacheObserver())
		if err != nil {
			return err
		}
	}
	policyState, err := redisstate.New(client, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		return err
	}
	observed := metrics.ObserveRepository(runtime.Repository)
	var cleanup suppressioncleanup.Recorder = configuredCleanupRecorder{storage: *cfg.Storage, deployment: cfg.Dispatch.WithDefaults().Deployment}
	if shared != nil && shared.cleanup != nil {
		cleanup = shared.cleanup
	}
	opts := []lifecycle.ProcessorOption{lifecycle.WithNewAlertSuppressor(&policyruntime.Suppressor{State: policyState, Aggregation: policyState, BatchState: policyState, Observer: metrics, Logger: logger, CleanupRecorder: cleanup}), lifecycle.WithDependencyRegistry(&policyruntime.Shielder{Dependency: policyState, Index: policyState, Hints: policyState, Observer: metrics, Logger: logger})}
	publisher := changePublisher(cfg, client, metrics)
	if pending, ok := ctx.Value(closeBatchNotificationsKey{}).(*closeBatchNotifications); ok && publisher != nil {
		publisher = pending.use(publisher)
	}
	opts = append(opts, lifecycle.WithAlertChangePublisher(publisher), lifecycle.WithActiveAlertCache(activeindex.New(client, cfg.Dispatch.WithDefaults().Deployment, release.Spec, metrics)))
	if configure != nil {
		opts = append(opts, configure(observed, policyState)...)
	}
	processor, err := lifecycle.NewProcessor(observed, cache, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, hooks, severity, lifecycle.SystemClock{}, logger, opts...)
	if err != nil {
		return err
	}
	key := mailbox.CorrelationKey(stored.Alert.BKTenantID, stored.Alert.EventSourceID, stored.Alert.Fingerprint)
	store.ObserveBatchPhase(ctx, "member.processor_prepare", time.Since(started))
	if ctx.Value(closeBatchLeasesHeldKey{}) == true {
		// 显式关闭批次已 pipeline 取得全部成员租约，来源处理器只构造一次。
		return run(processor)
	}
	return withAlertLease(ctx, locker, key, func() error { return run(processor) })
}

// CloseAlert 在固定预算内执行一次显式关闭命令；失败可能发生在 CAS 已成功之后。
func (s *AlertCloser) CloseAlert(ctx context.Context, command lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
	if ctx == nil {
		return lifecycle.CloseAlertResult{}, &CloseError{Status: 400, Code: "invalid_command", Message: "context is required"}
	}
	if command.Validate() != nil || domain.ValidateIdentityPart("bk_tenant_id", command.BKTenantID, 64) != nil || len(command.AlertID) > domain.EntityIDMaxBytes || len(command.OperationID) > 128 || len(command.OperatorID) > 256 || command.EffectiveAt.After(time.Now().Add(5*time.Minute)) {
		return lifecycle.CloseAlertResult{}, &CloseError{Status: 400, Code: "invalid_command", Message: "invalid close command"}
	}
	select {
	case <-ctx.Done():
		return lifecycle.CloseAlertResult{}, ctx.Err()
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ctx = store.WithBatchPhaseObserver(ctx, s.metrics.ObserveClosePhase)
	started := time.Now()
	defer func() { store.ObserveBatchPhase(ctx, "member.total", time.Since(started)) }()
	result, err := s.run(ctx, command)
	if err == nil {
		if s.projection != nil {
			started := time.Now()
			err := s.projection.ProjectNow(ctx, result.Alert.BKTenantID, result.Alert.AlertID)
			store.ObserveBatchPhase(ctx, "member.projection", time.Since(started))
			if err != nil {
				if s.logger != nil {
					s.logger.WarnContext(ctx, "synchronous close projection failed", "module", "lifecycle", "operation", "close_projection", "error", err,
						"bk_tenant_id", command.BKTenantID, "alert_id", command.AlertID, "operation_id", command.OperationID)
				}
				return result, &CloseError{Status: 502, Code: "result_uncertain", Message: "alert closed but KAC write failed; retry the same operation", cause: err}
			}
		}
		return result, nil
	}
	return result, s.closeError(ctx, command, err)
}

func (s *AlertCloser) closeError(ctx context.Context, command lifecycle.CloseAlertCommand, err error) error {
	var classified *CloseError
	if errors.As(err, &classified) {
		return classified
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, eventsource.ErrNotFound) {
		return &CloseError{Status: 404, Code: "not_found", Message: "alert or source not found", cause: err}
	}
	if errors.Is(err, scheduler.ErrLockBusy) {
		return &CloseError{Status: 429, Code: "busy", Message: "alert is being processed; retry the same operation", cause: err}
	}
	if errors.Is(err, store.ErrInvalidTransition) {
		return &CloseError{Status: 409, Code: "invalid_transition", Message: "alert state changed; refresh before another operation", cause: err}
	}
	if errors.Is(err, store.ErrVersionConflict) {
		return &CloseError{Status: 409, Code: "version_conflict", Message: "alert version changed; retry the same operation", cause: err}
	}
	if s.logger != nil {
		s.logger.WarnContext(ctx, "close alert failed", "module", "lifecycle", "operation", "close_alert", "error", err,
			"bk_tenant_id", command.BKTenantID, "alert_id", command.AlertID, "operation_id", command.OperationID)
	}
	return &CloseError{Status: 502, Code: "result_uncertain", Message: "close result uncertain; retry the same operation", cause: err}
}

// CloseKACAlert 复用正式关闭命令；KAC 的鉴权边界由 HTTP 入口单独处理。
func (s *AlertCloser) CloseKACAlert(ctx context.Context, command lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
	return s.CloseAlert(ctx, command)
}

// GetAlert 读取指定租户的当前快照，供 KAC 在不确定响应后核对稳定操作身份。
func (s *AlertCloser) GetAlert(ctx context.Context, tenantID, alertID string) (domain.Alert, error) {
	if ctx == nil || domain.ValidateIdentityPart("bk_tenant_id", tenantID, 64) != nil || alertID == "" || len(alertID) > domain.EntityIDMaxBytes {
		return domain.Alert{}, &CloseError{Status: 400, Code: "invalid_command", Message: "invalid alert identity"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	alert, err := s.read(ctx, tenantID, alertID)
	if err == nil {
		return alert, nil
	}
	if errors.Is(err, store.ErrNotFound) {
		return domain.Alert{}, &CloseError{Status: 404, Code: "not_found", Message: "alert not found"}
	}
	var classified *CloseError
	if errors.As(err, &classified) {
		return domain.Alert{}, classified
	}
	if s.logger != nil {
		s.logger.WarnContext(ctx, "read alert for close failed", "module", "lifecycle", "operation", "close_read", "error", err, "bk_tenant_id", tenantID, "alert_id", alertID)
	}
	return domain.Alert{}, &CloseError{Status: 502, Code: "result_uncertain", Message: "alert lookup failed"}
}

func withAlertLease(ctx context.Context, locker scheduler.Locker, key string, run func() error) (err error) {
	started := time.Now()
	lease, err := locker.Acquire(ctx, key)
	store.ObserveBatchPhase(ctx, "member.lease_acquire", time.Since(started))
	if err != nil {
		return err
	}
	defer func() {
		// 即使请求取消仍归还 lease；释放失败不能伪装成确定成功。
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		started := time.Now()
		err = errors.Join(err, locker.Release(releaseCtx, lease))
		store.ObserveBatchPhase(ctx, "member.lease_release", time.Since(started))
	}()
	return run()
}
