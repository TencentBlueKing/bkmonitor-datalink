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
	"log/slog"
	"sync"
	"time"

	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
)

// 旧目录读取最多十秒、Worker目录缓存五秒；到期后刷新正式Active再回填。
const dependencyPreparationDelay = 15 * time.Second

type dependencyIndexWork struct {
	dirty, force, prepare, initialized, running bool
	next                                        time.Time
	settleAt, lastChange                        time.Time
}

type dependencyIndexJob struct {
	tenant         string
	force, prepare bool
	started        time.Time
	settleAt       time.Time
	snapshot       runtimeconfig.Snapshot
}

// dependencyIndexManager 只在启动、新版本和实际依赖变化时回填；常态周期只发现尚未Ready的版本。
// 四个后台执行槽不会阻塞配置观察，旧job不能越过后来发布的准备截止。
type dependencyIndexManager struct {
	mu           sync.Mutex
	tenants      map[string]*dependencyIndexWork
	wake         chan struct{}
	shielder     *policyruntime.Shielder
	dependencies *policyruntime.Dependencies
	severity     *runtimeconfig.Severity
	logger       *slog.Logger
	digest       string
	now          func() time.Time
}

func (m *dependencyIndexManager) time() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

// Schedule 在首次目录响应前停用旧Ready；重复HTTP读取不会续期或重复访问Redis。
func (m *dependencyIndexManager) Schedule(ctx context.Context, tenant string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tenants == nil {
		m.tenants = map[string]*dependencyIndexWork{}
	}
	work := m.tenants[tenant]
	if work == nil {
		work = &dependencyIndexWork{}
		m.tenants[tenant] = work
	}
	if work.initialized {
		return nil
	}
	digest := m.severity.SeveritySnapshot().Digest
	if err := m.shielder.RetireDependencyNamespace(ctx, tenant, digest); err != nil {
		return err
	}
	if m.digest == "" {
		m.digest = digest
	}
	work.initialized = true
	work.dirty, work.force, work.prepare = true, true, true
	now := m.time()
	work.next = maxTime(work.next, now.Add(dependencyPreparationDelay))
	work.scheduleSettle(now)
	m.signal()
	return nil
}

func (m *dependencyIndexManager) Invalidate(tenant string) {
	m.mu.Lock()
	if m.tenants == nil {
		m.tenants = map[string]*dependencyIndexWork{}
	}
	work := m.tenants[tenant]
	if work == nil {
		work = &dependencyIndexWork{}
		m.tenants[tenant] = work
	}
	work.dirty, work.prepare = true, true
	work.next = maxTime(work.next, m.time().Add(dependencyPreparationDelay))
	m.mu.Unlock()
	m.signal()
}

// scheduleSettle 保留最早待补时间；连续内容变化不能把已承诺的补扫无限推迟。
func (w *dependencyIndexWork) scheduleSettle(now time.Time) {
	w.lastChange = now
	if w.settleAt.IsZero() {
		w.settleAt = now.Add(time.Minute)
	}
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func (m *dependencyIndexManager) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *dependencyIndexManager) prepareSeverity(ctx context.Context, snapshot runtimeconfig.Snapshot) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.digest == "" {
		m.digest = snapshot.Digest
		return nil
	}
	if m.digest == snapshot.Digest {
		return nil
	}
	for tenant, work := range m.tenants {
		// 回退也复用同一Digest；新旧namespace均先关闭，完整新回填后才Activate。
		if err := m.shielder.RetireDependencyNamespace(ctx, tenant, m.digest); err != nil {
			return err
		}
		if err := m.shielder.RetireDependencyNamespace(ctx, tenant, snapshot.Digest); err != nil {
			return err
		}
		work.dirty, work.force, work.prepare = true, true, true
		work.next = maxTime(work.next, m.time().Add(dependencyPreparationDelay))
	}
	m.digest = snapshot.Digest
	return nil
}

func (m *dependencyIndexManager) selectJobs(now time.Time, snapshot runtimeconfig.Snapshot, changed []string, periodic bool, limit int) []dependencyIndexJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tenant := range changed {
		if work := m.tenants[tenant]; work != nil {
			work.dirty, work.force = true, true
			work.scheduleSettle(now)
		}
	}
	if periodic {
		for _, work := range m.tenants {
			work.dirty = true
		}
	}
	jobs := []dependencyIndexJob{}
	for tenant, work := range m.tenants {
		if len(jobs) == limit {
			break
		}
		settle := !work.settleAt.IsZero() && !now.Before(work.settleAt)
		if work.initialized && (work.dirty || settle) && !work.running && !now.Before(work.next) {
			job := dependencyIndexJob{tenant: tenant, force: work.force || settle, prepare: work.prepare || settle, started: now, snapshot: snapshot}
			if settle {
				job.settleAt = work.settleAt
			}
			jobs = append(jobs, job)
			work.running = true
			work.dirty, work.force, work.prepare = false, false, false
		}
	}
	return jobs
}

