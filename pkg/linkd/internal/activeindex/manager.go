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

// Manager 按租户合批读取完整快照，逐策略发布，不重放历史 SADD/SREM。
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
	_, _, err := m.step(ctx)
	return err
}

func (m *Manager) step(ctx context.Context) (int, int, error) {
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
		return 0, 0, errors.Join(discoveryErr, err)
	}
	// 1. 同一轮的到期提示按租户合并，保持租户首次出现的队列顺序。
	batches := make(map[string][]Pending)
	tenants := []string{}
	for _, p := range pending {
		if _, exists := batches[p.Scope.BKTenantID]; !exists {
			tenants = append(tenants, p.Scope.BKTenantID)
		}
		batches[p.Scope.BKTenantID] = append(batches[p.Scope.BKTenantID], p)
	}
	failed := 0
	for _, tenant := range tenants {
		failed += m.reconcileBatch(ctx, batches[tenant])
		if err := ctx.Err(); err != nil {
			return len(pending), failed, err
		}
	}
	return len(pending), failed, discoveryErr
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
	// 策略仍按租户合批校准，避免全局扫描超限阻止其他租户恢复。
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

// reconcileBatch 的读取只服务于本批已经持有的租约，不跨轮缓存快照。
func (m *Manager) reconcileBatch(ctx context.Context, pending []Pending) int {
	call, cancel := context.WithTimeout(ctx, m.settings.OperationTimeout)
	defer cancel()
	type heldLease struct {
		pending Pending
		token   string
	}
	held := make([]heldLease, 0, len(pending))
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer stop()
		for _, lease := range held {
			_ = m.cache.Release(cleanup, lease.pending.Scope, lease.token)
		}
	}()
	// 2. 先取得所有待发布策略的租约，再建立快照，避免旧快照覆盖其他持有者的新结果。
	failed := 0
	for _, p := range pending {
		lease, err := m.cache.Acquire(call, p.Scope, m.settings.OperationTimeout+5*time.Second)
		if err != nil {
			failed++
			m.logFailure(ctx, p)
			continue
		}
		if lease != "" {
			held = append(held, heldLease{pending: p, token: lease})
		}
	}
	if len(held) == 0 {
		return failed
	}
	q := m.query(nil)
	q.BKTenantID = pending[0].Scope.BKTenantID
	rows, readErr := m.reader.ReadActiveIndex(call, q)
	reason := "read_failed"
	var groups map[Scope][]string
	if readErr == nil {
		groups, readErr = Group(q, rows)
		reason = "invalid_snapshot"
	}
	// 3. 各策略独立原子发布；失败保留旧集合，token 检查保留扫描期间的新提示。
	for _, lease := range held {
		if ctx.Err() != nil {
			break
		}
		err := readErr
		failureReason := reason
		if err == nil {
			err = m.cache.Publish(call, lease.pending, lease.token, groups[lease.pending.Scope])
			failureReason = "publish_failed"
		}
		if err != nil {
			failed++
			cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			_ = m.cache.Failed(cleanup, lease.pending, lease.token, failureReason)
			stop()
			m.logFailure(ctx, lease.pending)
		}
	}
	return failed
}

func (m *Manager) logFailure(ctx context.Context, p Pending) {
	if ctx.Err() == nil {
		m.logger.WarnContext(ctx, "active index strategy refresh failed", "bk_tenant_id", p.Scope.BKTenantID, "strategy_id", p.Scope.StrategyID)
	}
}
