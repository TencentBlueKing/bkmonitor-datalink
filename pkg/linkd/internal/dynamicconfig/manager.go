// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package dynamicconfig 同步选定业务配置并持久化最后有效快照；来源故障不清空正在使用的配置。
package dynamicconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"linkd/internal/config"
	"linkd/internal/runtimeconfig"
)

// ErrNotFound 表示当前作用域尚未保存快照。
var ErrNotFound = errors.New("dynamic config snapshot not found")

// ErrConflict 表示快照已被另一个 CAS 更新，调用方必须重新读取。
var ErrConflict = errors.New("dynamic config snapshot conflict")

// Source 只读上游；Read 必须返回完整字段值，不以空值表达读取失败。
type Source interface {
	Read(context.Context) (json.RawMessage, error)
	Close() error
}

// WatchSource 通知只表示需要重新读取；允许合并通知，重连时必须触发补读。
type WatchSource interface {
	Watch(context.Context, func()) error
}

// Store 以不透明 token 提供单对象 CAS；空 token 只允许创建。
type Store interface {
	Load(context.Context, string) (Record, string, error)
	Save(context.Context, string, string, Record) error
}

// Record 是最后有效业务配置，不含来源凭据，也不承担历史版本回放。
type Record struct {
	SchemaVersion  int                    `json:"schema_version"`
	Deployment     string                 `json:"deployment"`
	Item           string                 `json:"item"`
	SourceIdentity string                 `json:"source_identity"`
	PersistedAt    time.Time              `json:"persisted_at"`
	Snapshot       runtimeconfig.Snapshot `json:"snapshot"`
}

// Status 区分有效值来源与最近一次同步结果，失败不能冒充配置已清空。
type Status struct {
	Enabled     bool                   `json:"enabled"`
	Source      string                 `json:"source,omitempty"`
	SourceType  string                 `json:"source_type,omitempty"`
	BKTenantID  string                 `json:"bk_tenant_id,omitempty"`
	Origin      string                 `json:"origin"`
	SyncState   string                 `json:"sync_state"`
	LastAttempt *time.Time             `json:"last_attempt,omitempty"`
	LastSuccess *time.Time             `json:"last_success,omitempty"`
	PersistedAt *time.Time             `json:"persisted_at,omitempty"`
	Error       string                 `json:"error,omitempty"`
	Current     runtimeconfig.Snapshot `json:"current"`
}

// Manager 串行读取和保存一个配置项；状态读取与业务快照替换均可并发。
type Manager struct {
	settings                                   config.DynamicSourceConfig
	source                                     Source
	store                                      Store
	state                                      *runtimeconfig.Severity
	logger                                     *slog.Logger
	deployment, identity, key, defaultSeverity string
	mu                                         sync.RWMutex
	status                                     Status
}

