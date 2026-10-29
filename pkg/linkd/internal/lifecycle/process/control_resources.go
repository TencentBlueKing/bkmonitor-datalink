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
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle"
	"linkd/internal/redisclient"
	"linkd/internal/runtimeconfig"
	storeassembly "linkd/internal/store/assembly"
	mysqlstore "linkd/internal/store/mysql"
	"linkd/internal/suppressioncleanup"
	"linkd/internal/telemetry"
)

type controlResourcesKey struct{}

type controlHookKey struct {
	source  string
	version int64
}

type controlHooks struct {
	release eventsource.Release
	hooks   []lifecycle.NamedFinalHook
	close   func() error
	users   int
}

func (r *AlertProcessorResources) sourceRelease(ctx context.Context, sources closeSourceReader, id string, version int64) (eventsource.Release, error) {
	if releases, ok := ctx.Value(controlReleaseBatchKey{}).(map[controlHookKey]controlReleaseResult); ok {
		if item, found := releases[controlHookKey{id, version}]; found {
			return item.release, item.err
		}
	}
	r.mu.Lock()
	entry := r.hooks[controlHookKey{id, version}]
	if entry != nil && entry.release.ID != "" {
		result := entry.release
		r.mu.Unlock()
		return result, nil
	}
	r.mu.Unlock()
	return sources.GetRelease(ctx, id, version)
}

// AlertProcessorResources 在控制任务生命周期内复用连接及同一来源 Release 的 Hook。
// 关闭必须在所有控制任务结束后进行；不缓存 Alert 实时状态或 fingerprint lease。
type AlertProcessorResources struct {
	repository *storeassembly.Runtime
	client     *redis.Client
	mu         sync.Mutex
	hooks      map[controlHookKey]*controlHooks
	latest     map[string]controlHookKey
	closeBatch func()
	cleanup    suppressioncleanup.Recorder
}

// UseCleanupRecorder 在控制面启动时复用已就绪的清理 Journal，避免每条终态检查存储 schema。
func (r *AlertProcessorResources) UseCleanupRecorder(recorder suppressioncleanup.Recorder) {
	r.cleanup = recorder
}

// OpenAlertProcessorResources 供已启用 Lifecycle 的控制面启动一次，不迁移存储 schema。
func OpenAlertProcessorResources(ctx context.Context, cfg config.Config) (*AlertProcessorResources, error) {
	r, err := storeassembly.OpenExisting(ctx, *cfg.Storage, 16)
	if err != nil {
		return nil, err
	}
	options := cfg.Storage.Redis.ClientOptions()
	options.ContextTimeoutEnabled = true
	options.PoolSize = 8
	client, err := redisclient.New(options)
	if err != nil {
		return nil, errors.Join(err, r.Close())
	}
	resources := &AlertProcessorResources{repository: r, client: client, hooks: map[controlHookKey]*controlHooks{}, latest: map[string]controlHookKey{}}
	if repo, ok := r.Repository.(*mysqlstore.Repository); ok {
		batched, closeBatch, err := repo.EnableWriteBatch(cfg.MergeControlSettings().Concurrency, 4<<20, 10*time.Second)
		if err != nil {
			return nil, errors.Join(err, resources.Close())
		}
		r.Repository, resources.closeBatch = batched, closeBatch
	}
	return resources, nil
}

func (r *AlertProcessorResources) context(ctx context.Context) context.Context {
	if r == nil {
		return ctx
	}
	return context.WithValue(ctx, controlResourcesKey{}, r)
}

func (r *AlertProcessorResources) acquireHooks(release eventsource.Release, cfg config.Config, metrics *telemetry.Runtime, severity *runtimeconfig.Severity) ([]lifecycle.NamedFinalHook, func() error, error) {
	key := controlHookKey{release.ID, release.Version}
	r.mu.Lock()
	entry := r.hooks[key]
	if entry == nil {
		hooks, closeHooks, err := openHooksWithSeverity(kacHooks(release.Spec.Hooks, cfg.Plugins.KACEnabled()), metrics, severity)
		if err != nil {
			r.mu.Unlock()
			return nil, nil, err
		}
		entry = &controlHooks{release: release, hooks: hooks, close: closeHooks}
		r.hooks[key] = entry
	}
	entry.users++
	versionChanged := r.latest[key.source] != key
	r.latest[key.source] = key
	var retired []func() error
	// 稳定版本命中只修改自己的引用计数；旧活跃版本在最后一个使用者释放时回收。
	if versionChanged {
		for otherKey, other := range r.hooks {
			if otherKey.source == key.source && otherKey != key && other.users == 0 {
				delete(r.hooks, otherKey)
				retired = append(retired, other.close)
			}
		}
	}
	r.mu.Unlock()
	// 关闭客户端可能等待内部工作；引用归零并摘除后即可在锁外回收，不能阻塞其他来源。
	var closeErr error
	for _, closeHook := range retired {
		closeErr = errors.Join(closeErr, closeHook())
	}
	releaseHooks := func() error {
		r.mu.Lock()
		entry.users--
		var retired func() error
		if entry.users == 0 && r.latest[key.source] != key {
			delete(r.hooks, key)
			retired = entry.close
		}
		r.mu.Unlock()
		if retired != nil {
			return errors.Join(closeErr, retired())
		}
		return closeErr
	}
	return entry.hooks, releaseHooks, nil
}

func (r *AlertProcessorResources) observePoolWait(ctx context.Context, metrics *telemetry.Runtime) {
	if r.client != nil {
		s := r.client.PoolStats()
		metrics.ObserveMergePoolWait(ctx, "redis", int64(s.WaitCount), time.Duration(s.WaitDurationNs))
	}
	if repo, ok := r.repository.Repository.(*mysqlstore.Repository); ok {
		s := repo.PoolStats()
		metrics.ObserveMergePoolWait(ctx, "mysql", s.WaitCount, s.WaitDuration)
	}
}

// Close 在任务排空后关闭共用 Hook 和连接。
func (r *AlertProcessorResources) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	retired := make([]func() error, 0, len(r.hooks))
	for key, entry := range r.hooks {
		delete(r.hooks, key)
		retired = append(retired, entry.close)
	}
	r.mu.Unlock()
	var err error
	for _, closeHook := range retired {
		err = errors.Join(err, closeHook())
	}
	if r.closeBatch != nil {
		r.closeBatch()
	}
	return errors.Join(err, r.client.Close(), r.repository.Close())
}
