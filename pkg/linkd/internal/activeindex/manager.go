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
	BatchSize         int
	MaxRows           int
	MaxBytes          int
}

// Manager 逐策略串行执行完整快照发布，不重放历史 SADD/SREM。
// 每个目标有独立任务，目标故障不会阻塞其他目标；同策略并发由目标 Redis 租约隔离。
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
	q := Query{Sources: sources, MaxRows: settings.MaxRows, MaxBytes: settings.MaxBytes}
	if err := q.Validate(); err != nil {
		return nil, err
	}
	if reader == nil || cache == nil || logger == nil || settings.PollInterval <= 0 || settings.ReconcileInterval < settings.PollInterval || settings.OperationTimeout <= 0 || settings.OperationTimeout > time.Minute || settings.BatchSize < 1 || settings.BatchSize > 100 {
		return nil, fmt.Errorf("invalid active index manager")
	}
	return &Manager{reader: reader, cache: cache, sources: slices.Clone(sources), settings: settings, logger: logger}, nil
}

// Run 保留失败提示并持续校准；正常取消返回 nil，普通存储错误不会退出控制面。
func (m *Manager) Run(ctx context.Context) error {
	ticker := time.NewTicker(m.settings.PollInterval)
	defer ticker.Stop()
	for {
		if err := m.Step(ctx); err != nil && ctx.Err() == nil {
			m.logger.WarnContext(ctx, "active index reconciliation failed")
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// Step 先周期发现，再处理有界到期策略；发现失败仍允许其他已排队策略修复。
func (m *Manager) Step(ctx context.Context) error {
	var discoveryErr error
	if time.Now().After(m.nextDiscovery) {
		call, cancel := context.WithTimeout(ctx, m.settings.OperationTimeout)
		discoveryErr = m.discover(call)
		cancel()
		if discoveryErr == nil {
			m.nextDiscovery = time.Now().Add(m.settings.ReconcileInterval)
		} else {
			m.nextDiscovery = time.Now().Add(5 * time.Second)
		}
		health, stop := context.WithTimeout(ctx, time.Second)
		_ = m.cache.ReportDiscovery(health, discoveryErr == nil)
		stop()
	}
	call, cancel := context.WithTimeout(ctx, m.settings.OperationTimeout)
	pending, err := m.cache.Pending(call, m.settings.BatchSize)
	cancel()
	if err != nil {
		return errors.Join(discoveryErr, err)
	}
	for _, p := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.reconcile(ctx, p); err != nil && ctx.Err() == nil {
			m.logger.WarnContext(ctx, "active index strategy refresh failed", "bk_tenant_id", p.Scope.BKTenantID, "strategy_id", p.Scope.StrategyID)
		}
	}
	return discoveryErr
}

func (m *Manager) query(scope *Scope) Query {
	return Query{Sources: m.sources, Scope: scope, MaxRows: m.settings.MaxRows, MaxBytes: m.settings.MaxBytes}
}

func (m *Manager) discover(ctx context.Context) error {
	q := m.query(nil)
	rows, readErr := m.reader.ReadActiveIndex(ctx, q)
	groups := map[Scope][]string{}
	if readErr == nil {
		groups, readErr = Group(q, rows)
	}
	if readErr != nil {
		groups = map[Scope][]string{}
	}
	// 发现只产生重读提示，不据此发布或删除。某一侧发现失败时，另一侧已知
	// 策略仍可逐个校准，避免一个超大策略阻止其他现有集合恢复。
	old, scanErr := m.cache.Discover(ctx)
	for _, s := range old {
		if _, ok := groups[s]; !ok {
			groups[s] = nil
		}
	}
	if len(groups) > MaxPending {
		return fmt.Errorf("too many active strategies")
	}
	for s := range groups {
		if err := m.cache.Enqueue(ctx, s); err != nil {
			return err
		}
	}
	return errors.Join(readErr, scanErr)
}

func (m *Manager) reconcile(ctx context.Context, p Pending) error {
	call, cancel := context.WithTimeout(ctx, m.settings.OperationTimeout)
	defer cancel()
	lease, err := m.cache.Acquire(call, p.Scope, m.settings.OperationTimeout+5*time.Second)
	if err != nil || lease == "" {
		return err
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = m.cache.Release(cleanup, p.Scope, lease)
	}()
	q := m.query(&p.Scope)
	rows, err := m.reader.ReadActiveIndex(call, q)
	reason := "read_failed"
	var groups map[Scope][]string
	if err == nil {
		groups, err = Group(q, rows)
		reason = "invalid_snapshot"
	}
	if err == nil {
		err = m.cache.Publish(call, p, lease, groups[p.Scope])
		reason = "publish_failed"
	}
	if err != nil {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		_ = m.cache.Failed(cleanup, p, lease, reason)
	}
	return err
}