// New 构造等级同步器，既不读取存储也不启动 goroutine。
func New(c config.DynamicConfigConfig, deployment string, fallback config.SeverityConfig, state *runtimeconfig.Severity, source Source, store Store, logger *slog.Logger) (*Manager, error) {
	if !c.Enabled {
		return nil, fmt.Errorf("dynamic config is disabled")
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if state == nil || source == nil || store == nil || logger == nil {
		return nil, fmt.Errorf("dynamic config dependencies are required")
	}
	b := *c.Bindings.Severity
	s := c.Sources[b.Source].WithDefaults()
	identity := SourceIdentity(s, b)
	m := &Manager{settings: s, source: source, store: store, state: state, logger: logger, deployment: deployment, identity: identity, defaultSeverity: fallback.WithDefaults().DefaultSeverity}
	m.key = hashJSON([]string{deployment, "severity", identity})
	m.status = Status{Enabled: true, Source: b.Source, SourceType: s.Type, BKTenantID: s.BKTenantID, Origin: "yaml", SyncState: "pending"}
	if err := state.Install(runtimeconfig.Snapshot{Enabled: true, Severity: fallback.WithDefaults()}); err != nil {
		return nil, err
	}
	return m, nil
}

// SourceIdentity 隔离上游定位与租户；用户名和密码轮换不改变配置身份。
func SourceIdentity(s config.DynamicSourceConfig, b config.DynamicBinding) string {
	s = s.WithDefaults()
	identity := map[string]any{"type": s.Type, "tenant": s.BKTenantID, "key": b.Key}
	if s.Type == config.DynamicSourceAlarmLevel && s.MySQL != nil {
		identity["address"], identity["database"], identity["table"] = s.MySQL.Address, s.MySQL.Database, s.Table
	}
	if s.Type == config.DynamicSourceRedis && s.Redis != nil {
		r := s.Redis.WithDefaults()
		identity["mode"], identity["address"], identity["database"], identity["prefix"] = r.Mode, r.Address, r.Database, s.RedisKeyPrefix
		if r.Sentinel != nil {
			identity["master"] = r.Sentinel.MasterName
			identity["sentinels"] = r.Sentinel.Addresses
		}
	}
	return hashJSON(identity)
}

func hashJSON(value any) string {
	b, _ := json.Marshal(value)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Status 返回诊断副本；只包含配置值及非敏感来源标识。
func (m *Manager) Status() Status {
	m.mu.RLock()
	s := m.status
	m.mu.RUnlock()
	s.Current = m.state.SeveritySnapshot()
	return s
}

// Bootstrap 优先恢复持久化快照；没有可信快照才有界拉取上游，任何失败保留 YAML。
func (m *Manager) Bootstrap(ctx context.Context) {
	call, cancel := context.WithTimeout(ctx, m.settings.Timeout())
	r, _, err := m.store.Load(call, m.key)
	cancel()
	if err == nil && m.validateRecord(r) == nil {
		if err = m.state.Install(r.Snapshot); err == nil {
			m.mu.Lock()
			m.status.Origin = "persisted"
			m.status.SyncState = "restored"
			m.status.PersistedAt = &r.PersistedAt
			m.mu.Unlock()
			return
		}
	}
	m.Sync(ctx)
}

func (m *Manager) validateRecord(r Record) error {
	if r.SchemaVersion != 1 || r.Deployment != m.deployment || r.Item != "severity" || r.SourceIdentity != m.identity || r.PersistedAt.IsZero() || !r.Snapshot.Enabled || !r.Snapshot.NativeNames {
		return fmt.Errorf("snapshot scope or schema mismatch")
	}
	n, err := r.Snapshot.Normalize()
	if err != nil {
		return err
	}
	if n.Digest != r.Snapshot.Digest {
		return fmt.Errorf("snapshot digest mismatch")
	}
	return nil
}

// Sync 串行执行一次读取、校验、CAS、发布；失败不会改变当前有效值。
// 调用者不得并发调用 Bootstrap、Sync 或 Run。
func (m *Manager) Sync(ctx context.Context) {
	now := time.Now().UTC()
	m.mu.Lock()
	m.status.LastAttempt = &now
	m.mu.Unlock()
	call, cancel := context.WithTimeout(ctx, m.settings.Timeout())
	raw, err := m.source.Read(call)
	cancel()
	if err != nil {
		m.failed(ctx, "source_error")
		return
	}
	snapshot, err := decodeLevels(raw, m.defaultSeverity)
	if err != nil {
		m.failed(ctx, "invalid_config")
		return
	}
	call, cancel = context.WithTimeout(ctx, m.settings.Timeout())
	previous, token, err := m.store.Load(call, m.key)
	cancel()
	if err != nil && !errors.Is(err, ErrNotFound) && token == "" {
		m.failed(ctx, "persistence_error")
		return
	}
	r := Record{SchemaVersion: 1, Deployment: m.deployment, Item: "severity", SourceIdentity: m.identity, PersistedAt: now, Snapshot: snapshot}
	if err == nil && m.validateRecord(previous) == nil && previous.Snapshot.Digest == snapshot.Digest {
		r = previous
	} else {
		call, cancel = context.WithTimeout(ctx, m.settings.Timeout())
		err = m.store.Save(call, m.key, token, r)
		cancel()
		if err != nil {
			m.failed(ctx, "persistence_error")
			return
		}
	}
	// 保存成功后崩溃可在下次启动恢复；绝不先发布尚未持久化的配置。
	if err = m.state.Install(r.Snapshot); err != nil {
		m.failed(ctx, "invalid_config")
		return
	}
	m.mu.Lock()
	m.status.Origin = "upstream"
	m.status.SyncState = "synced"
	m.status.Error = ""
	m.status.LastSuccess = &now
	m.status.PersistedAt = &r.PersistedAt
	m.mu.Unlock()
}

func (m *Manager) failed(ctx context.Context, code string) {
	m.mu.Lock()
	m.status.SyncState = code
	m.status.Error = code
	m.mu.Unlock()
	// 不记录驱动错误中的 SQL、远端载荷或连接材料。
	m.logger.WarnContext(ctx, "dynamic config synchronization failed; retaining current configuration", "source", m.status.Source, "stage", code)
}

// RunObserver 只接收串行同步的开始与完整结果；实现不得执行外部 I/O。
type RunObserver interface {
	SyncStarted()
	SyncFinished(context.Context, time.Duration, Status)
}

// Run 合并通知并周期补读；单来源失败只更新状态，取消后等待 watch 退出。
func (m *Manager) Run(ctx context.Context, observers ...RunObserver) error {
	syncOnce := func() {
		started := time.Now()
		for _, o := range observers {
			o.SyncStarted()
		}
		m.Sync(ctx)
		for _, observe := range observers {
			observe.SyncFinished(ctx, time.Since(started), m.Status())
		}
	}
	notifications := make(chan struct{}, 1)
	notify := func() {
		select {
		case notifications <- struct{}{}:
		default:
		}
	}
	watchCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	if watcher, ok := m.source.(WatchSource); ok {
		go func() { defer close(done); _ = watcher.Watch(watchCtx, notify) }()
	} else {
		close(done)
	}
	defer func() { cancel(); <-done }()
	ticker := time.NewTicker(m.settings.Interval())
	defer ticker.Stop()
	// Bootstrap 恢复快照后立即后台尝试同步，不让恢复依赖上游可用。
	syncOnce()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-notifications:
			syncOnce()
		case <-ticker.C:
			syncOnce()
		}
	}
}

