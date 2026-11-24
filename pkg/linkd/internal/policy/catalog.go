// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"
)

// RecordReader 只读取带发布指针的策略记录；查询必须限定显式租户和类型。
type RecordReader interface {
	List(context.Context, Scope, string, int) ([]Record, error)
}

// PublishedReader 让 Worker 一次读取完整发布目录，避免逐页控制面 HTTP 请求。
// 普通本地 Service 仍使用 RecordReader 分页，缓存只发布完整目录。
type PublishedReader interface {
	PublishedPolicies(context.Context, string) ([]Release, error)
}

// FrozenPolicy 在一次 Event 裁决内固定配置版本和编译结果，消费者只能读取，不得修改。
type FrozenPolicy struct {
	Release  Release
	Compiled *Compiled
}

type releaseIdentity struct {
	scope   Scope
	id      string
	version int64
	digest  string
}

type policyIdentity struct {
	scope Scope
	id    string
}

type catalogEntry struct {
	candidates *candidateIndex
	frozen     map[releaseIdentity]FrozenPolicy
	latest     map[policyIdentity]FrozenPolicy
	policies   []FrozenPolicy
	expires    time.Time
	refreshAt  time.Time
	used       time.Time
	bytes      int
}

// Catalog 默认保留已发布配置一分钟，每五秒后台刷新；跨租户最多 32 项/32 MiB 原配置。
// 失败不复用已经过期的快照；仅返回完整一次读取，不能把半页当作策略停用。
type Catalog struct {
	reader          RecordReader
	mu              sync.Mutex
	entries         map[string]catalogEntry
	loading         chan struct{}
	ttl             time.Duration
	refreshInterval time.Duration
	dirty           map[string]bool
	reading         string
	now             func() time.Time
}

// NewCatalog 装配受租户约束的 Reader；后台调度与运行时加载分离。
func NewCatalog(reader RecordReader) *Catalog {
	c := NewCatalogWithTTL(reader, time.Minute)
	c.refreshInterval = 5 * time.Second
	return c
}

// NewCatalogWithTTL 为控制面目录设置可用时间和刷新周期；发布时仍立即失效对应租户。
func NewCatalogWithTTL(reader RecordReader, ttl time.Duration) *Catalog {
	return &Catalog{reader: reader, entries: map[string]catalogEntry{}, loading: make(chan struct{}, 1), ttl: ttl, refreshInterval: ttl, dirty: make(map[string]bool), now: time.Now}
}

// Invalidate 只让指定租户目录过期，保留在途裁决需要的精确旧版本。
// 刷新期间收到的失效不能被旧读取完成覆盖，下一轮仍需补读。
func (c *Catalog) Invalidate(tenant string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[tenant]; ok {
		entry.expires = time.Time{}
		entry.refreshAt = time.Time{}
		c.entries[tenant] = entry
		c.dirty[tenant] = true
	} else if c.reading == tenant {
		c.dirty[tenant] = true
	}
}

// Freeze 返回当前 Event 使用的只读快照；slice 是副本，Compiled/Release 为不可变共享内容。
func (c *Catalog) Freeze(ctx context.Context, tenant string) ([]FrozenPolicy, error) {
	entry, err := c.loadEntry(ctx, tenant, false)
	if err != nil {
		return nil, err
	}
	return slices.Clone(entry.policies), nil
}

// Candidates 返回原优先级下的可能命中规则，不复制或遍历完整策略列表。
func (c *Catalog) Candidates(ctx context.Context, tenant string, reader CandidateReader) ([]FrozenPolicy, error) {
	entry, err := c.loadEntry(ctx, tenant, false)
	if err != nil {
		return nil, err
	}
	return entry.candidates.selectPolicies(ctx, reader)
}

