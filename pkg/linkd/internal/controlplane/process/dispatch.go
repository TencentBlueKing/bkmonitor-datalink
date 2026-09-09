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
	"net"
	"net/http"
	"time"

	"linkd/internal/config"
	streams "linkd/internal/controlplane/redisstream"
	"linkd/internal/eventsource"
	sourcestore "linkd/internal/eventsource/storage"
	"linkd/internal/redisclient"
	"linkd/internal/taskdispatch"
	"linkd/internal/taskgroup"
	"linkd/internal/telemetry"
)

func runDispatch(ctx context.Context, cfg config.Config, logger *slog.Logger, metrics *telemetry.Runtime, providers ...eventsource.Provider) error {
	d := cfg.Dispatch.WithDefaults()
	if err := validateDispatchConfig(cfg); err != nil {
		return err
	}
	startup, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	docs, e := sourcestore.Open(startup, *cfg.Storage, d.Deployment)
	if e != nil {
		return e
	}
	defer func() { _ = docs.Close() }()
	sources := eventsource.New(docs, cfg.Severity, cfg.Cleaner)
	client, e := redisclient.New(cfg.Storage.Redis.ClientOptions())
	if e != nil {
		return e
	}
	defer func() { _ = client.Close() }()
	controller, e := taskdispatch.NewController(startup, client, sources, d.Deployment, metrics.DispatchObserver())
	if e != nil {
		return e
	}
	controller.CleanerDefaults = cfg.Cleaner
	if cfg.Lifecycle != nil {
		controller.LifecycleDefaults = *cfg.Lifecycle
	}
	api := &taskdispatch.API{Lifecycle: controller.LifecycleDefaults, Sources: sources, Controller: controller, Config: d}
	server := &http.Server{Addr: d.Listen, Handler: api.Handler(), ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second}
	listener, e := (&net.ListenConfig{}).Listen(startup, "tcp", d.Listen)
	if e != nil {
		return e
	}
	defer func() { _ = listener.Close() }()
	tasks := []taskgroup.Task{{Name: "scheduler", Run: controller.Run}, {Name: "source-api", Run: func(ctx context.Context) error {
		done := make(chan error, 1)
		go func() { done <- server.Serve(listener) }()
		select {
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			return server.Shutdown(shutdown)
		}
	}}}
	if cfg.ControlPlane != nil && cfg.ControlPlane.RedisStream != nil && cfg.Lifecycle != nil {
		settings := cfg.ControlPlane.RedisStream.WithDefaults()
		after := ""
		tasks = append(tasks, taskgroup.Task{Name: "source-stream-managers", Run: func(ctx context.Context) error {
			ticker := time.NewTicker(settings.ReconcileInterval())
			defer ticker.Stop()
			for {
				call, stop := context.WithTimeout(ctx, settings.OperationTimeout())
				rs, err := sources.List(call, after, 16)
				if err == nil {
					for _, r := range rs {
						after = r.ID
						lc := cfg.Lifecycle.ForSource(d.Deployment, r.ID)
						manager, e := streams.NewManager(client, streams.Config{Stream: lc.Signal.Stream, ExpectedGroup: lc.Signal.Group, ReconcileInterval: settings.ReconcileInterval(), OperationTimeout: settings.OperationTimeout(), MaxEntries: settings.MaxEntries, TrimBatchSize: settings.TrimBatchSize, MaxTrimEntriesPerCycle: settings.MaxTrimEntriesPerCycle}, metrics.RedisStreamObserver())
						if e == nil {
							e = manager.ReconcileOnce(call)
						}
						if e != nil {
							logger.WarnContext(ctx, "source stream reconcile failed", "event_source_id", r.ID)
						}
					}
					if len(rs) < 16 {
						after = ""
					}
				}
				stop()
				select {
				case <-ctx.Done():
					return nil
				case <-ticker.C:
				}
			}
		}})
	}
	for i, provider := range providers {
		tasks = append(tasks, taskgroup.Task{Name: fmt.Sprintf("source-provider-%d", i), Run: func(ctx context.Context) error {
			return sources.RunProvider(ctx, provider, 30*time.Second, func(error) { logger.WarnContext(ctx, "event source provider reconciliation failed") })
		}})
	}
	return taskgroup.Run(ctx, tasks)
}