func (m *dependencyIndexManager) jobCurrent(job dependencyIndexJob) bool {
	if m.severity.SeveritySnapshot().Digest != job.snapshot.Digest {
		return false
	}
	work := m.tenants[job.tenant]
	return work != nil && work.initialized && !work.next.After(job.started)
}

func (m *dependencyIndexManager) execute(ctx context.Context, job dependencyIndexJob) error {
	base := m.shielder.WithConfigDigest(job.snapshot.Digest).(*policyruntime.Shielder)
	// 1. 固定本次策略集合，再复核截止；发布中的新版本不能混入已启动的force任务。
	all, err := base.Loader.Catalog.Freeze(ctx, job.tenant)
	if err != nil {
		return err
	}
	m.mu.Lock()
	current := m.jobCurrent(job)
	m.mu.Unlock()
	if !current {
		return policyruntime.ErrDependencyIndexNotReady
	}
	// 2. 只在准备过渡期建立搜索可见边界，MySQL实时查询不需要refresh。
	if job.prepare || job.force {
		if reader, ok := base.Candidates.(interface{ RefreshActiveAlerts(context.Context) error }); ok {
			if err := reader.RefreshActiveAlerts(ctx); err != nil {
				return err
			}
		}
	}
	if err := base.RefreshDependencyIndexSnapshot(ctx, job.tenant, m.time().UTC(), job.snapshot.KACLevel, all, job.force); err != nil {
		return err
	}
	// 3. 完整回填成功且仍是当前准备任务，才重新开放当前namespace，包括配置回退。
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.jobCurrent(job) {
		return policyruntime.ErrDependencyIndexNotReady
	}
	if job.prepare || job.force {
		return base.ActivateDependencyNamespace(ctx, job.tenant, job.snapshot.Digest)
	}
	return nil
}

func (m *dependencyIndexManager) finish(job dependencyIndexJob, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	work := m.tenants[job.tenant]
	work.running = false
	// 最终补扫覆盖此前CAS；期间更晚的实际变化仍保留自己的60秒收敛时间。
	if err == nil && !job.settleAt.IsZero() && work.settleAt.Equal(job.settleAt) {
		work.settleAt = time.Time{}
		if next := work.lastChange.Add(time.Minute); next.After(job.started) {
			work.settleAt = next
		}
	}
	if err != nil {
		work.dirty = true
		work.force = work.force || job.force
		work.prepare = work.prepare || job.prepare
		work.next = maxTime(work.next, m.time().Add(5*time.Second))
	}
	m.signal()
}

func (m *dependencyIndexManager) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	queue := make(chan dependencyIndexJob, 4)
	var workers sync.WaitGroup
	for range 4 {
		workers.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-queue:
					err := m.execute(ctx, job)
					if err != nil && ctx.Err() == nil && !errors.Is(err, policyruntime.ErrDependencyIndexNotReady) && m.logger != nil {
						m.logger.WarnContext(ctx, "dependency candidate index refresh failed", "module", "policy", "operation", "dependency_index_refresh", "bk_tenant_id", job.tenant, "error", err)
					}
					m.finish(job, err)
				}
			}
		})
	}
	defer workers.Wait()
	lastPolicyRefresh := m.time()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-m.wake:
		case <-ticker.C:
		}
		snapshot := m.severity.SeveritySnapshot()
		if err := m.prepareSeverity(ctx, snapshot); err != nil {
			if ctx.Err() == nil && m.logger != nil {
				m.logger.WarnContext(ctx, "dependency namespace retirement failed", "module", "policy", "operation", "dependency_index_retire", "error", err)
			}
			continue
		}
		m.mu.Lock()
		startup := []string{}
		for tenant, work := range m.tenants {
			if !work.initialized {
				startup = append(startup, tenant)
			}
		}
		m.mu.Unlock()
		for _, tenant := range startup {
			if err := m.Schedule(ctx, tenant); err != nil && ctx.Err() == nil && m.logger != nil {
				m.logger.WarnContext(ctx, "dependency startup retirement failed", "module", "policy", "operation", "dependency_index_retire", "bk_tenant_id", tenant, "error", err)
			}
		}
		now := m.time()
		periodic := now.Sub(lastPolicyRefresh) >= time.Minute
		if periodic {
			lastPolicyRefresh = now
		}
		jobs := m.selectJobs(now, snapshot, m.dependencies.DrainChangedTenants(), periodic, cap(queue)-len(queue))
		for _, job := range jobs {
			select {
			case <-ctx.Done():
				return nil
			case queue <- job:
			}
		}
	}
}
