// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplaneprocess

import (
	"context"
	"errors"
	"log/slog"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/activeindex"
	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle/recentalert"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/redisclient"
	"linkd/internal/store"
	repositoryassembly "linkd/internal/store/assembly"
	"linkd/internal/telemetry"
)

type indexSources interface {
	List(context.Context, string, int) ([]eventsource.Record, error)
	GetRelease(context.Context, string, int64) (eventsource.Release, error)
}

// runActiveIndexes 共用内部 Redis，周期修复已发布来源；配置和存储故障不阻塞调度/API。
func runActiveIndexes(ctx context.Context, cfg config.Config, sources indexSources, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) error {
	if cfg.Storage == nil || cfg.Storage.Redis == nil {
		return nil
	}
	settings := config.ActiveIndexConfig{}.WithDefaults()
	if cfg.ControlPlane != nil && cfg.ControlPlane.ActiveIndex != nil {
		settings = cfg.ControlPlane.ActiveIndex.WithDefaults()
	}
	options := cfg.Storage.Redis.ClientOptions()
	options.ContextTimeoutEnabled = true
	client, err := redisclient.New(options)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	var repository *repositoryassembly.Runtime
	defer func() {
		if repository != nil {
			_ = repository.Close()
		}
	}()
	ticker := time.NewTicker(time.Duration(settings.PollIntervalSeconds) * time.Second)
	defer ticker.Stop()
	nextRun := time.Time{}
	for {
		if !time.Now().Before(nextRun) {
			registry.Begin("active-alert-indexes", "", "")
			started := time.Now()
			after := ""
			roundErr := error(nil)
			for {
				records, err := sources.List(ctx, after, 64)
				if err != nil {
					roundErr = err
					break
				}
				for _, record := range records {
					after = record.ID
					if record.Published == 0 {
						continue
					}
					release, err := sources.GetRelease(ctx, record.ID, record.Published)
					if err != nil {
						roundErr = errors.Join(roundErr, err)
						continue
					}
					// 清空规则后停止维护；停用/删除来源不代表其中的告警已经终结。
					if release.Spec.ActiveAlertLimits == nil || len(release.Spec.ActiveAlertLimits.Rules) == 0 {
						continue
					}
					if repository == nil {
						repository, err = repositoryassembly.OpenExisting(ctx, *cfg.Storage, 4)
						if err != nil {
							roundErr = errors.Join(roundErr, err)
							continue
						}
					}
					reader, ok := repository.Repository.(activeindex.Reader)
					if !ok {
						continue
					}
					call, cancel := context.WithTimeout(ctx, time.Duration(settings.OperationTimeoutSeconds)*time.Second)
					err = reconcileActiveSource(call, cfg, release.Spec, activeindex.New(client, cfg.Dispatch.WithDefaults().Deployment, release.Spec, metrics), reader, repository.Repository, client, settings)
					cancel()
					if err != nil {
						roundErr = errors.Join(roundErr, err)
					}
				}
				if len(records) < 64 {
					break
				}
			}
			code := ""
			if roundErr != nil {
				code = "reconcile_failed"
				logger.WarnContext(ctx, "active alert cache reconciliation failed", "module", "controlplane", "operation", "active_index_reconcile", "error", roundErr)
			}
			registry.Finish(ctx, "active-alert-indexes", "", "", time.Since(started), 0, 0, code)
			nextRun = time.Now().Add(time.Duration(settings.ReconcileIntervalSeconds) * time.Second)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func reconcileActiveSource(ctx context.Context, cfg config.Config, source config.EventSource, cache *activeindex.Store, reader activeindex.Reader, repository store.Repository, client redis.UniversalClient, settings config.ActiveIndexConfig) error {
	lifecycleConfig := config.LifecycleConfig{}
	if cfg.Lifecycle != nil {
		lifecycleConfig = *cfg.Lifecycle
	}
	lockConfig := lifecycleConfig.ForSource(cfg.Dispatch.WithDefaults().Deployment, source.EventSourceID).SchedulerConfig()
	locker, err := scheduler.NewRedisLocker(client, lockConfig)
	if err != nil {
		return err
	}
	var recent *recentalert.Store
	if cfg.Storage.Repository == config.RepositoryTypeElasticsearch {
		recent, err = recentalert.NewStore(client, recentalert.Config{KeyPrefix: lifecycleConfig.WithDefaults().Mailbox.KeyPrefix + ":recent-alert", RefreshInterval: cfg.Storage.Elasticsearch.ActiveAlertRefreshInterval()}, nil)
		if err != nil {
			return err
		}
	}
	get := repository.GetAlert
	if current, ok := repository.(store.LifecycleAlertStore); ok {
		get = current.GetAlertCurrent
	}
	return cache.Reconcile(ctx, reader, settings.PageSize, settings.MaxBytes, func(ctx context.Context, row activeindex.RepairCandidate) error {
		// 单项修复预算小于已有 lease；竞争时留给下轮，不等待或阻塞 Worker。
		call, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		lease, err := locker.Acquire(call, scheduler.CorrelationKey(row.BKTenantID, row.EventSourceID, row.Fingerprint))
		if errors.Is(err, scheduler.ErrLockBusy) {
			return nil
		}
		if err != nil {
			return err
		}
		defer func() {
			release, stop := context.WithTimeout(context.WithoutCancel(call), time.Second)
			defer stop()
			_ = locker.Release(release, lease)
		}()
		key := store.ActiveAlertKey{BKTenantID: row.BKTenantID, EventSourceID: row.EventSourceID, Fingerprint: row.Fingerprint}
		var active store.StoredAlert
		found := false
		if recent != nil {
			active, found, err = recent.GetCurrent(call, key)
			if err != nil {
				return err
			}
		}
		if !found && row.AlertID != "" {
			active, err = get(call, row.BKTenantID, row.AlertID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			found = err == nil && active.Alert.Status == domain.AlertStatusActive
		}
		if !found {
			active, err = repository.FindActiveAlert(call, key)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
		}
		if active.Alert.Status == domain.AlertStatusActive {
			return cache.RepairMember(call, row, &active.Alert)
		}
		return cache.RepairMember(call, row, nil)
	})
}
