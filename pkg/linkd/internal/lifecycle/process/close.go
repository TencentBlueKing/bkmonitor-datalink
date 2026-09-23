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

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/mailbox"
	"linkd/internal/lifecycle/recentalert"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/redisclient"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/telemetry"
)

// CloseError 提供管理接口的错误分类；不向客户端暴露基础设施错误和凭据。
type CloseError struct {
	Status  int
	Message string
}

func (e *CloseError) Error() string { return e.Message }

type closeSourceReader interface {
	Get(context.Context, string) (eventsource.Record, error)
	GetRelease(context.Context, string, int64) (eventsource.Release, error)
}

// AlertCloser 为人工关闭装配当前已发布来源的 Hook，复用正式 Lifecycle 的 CAS 和流水逻辑。
// 最多四个并发请求；重试必须携带相同命令，终态部分成功不等同于未执行。
type AlertCloser struct {
	slots chan struct{}
	run   func(context.Context, lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error)
}

// NewAlertCloser 创建控制面专用的有界关闭入口，不启动消费者或修改存储 schema。
func NewAlertCloser(cfg config.Config, sources closeSourceReader, severity *runtimeconfig.Severity, logger *slog.Logger, metrics *telemetry.Runtime) *AlertCloser {
	return &AlertCloser{slots: make(chan struct{}, 4), run: func(ctx context.Context, command lifecycle.CloseAlertCommand) (result lifecycle.CloseAlertResult, runErr error) {
		if cfg.Storage == nil || cfg.Lifecycle == nil || cfg.Storage.Redis == nil {
			return result, &CloseError{503, "lifecycle is not configured"}
		}
		runtime, err := storeassembly.OpenExisting(ctx, *cfg.Storage, 4)
		if err != nil {
			return result, err
		}
		defer storeassembly.JoinCloseError(&runErr, runtime)
		reader := runtime.Repository.GetAlert
		if current, ok := runtime.Repository.(store.LifecycleAlertStore); ok {
			reader = current.GetAlertCurrent
		}
		stored, err := reader(ctx, command.BKTenantID, command.AlertID)
		if err != nil {
			return result, err
		}
		source, err := sources.Get(ctx, stored.Alert.EventSourceID)
		if err != nil {
			return result, err
		}
		if source.Published <= 0 {
			return result, &CloseError{409, "event source has no published configuration"}
		}
		release, err := sources.GetRelease(ctx, source.ID, source.Published)
		if err != nil {
			return result, err
		}
		if release.Spec.RelatedTenantID != "" && release.Spec.RelatedTenantID != command.BKTenantID {
			return result, &CloseError{403, "event source tenant mismatch"}
		}
		hooks, closeHooks, err := openHooksWithSeverity(release.Spec.Hooks, metrics, severity)
		if err != nil {
			return result, err
		}
		defer func() { runErr = errors.Join(runErr, closeHooks()) }()
		options := cfg.Storage.Redis.ClientOptions()
		options.ContextTimeoutEnabled = true
		options.PoolSize = 2
		client, err := redisclient.New(options)
		if err != nil {
			return result, err
		}
		defer func() { runErr = errors.Join(runErr, client.Close()) }()
		lockConfig := cfg.Lifecycle.ForSource(cfg.Dispatch.WithDefaults().Deployment, stored.Alert.EventSourceID).SchedulerConfig()
		// 单次请求最多十秒，lease 至少三十秒；不启动额外续租 goroutine。
		// 与 Worker 共用 tenant/source/fingerprint 锁，避免旧终态缓存覆盖新生命周期。
		lockConfig.LockTTL = max(lockConfig.LockTTL, 30*time.Second)
		locker, err := scheduler.NewRedisLocker(client, lockConfig)
		if err != nil {
			return result, err
		}
		cache := lifecycle.RecentAlertCache(lifecycle.NoopRecentAlertCache{})
		if runtime.Backend == config.RepositoryTypeElasticsearch {
			cache, err = recentalert.NewStore(client, recentalert.Config{KeyPrefix: cfg.Lifecycle.WithDefaults().Mailbox.KeyPrefix + ":recent-alert", RefreshInterval: cfg.Storage.Elasticsearch.ActiveAlertRefreshInterval()}, metrics.RecentAlertCacheObserver())
			if err != nil {
				return result, err
			}
		}
		processor, err := lifecycle.NewProcessor(metrics.ObserveRepository(runtime.Repository), cache, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, hooks, severity, lifecycle.SystemClock{}, logger)
		if err != nil {
			return result, err
		}
		key := mailbox.CorrelationKey(stored.Alert.BKTenantID, stored.Alert.EventSourceID, stored.Alert.Fingerprint)
		return closeUnderLease(ctx, locker, key, func() (lifecycle.CloseAlertResult, error) { return processor.CloseAlert(ctx, command) })
	}}
}

// CloseAlert 在固定预算内执行一次显式用户命令；失败可能发生在 CAS 已成功之后。
func (s *AlertCloser) CloseAlert(ctx context.Context, command lifecycle.CloseAlertCommand) (lifecycle.CloseAlertResult, error) {
	if ctx == nil {
		return lifecycle.CloseAlertResult{}, &CloseError{400, "context is required"}
	}
	if command.Validate() != nil || command.OperatorKind != domain.OperatorKindUser || domain.ValidateIdentityPart("bk_tenant_id", command.BKTenantID, 64) != nil || len(command.AlertID) > domain.EntityIDMaxBytes || len(command.OperationID) > 128 || len(command.OperatorID) > 256 || command.EffectiveAt.After(time.Now().Add(5*time.Minute)) {
		return lifecycle.CloseAlertResult{}, &CloseError{400, "invalid close command"}
	}
	select {
	case <-ctx.Done():
		return lifecycle.CloseAlertResult{}, ctx.Err()
	default:
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return lifecycle.CloseAlertResult{}, &CloseError{429, "close capacity exceeded"}
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	result, err := s.run(ctx, command)
	if err == nil {
		return result, nil
	}
	var classified *CloseError
	if errors.As(err, &classified) {
		return result, classified
	}
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, eventsource.ErrNotFound) {
		return result, &CloseError{404, "alert or source not found"}
	}
	if errors.Is(err, scheduler.ErrLockBusy) {
		return result, &CloseError{429, "alert is being processed; retry the same operation"}
	}
	if errors.Is(err, store.ErrInvalidTransition) || errors.Is(err, store.ErrVersionConflict) {
		return result, &CloseError{409, "alert state changed; refresh before another operation"}
	}
	return result, &CloseError{502, "close result uncertain; retry the same operation"}
}

func closeUnderLease(ctx context.Context, locker scheduler.Locker, key string, run func() (lifecycle.CloseAlertResult, error)) (result lifecycle.CloseAlertResult, err error) {
	lease, err := locker.Acquire(ctx, key)
	if err != nil {
		return result, err
	}
	defer func() {
		// 即使请求取消仍归还 lease；释放失败不能伪装成确定成功。
		releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		err = errors.Join(err, locker.Release(releaseCtx, lease))
	}()
	return run()
}