func (c *Catalog) loadEntry(ctx context.Context, tenant string, refresh bool) (catalogEntry, error) {
	if err := ctx.Err(); err != nil {
		return catalogEntry{}, err
	}
	if err := (Scope{TenantID: tenant, Kind: Suppression}).Validate(); err != nil {
		return catalogEntry{}, err
	}
	if c.reader == nil {
		return catalogEntry{}, ErrUnavailable
	}
	if !refresh {
		if entry, ok := c.cached(tenant); ok {
			return entry, nil
		}
	}
	select {
	case c.loading <- struct{}{}:
		defer func() { <-c.loading }()
	case <-ctx.Done():
		return catalogEntry{}, ctx.Err()
	}
	if !refresh {
		if entry, ok := c.cached(tenant); ok {
			return entry, nil
		}
	}
	c.mu.Lock()
	c.reading = tenant
	delete(c.dirty, tenant)
	c.mu.Unlock()
	defer func() { c.mu.Lock(); c.reading = ""; c.mu.Unlock() }()
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	policies, size, err := c.read(call, tenant)
	if err != nil {
		return catalogEntry{}, err
	}
	if err := call.Err(); err != nil {
		return catalogEntry{}, err
	}
	c.mu.Lock()
	previous := c.entries[tenant]
	c.mu.Unlock()
	entry := catalogEntry{policies: policies, candidates: newCandidateIndex(policies), frozen: make(map[releaseIdentity]FrozenPolicy), latest: make(map[policyIdentity]FrozenPolicy), bytes: size}
	// 在途求值可跨刷新，保留上一代配置；更旧的长期绑定由精确 ReleaseReader 读取。
	for _, item := range previous.policies {
		entry.frozen[releaseIdentity{item.Release.Scope, item.Release.ID, item.Release.Version, item.Compiled.Summary.Digest}] = item
	}
	for _, item := range policies {
		entry.frozen[releaseIdentity{item.Release.Scope, item.Release.ID, item.Release.Version, item.Compiled.Summary.Digest}] = item
		entry.latest[policyIdentity{item.Release.Scope, item.Release.ID}] = item
	}
	now := c.now()
	entry.expires = now.Add(c.ttl)
	entry.refreshAt = now.Add(c.refreshInterval)
	entry.used = now
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := call.Err(); err != nil {
		return catalogEntry{}, err
	}
	if refresh {
		entry.used = c.entries[tenant].used
	}
	if c.dirty[tenant] {
		entry.expires = time.Time{}
		entry.refreshAt = time.Time{}
	}
	delete(c.entries, tenant)
	total := 0
	for key, entry := range c.entries {
		if !now.Before(entry.expires) {
			delete(c.entries, key)
		} else {
			total += entry.bytes
		}
	}
	for len(c.entries) >= 32 || total+size > 32<<20 {
		oldest := ""
		var used time.Time
		for key, entry := range c.entries {
			if oldest == "" || entry.used.Before(used) {
				oldest = key
				used = entry.used
			}
		}
		if oldest == "" {
			return catalogEntry{}, ErrUnavailable
		}
		total -= c.entries[oldest].bytes
		delete(c.entries, oldest)
	}
	c.entries[tenant] = entry
	return entry, nil
}

func (c *Catalog) cached(tenant string) (catalogEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[tenant]
	now := c.now()
	if !ok || !now.Before(entry.expires) {
		return catalogEntry{}, false
	}
	entry.used = now
	c.entries[tenant] = entry
	return entry, true
}

