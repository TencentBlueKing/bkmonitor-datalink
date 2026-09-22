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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"linkd/internal/activeindex"
	"linkd/internal/config"
	"linkd/internal/eventsource"
	"linkd/internal/redisclient"
	repositoryassembly "linkd/internal/store/assembly"
)

var errIndexPrefixOverlap = errors.New("active index prefixes overlap on the same Redis target")

type indexTarget struct {
	Locator string
	Redis   config.RedisConfig
	Prefix  string
	Sources []string
}

type indexSources interface {
	List(context.Context, string, int) ([]eventsource.Record, error)
	GetRelease(context.Context, string, int64) (eventsource.Release, error)
}

// 读取完整已发布配置再分组；不根据编辑中的 Spec 或截断来源列表推断删除。
// tombstone 和停用来源仍保留绑定，其告警只在事实存储终态后离开集合。
func loadIndexTargets(ctx context.Context, sources indexSources) (map[string]indexTarget, error) {
	targets := map[string]indexTarget{}
	after := ""
	total := 0
	for {
		records, err := sources.List(ctx, after, 64)
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			total++
			if total > 512 {
				return nil, fmt.Errorf("active index source limit exceeded")
			}
			if record.ID <= after {
				return nil, fmt.Errorf("invalid source cursor")
			}
			after = record.ID
			if record.Published == 0 {
				continue
			}
			release, err := sources.GetRelease(ctx, record.ID, record.Published)
			if err != nil {
				return nil, err
			}
			for _, rawHook := range release.Spec.Hooks {
				hook := rawHook.WithDefaults()
				if hook.Type != config.HookTypeActiveAlertByStrategy {
					continue
				}
				if err := config.ValidateHooks([]config.HookConfig{hook}); err != nil {
					return nil, err
				}
				redisConfig := hook.Config.Redis.WithDefaults()
				identity := redisConfig
				identity.Password = ""
				identity.Username = ""
				identity.Address = strings.ToLower(identity.Address)
				if identity.Sentinel != nil {
					s := *identity.Sentinel
					s.Password = ""
					s.Username = ""
					s.Addresses = slices.Clone(s.Addresses)
					for i := range s.Addresses {
						s.Addresses[i] = strings.ToLower(s.Addresses[i])
					}
					slices.Sort(s.Addresses)
					identity.Sentinel = &s
				}
				data, _ := json.Marshal(struct {
					Redis  config.RedisConfig
					Prefix string
				}{identity, hook.Config.KeyPrefix})
				sum := sha256.Sum256(data)
				id := hex.EncodeToString(sum[:])
				target, exists := targets[id]
				if !exists {
					master := ""
					var addresses []string
					if identity.Sentinel != nil {
						master = identity.Sentinel.MasterName
						addresses = identity.Sentinel.Addresses
					}
					// 只编码实例定位字段，凭据不进入跨目标重叠检查。
					location, _ := json.Marshal([]any{identity.Mode, identity.Address, identity.Database, master, addresses})
					target = indexTarget{Locator: string(location), Redis: redisConfig, Prefix: hook.Config.KeyPrefix}
				}
				if !slices.Contains(target.Sources, record.ID) {
					target.Sources = append(target.Sources, record.ID)
				}
				if len(target.Sources) > 64 {
					return nil, fmt.Errorf("active index shared source limit exceeded")
				}
				targets[id] = target
				if len(targets) > 32 {
					return nil, fmt.Errorf("active index target limit exceeded")
				}
			}
		}
		if len(records) < 64 {
			// 同实例的父前缀扫描会包含子前缀集合，无法可靠区分其租户、策略身份。
			// 在启动任何写入者前拒绝整个不完整所有权配置，保留既有缓存。
			for aID, a := range targets {
				for bID, b := range targets {
					if aID != bID && a.Locator == b.Locator && strings.HasPrefix(a.Prefix+":", b.Prefix+":") {
						return nil, errIndexPrefixOverlap
					}
				}
			}
			return targets, nil
		}
	}
}

type runningIndex struct {
	digest [32]byte
	cancel context.CancelFunc
	done   chan struct{}
}

// runActiveIndexes 按目标隔离生命周期；配置或凭据变化时先取消旧任务再装配。
// 未配置策略 Hook 时不会打开告警仓储或目标 Redis；初始化故障在任务内部重试。
func runActiveIndexes(ctx context.Context, cfg config.Config, sources indexSources, logger *slog.Logger) error {
	settings := config.ActiveIndexConfig{}.WithDefaults()
	if cfg.ControlPlane != nil && cfg.ControlPlane.ActiveIndex != nil {
		settings = cfg.ControlPlane.ActiveIndex.WithDefaults()
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	managed := map[string]runningIndex{}
	var repository *repositoryassembly.Runtime
	defer func() {
		for _, r := range managed {
			r.cancel()
		}
		for _, r := range managed {
			<-r.done
		}
		if repository != nil {
			_ = repository.Close()
		}
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		call, cancel := context.WithTimeout(ctx, 10*time.Second)
		targets, err := loadIndexTargets(call, sources)
		if err == nil && len(targets) > 0 && repository == nil {
			repository, err = repositoryassembly.Open(call, *cfg.Storage, 4)
		}
		cancel()
		if err != nil {
			reason := "configuration_unavailable"
			if errors.Is(err, errIndexPrefixOverlap) {
				reason = "overlapping_prefixes"
			}
			logger.WarnContext(ctx, "active index target discovery failed", "reason_code", reason)
		} else {
			// 配置变化先取消旧任务；同目标不会同时使用两个来源范围。
			for id, r := range managed {
				target, exists := targets[id]
				data, _ := json.Marshal(target)
				if !exists || sha256.Sum256(data) != r.digest {
					r.cancel()
					<-r.done
					delete(managed, id)
				}
			}
			for id, target := range targets {
				if _, exists := managed[id]; exists {
					continue
				}
				reader, ok := repository.Repository.(activeindex.Reader)
				if !ok {
					logger.WarnContext(ctx, "active index repository reader unavailable")
					continue
				}
				options := target.Redis.ClientOptions()
				options.ContextTimeoutEnabled = true
				options.PoolSize = 4
				client, err := redisclient.New(options)
				if err != nil {
					logger.WarnContext(ctx, "active index client initialization failed")
					continue
				}
				cache := activeindex.NewRedisCache(client, target.Prefix, settings.MaxRows, settings.MaxBytes)
				manager, err := activeindex.NewManager(reader, cache, target.Sources, activeindex.Settings{PollInterval: time.Duration(settings.PollIntervalSeconds) * time.Second, ReconcileInterval: time.Duration(settings.ReconcileIntervalSeconds) * time.Second, OperationTimeout: time.Duration(settings.OperationTimeoutSeconds) * time.Second, BatchSize: settings.BatchSize, MaxRows: settings.MaxRows, MaxBytes: settings.MaxBytes}, logger.With("index_target", id))
				if err != nil {
					_ = client.Close()
					continue
				}
				taskCtx, stop := context.WithCancel(ctx)
				done := make(chan struct{})
				data, _ := json.Marshal(target)
				managed[id] = runningIndex{sha256.Sum256(data), stop, done}
				go func() { defer close(done); defer func() { _ = client.Close() }(); _ = manager.Run(taskCtx) }()
			}
		}
		if err != nil {
			for id, r := range managed {
				r.cancel()
				<-r.done
				delete(managed, id)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