func decodeLevels(raw json.RawMessage, defaultName string) (runtimeconfig.Snapshot, error) {
	if len(raw) == 0 || len(raw) > 1<<20 {
		return runtimeconfig.Snapshot{}, fmt.Errorf("invalid config payload size")
	}
	var wire []struct {
		Name     string `json:"name"`
		Priority *int   `json:"priority"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return runtimeconfig.Snapshot{}, err
	}
	if len(wire) == 0 || len(wire) > 256 {
		return runtimeconfig.Snapshot{}, fmt.Errorf("invalid level count")
	}
	levels := make([]config.SeverityLevel, 0, len(wire))
	for _, item := range wire {
		if item.Priority == nil {
			return runtimeconfig.Snapshot{}, fmt.Errorf("priority is required")
		}
		levels = append(levels, config.SeverityLevel{Name: item.Name, Priority: *item.Priority})
	}
	c := config.SeverityConfig{DefaultSeverity: defaultName, Levels: levels}
	// 上游没有 default_severity 字段。删除 YAML 默认名时选择当前最轻等级，
	// 只用于原始值映射的默认值；已经清洗的未知 Event 仍由 Lifecycle 拒绝。
	if !c.Has(defaultName) {
		last := levels[0]
		for _, level := range levels[1:] {
			if level.Priority > last.Priority {
				last = level
			}
		}
		c.DefaultSeverity = last.Name
	}
	return (runtimeconfig.Snapshot{Enabled: true, NativeNames: true, Severity: c}).Normalize()
}
