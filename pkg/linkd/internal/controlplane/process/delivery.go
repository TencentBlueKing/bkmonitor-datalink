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

	"linkd/internal/actiondelivery"
	actionproducer "linkd/internal/actiondelivery/producer"
	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/deliverysource"
	"linkd/internal/enrich"
	"linkd/internal/kaccompat"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/recentalert"
	"linkd/internal/merge"
	"linkd/internal/projection"
	projectionlock "linkd/internal/projection/redislock"
	"linkd/internal/redisclient"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/taskgroup"
	"linkd/internal/telemetry"
)

// 一个全局插件开关同时启动兼容存储和处置通知，来源和租户不再单独登记。
// 该条件和任务目录共用，不能把仅有任务管理 API 解释为运行器已启动。
func hasKACDelivery(cfg config.Config) bool {
	return cfg.Lifecycle != nil && cfg.Plugins.KACEnabled()
}

type deliveryBusinessStore struct {
	store.Repository
	store.ProjectionWorkStore
	store.ActionWorkStore
	current func(context.Context, string, string) (store.StoredAlert, error)
}

// MySQL 的普通读取已提供当前版本；ES 用专用实时读取。该边界不能依赖遥测包装器补齐可选接口。
func newDeliveryBusinessStore(repository store.Repository) (*deliveryBusinessStore, error) {
	projectionWork, ok := repository.(store.ProjectionWorkStore)
	if !ok {
		return nil, fmt.Errorf("repository does not support projection work")
	}
	actionWork, ok := repository.(store.ActionWorkStore)
	if !ok {
		return nil, fmt.Errorf("repository does not support action work")
	}
	current := repository.GetAlert
	if reader, ok := repository.(interface {
		GetAlertCurrent(context.Context, string, string) (store.StoredAlert, error)
	}); ok {
		current = reader.GetAlertCurrent
	}
	return &deliveryBusinessStore{Repository: repository, ProjectionWorkStore: projectionWork, ActionWorkStore: actionWork, current: current}, nil
}

func (r *deliveryBusinessStore) GetAlertCurrent(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	return r.current(ctx, tenant, id)
}

// runKACDelivery 只推进已有投影要求/动作意图，不创建目标绑定或解释来源目标迁移。
// 同一目标的投影、动作和管理重试复用部署租约空间；动作补扫复用正式 fingerprint lease 和近期缓存。
func runKACDelivery(ctx context.Context, cfg config.Config, relations *merge.Journal, projections projection.Store, actions actiondelivery.Store, severity *runtimeconfig.Severity, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) (runErr error) {
	if !hasKACDelivery(cfg) || cfg.Storage == nil || cfg.Storage.Redis == nil {
		return fmt.Errorf("KAC delivery requires lifecycle, storage, Redis and enabled global plugin")
	}
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	runtime, err := storeassembly.OpenExisting(startup, *cfg.Storage, 8)
	if err != nil {
		return err
	}
	defer storeassembly.JoinCloseError(&runErr, runtime)
	observed := metrics.ObserveRepository(runtime.Repository)
	business, err := newDeliveryBusinessStore(observed)
	if err != nil {
		return err
	}
	options := cfg.Storage.Redis.ClientOptions()
	options.ContextTimeoutEnabled = true
	options.PoolSize = 8
	client, err := redisclient.New(options)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, client.Close()) }()
	if err := client.Ping(startup).Err(); err != nil {
		return err
	}
	deployment := cfg.Dispatch.WithDefaults().Deployment
	locker, err := projectionlock.New(client, deployment)
	if err != nil {
		return err
	}
	resolver, err := deliverysource.New(cfg.Plugins)
	if err != nil {
		return err
	}
	projectionSender, err := kaccompat.Open(*cfg.Plugins.KAC, func(name string) (string, error) {
		if severity.SeveritySnapshot().NativeNames {
			return name, nil
		}
		switch name {
		case "critical":
			return "fatal", nil
		case "info":
			return "remind", nil
		default:
			return name, nil
		}
	}, relations)
	if err != nil {
		return err
	}
	defer projectionSender.Close()
	actionSender, err := actiondelivery.NewHTTPSender()
	if err != nil {
		return err
	}
	defer actionSender.Close()
	projector, err := projection.New(projections, business, resolver, projectionSender, locker, time.Now)
	if err != nil {
		return err
	}
	gate, err := actiondelivery.NewProjectionGate(business, projections)
	if err != nil {
		return err
	}
	deliverer, err := actiondelivery.New(actions, gate, resolver, actionSender, locker, time.Now)
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
	// Processor 仅消费已持久化的动作意图；不需要来源 Hook/Enrich，也不能在重试中重新裁决。
	finisher, err := lifecycle.NewProcessor(observed, cache, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, severity, lifecycle.SystemClock{}, logger, lifecycle.WithActionRecorder(deliverer))
	if err != nil {
		return err
	}
	producer, err := actionproducer.New(*cfg.Lifecycle, deployment, client, business, finisher)
	if err != nil {
		return err
	}
	projectionObserver, err := newProjectionTaskObserver(registry, metrics, logger)
	if err != nil {
		return err
	}
	actionObserver, err := newActionTaskObserver(registry, metrics, logger)
	if err != nil {
		return err
	}
	projectionRunner, err := projection.NewRunner(business, projections, projector, projectionObserver, time.Now)
	if err != nil {
		return err
	}
	actionRunner, err := actiondelivery.NewRunner(business, actions, producer, deliverer, actionObserver, time.Now)
	if err != nil {
		return err
	}
	return taskgroup.Run(ctx, []taskgroup.Task{{Name: "projection", Run: projectionRunner.Run}, {Name: "action", Run: actionRunner.Run}, {Name: "kac-index-maintenance", Run: func(ctx context.Context) error {
		return runKACIndexMaintenance(ctx, projectionSender, registry, metrics, logger)
	}}})
}
