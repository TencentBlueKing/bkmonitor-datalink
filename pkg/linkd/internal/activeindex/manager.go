// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package activeindex

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

// Settings 是目标任务的有界扫描、轮询和查询预算。
type Settings struct {
	PollInterval      time.Duration
	ReconcileInterval time.Duration
	OperationTimeout  time.Duration
	PageSize          int
	MaxBytes          int
}

// Manager 每轮共用一次完整扫描，逐页暂存、逐策略发布，不在内存保留完整快照。
// 每个目标有独立任务和单写入者租约，目标故障不会阻塞其他目标。
type Manager struct {
	reader        Reader
	cache         Cache
	sources       []string
	settings      Settings
	logger        *slog.Logger
	nextDiscovery time.Time
}

// NewManager 创建不执行 I/O 的投影管理器。
func NewManager(reader Reader, cache Cache, sources []string, settings Settings, logger *slog.Logger) (*Manager, error) {
	q := Query{Sources: sources, PageSize: settings.PageSize, MaxBytes: settings.MaxBytes}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if reader == nil || cache == nil || logger == nil || settings.PollInterval <= 0 || settings.ReconcileInterval < settings.PollInterval || settings.OperationTimeout <= 0 || settings.OperationTimeout > time.Minute {
		return nil, fmt.Errorf("invalid active index manager")
	}
	return &Manager{reader: reader, cache: cache, sources: slices.Clone(sources), settings: settings, logger: logger}, nil
}

// Run 保留失败提示并持续校准；正常取消返回 nil，普通存储错误不会退出控制面。
func (m *Manager) Run(ctx context.Context, observers ...func(context.Context, time.Duration, int, int, error)) error {
	ticker := time.NewTicker(m.settings.PollInterval)
	defer ticker.Stop()
	for {
		started := time.Now()
		work, failed, err := m.step(ctx)
		for _, observe := range observers {
			observe(ctx, time.Since(started), work, failed, err)
		}
		if err != nil && ctx.Err() == nil {
			m.logger.WarnContext(ctx, "active index reconciliation failed", "module", "activeindex", "operation", "reconcile", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Step 在有到期提示或周期校准到期时扫描一次；同一目标的所有租户和策略共用该扫描。
func (m *Manager) Step(ctx context.Context) error {
	_, _, err := m.step(ctx)
	return err
}

func (m *Manager) step(ctx context.Context) (int, int, error) {
	call, cancel := context.WithTimeout(ctx, m.settings.OperationTimeout)
	defer cancel()
	// 租约覆盖整个目标，先取得发布权再领取提示和建立快照，避免旧快照覆盖继任者。
	lease, err := m.cache.Acquire(call, m.settings.OperationTimeout+5*time.Second)
	if err != nil || lease == "" {
		return 0, 0, err
	}
	pending := map[Scope]Pending{}
	defer func() {
		scopes := make([]Scope, 0, len(pending))
		for scope := range pending {
			scopes = append(scopes, scope)
		}
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = m.cache.Release(cleanup, lease, scopes)
	}()
	due, err := m.cache.Pending(call, MaxPending)
	if err != nil {
		return 0, 0, err
	}
	for _, p := range due {
		pending[p.Scope] = p
	}
	discovery := time.Now().After(m.nextDiscovery)
	var discoveryErr error
	if discovery {
		old, err := m.cache.Discover(call)
		discoveryErr = err
		for _, scope := range old {
			if _, exists := pending[scope]; !exists {
				pending[scope] = Pending{Scope: scope}
			}
		}
	}
	if !discovery && len(pending) == 0 {
		return 0, 0, nil
	}
	// 发现和刷新共用快照；每页只保留该页分组，临时 Set 跨页去重。
	// 本轮中新发现的策略没有领取 token，发布时不能确认扫描期间到来的提示。
	failures := map[Scope]error{}
	counts := map[Scope]int64{}
	q := Query{Sources: m.sources, PageSize: m.settings.PageSize, MaxBytes: m.settings.MaxBytes}
	readErr := m.reader.ScanActiveIndex(call, q, func(rows []Row) error {
		groups, err := Group(q, rows)
		if err != nil {
			return err
		}
		for scope, members := range groups {
			if _, exists := pending[scope]; !exists {
				if len(pending) >= MaxPending {
					return fmt.Errorf("too many active strategies")
				}
				pending[scope] = Pending{Scope: scope}
			}
			if failures[scope] != nil {
				continue
			}
			count, err := m.cache.Append(call, scope, lease, members)
			if err != nil {
				failures[scope] = err
			} else {
				counts[scope] = count
			}
		}
		return nil
	})
	if discovery {
		discoveryFailure := errors.Join(discoveryErr, readErr)
		delay := m.settings.ReconcileInterval
		if discoveryFailure != nil {
			delay = 5 * time.Second
		}
		m.nextDiscovery = time.Now().Add(delay)
		health, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		_ = m.cache.ReportDiscovery(health, discoveryFailure == nil)
		stop()
	}
	failed := 0
	report, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer stop()
	for scope, p := range pending {
		if ctx.Err() != nil {
			return len(pending), failed, ctx.Err()
		}
		err := readErr
		reason := "read_failed"
		if err == nil {
			err = failures[scope]
			reason = "staging_failed"
		}
		if err == nil {
			err = m.cache.Publish(call, p, lease, counts[scope])
			reason = "publish_failed"
		}
		if err != nil {
			failed++
			_ = m.cache.Failed(report, p, lease, reason)
			if readErr == nil {
				m.logFailure(ctx, p, err)
			}
		}
	}
	return len(pending), failed, errors.Join(discoveryErr, readErr)
}

func (m *Manager) logFailure(ctx context.Context, p Pending, err error) {
	if ctx.Err() == nil {
		m.logger.WarnContext(ctx, "active index strategy refresh failed", "module", "activeindex", "operation", "refresh_strategy", "error", err, "bk_tenant_id", p.Scope.BKTenantID, "strategy_id", p.Scope.StrategyID)
	}
}