func (c *Catalog) read(ctx context.Context, tenant string) ([]FrozenPolicy, int, error) {
	c.mu.Lock()
	known := c.entries[tenant].frozen
	c.mu.Unlock()
	releases := []Release{}
	readBytes := 0
	if bulk, ok := c.reader.(PublishedReader); ok {
		var err error
		releases, err = bulk.PublishedPolicies(ctx, tenant)
		if err != nil {
			return nil, 0, err
		}
	} else {
		for _, kind := range []Kind{Suppression, Shield, Merge} {
			after := ""
			scope := Scope{TenantID: tenant, Kind: kind}
			for {
				// 本地存储分页不经过 Worker HTTP；只有完整读取才能替换目录。
				records, err := c.reader.List(ctx, scope, after, MaxPageSize)
				if err != nil {
					return nil, 0, err
				}
				if len(records) > MaxPageSize {
					return nil, 0, ErrUnavailable
				}
				for _, record := range records {
					if record.Scope != scope {
						return nil, 0, ErrAccess
					}
					if record.ID <= after {
						return nil, 0, fmt.Errorf("policy catalog order mismatch")
					}
					after = record.ID
					if record.Published == 0 || record.Deleted {
						continue
					}
					if record.Published > record.Revision {
						return nil, 0, ErrUnavailable
					}
					readBytes += len(record.Spec)
					if readBytes > 16<<20 {
						return nil, 0, fmt.Errorf("policy catalog configuration budget exceeded")
					}
					releases = append(releases, Release{Scope: scope, ID: record.ID, Version: record.Published, Spec: record.Spec, Compiled: record.Compiled})
				}
				if len(records) < MaxPageSize {
					break
				}
			}
		}
	}
	result := make([]FrozenPolicy, 0, len(releases))
	seen := make(map[policyIdentity]bool, len(releases))
	size := 0
	for _, release := range releases {
		if err := ctx.Err(); err != nil {
			return nil, 0, err
		}
		if release.TenantID != tenant {
			return nil, 0, ErrAccess
		}
		if !release.Kind.Valid() || release.ID == "" || release.Version < 1 {
			return nil, 0, ErrInvalid
		}
		if release.Deleted {
			continue
		}
		identity := policyIdentity{release.Scope, release.ID}
		if seen[identity] {
			return nil, 0, fmt.Errorf("%w: duplicate published policy", ErrInvalid)
		}
		seen[identity] = true
		size += len(release.Spec)
		if size > 16<<20 {
			return nil, 0, fmt.Errorf("policy catalog configuration budget exceeded")
		}
		// 未修改的发布配置复用不可变编译结果，不在每轮后台刷新重复编译。
		var compiled *Compiled
		if frozen, ok := known[releaseIdentity{release.Scope, release.ID, release.Version, release.Compiled.Digest}]; ok && frozen.Compiled.Summary.CompilerVersion == release.Compiled.CompilerVersion && bytes.Equal(frozen.Release.Spec, release.Spec) {
			compiled = frozen.Compiled
		} else {
			var err error
			compiled, err = Compile(release.Kind, release.Spec)
			if err != nil {
				return nil, 0, fmt.Errorf("%w: %w", ErrInvalid, err)
			}
		}
		if release.Compiled.Digest != compiled.Summary.Digest || release.Compiled.CompilerVersion != compiled.Summary.CompilerVersion {
			return nil, 0, fmt.Errorf("%w: published policy compile identity mismatch", ErrInvalid)
		}
		release.Spec = compiled.Canonical
		result = append(result, FrozenPolicy{Release: release, Compiled: compiled})
	}
	slices.SortFunc(result, func(a, b FrozenPolicy) int {
		if order := b.Compiled.Common.UpdatedAt.Compare(a.Compiled.Common.UpdatedAt); order != 0 {
			return order
		}
		if order := strings.Compare(a.Release.ID, b.Release.ID); order != 0 {
			return order
		}
		return strings.Compare(string(a.Release.Kind), string(b.Release.Kind))
	})
	return result, size, nil
}

// FindFrozen 仅复用缓存中身份完全相同的不可变配置，不查询或替代已冻结引用。
// 即使 TTL 到期，相同版本内容也不变；TTL 只限制 Freeze 的最新配置列表。
func (c *Catalog) FindFrozen(scope Scope, id string, version int64, digest string) (FrozenPolicy, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.entries[scope.TenantID].frozen[releaseIdentity{scope, id, version, digest}]
	return item, ok
}

// FindLatest 只查缓存的发布版本；未命中可能是目录被淘汰，判断停用应使用 Latest。
func (c *Catalog) FindLatest(scope Scope, id string) (FrozenPolicy, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.entries[scope.TenantID].latest[policyIdentity{scope, id}]
	return item, ok
}

// Latest 从完整目录确认发布状态；淘汰或过期时重新加载，缓存未命中不表示策略停用。
func (c *Catalog) Latest(ctx context.Context, scope Scope, id string) (FrozenPolicy, bool, error) {
	entry, err := c.loadEntry(ctx, scope.TenantID, false)
	if err != nil {
		return FrozenPolicy{}, false, err
	}
	item, found := entry.latest[policyIdentity{scope, id}]
	return item, found, nil
}

// Run 在已访问租户的快照到期前后台刷新；失败不延长旧快照的有效期。
// 调用者在关闭配置客户端前取消并等待 Run 返回。
func (c *Catalog) Run(ctx context.Context) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c.mu.Lock()
			now := c.now()
			tenants := []string{}
			for tenant, entry := range c.entries {
				if !now.Before(entry.refreshAt) && now.Sub(entry.used) < time.Minute {
					tenants = append(tenants, tenant)
				}
			}
			c.mu.Unlock()
			for _, tenant := range tenants {
				if _, err := c.loadEntry(ctx, tenant, true); err != nil && ctx.Err() != nil {
					return ctx.Err()
				}
			}
		}
	}
}
