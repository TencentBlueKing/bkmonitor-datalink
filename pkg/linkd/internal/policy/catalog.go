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

// FrozenPolicy 在一次 Event 裁决内固定配置版本和编译结果，消费者只能读取，不得修改。
type FrozenPolicy struct {
	Release  Release
	Compiled *Compiled
}

type catalogEntry struct {
	policies []FrozenPolicy
	expires  time.Time
	used     time.Time
	bytes    int
}

// Catalog 缓存已发布配置最多 5 秒；跨租户最多 32 项/32 MiB 原配置，每租户最多 256 个策略。
// 失败不复用已经过期的快照；仅返回完整一次读取，不能把半页当作策略停用。
type Catalog struct {
	reader  RecordReader
	mu      sync.Mutex
	entries map[string]catalogEntry
	loading chan struct{}
	now     func() time.Time
}

// NewCatalog 装配受租户约束的 Reader；后台调度与运行时加载分离。
func NewCatalog(reader RecordReader) *Catalog {
	return &Catalog{reader: reader, entries: map[string]catalogEntry{}, loading: make(chan struct{}, 1), now: time.Now}
}

// Freeze 返回当前 Event 使用的只读快照；slice 是副本，Compiled/Release 为不可变共享内容。
func (c *Catalog) Freeze(ctx context.Context, tenant string) ([]FrozenPolicy, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := (Scope{TenantID: tenant, Kind: Suppression}).Validate(); err != nil {
		return nil, err
	}
	if c.reader == nil {
		return nil, ErrUnavailable
	}
	if policies, ok := c.cached(tenant); ok {
		return policies, nil
	}
	select {
	case c.loading <- struct{}{}:
		defer func() { <-c.loading }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if policies, ok := c.cached(tenant); ok {
		return policies, nil
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	policies, size, err := c.read(call, tenant)
	if err != nil {
		return nil, err
	}
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
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
			return nil, ErrUnavailable
		}
		total -= c.entries[oldest].bytes
		delete(c.entries, oldest)
	}
	c.entries[tenant] = catalogEntry{policies: policies, expires: now.Add(5 * time.Second), used: now, bytes: size}
	return slices.Clone(policies), nil
}

func (c *Catalog) cached(tenant string) ([]FrozenPolicy, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[tenant]
	now := c.now()
	if !ok || !now.Before(entry.expires) {
		return nil, false
	}
	entry.used = now
	c.entries[tenant] = entry
	return slices.Clone(entry.policies), true
}

func (c *Catalog) read(ctx context.Context, tenant string) ([]FrozenPolicy, int, error) {
	result := []FrozenPolicy{}
	size, scanned := 0, 0
	for _, kind := range []Kind{Suppression, Shield, Merge} {
		after := ""
		scope := Scope{TenantID: tenant, Kind: kind}
		for {
			// Worker 控制面客户端响应上限 8 MiB，三份 2 MiB published Spec 留有足够信封余量。
			records, err := c.reader.List(ctx, scope, after, 3)
			if err != nil {
				return nil, 0, err
			}
			if len(records) > 3 {
				return nil, 0, ErrUnavailable
			}
			for _, record := range records {
				scanned++
				if scanned > 1024 {
					return nil, 0, fmt.Errorf("policy catalog record budget exceeded")
				}
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
				size += len(record.Spec)
				if size > 16<<20 || len(result) >= 256 {
					return nil, 0, fmt.Errorf("policy catalog configuration budget exceeded")
				}
				compiled, err := Compile(kind, record.Spec)
				if err != nil {
					return nil, 0, fmt.Errorf("%w: %w", ErrInvalid, err)
				}
				if record.Compiled.Digest != compiled.Summary.Digest || record.Compiled.CompilerVersion != compiled.Summary.CompilerVersion {
					return nil, 0, fmt.Errorf("%w: published policy compile identity mismatch", ErrInvalid)
				}
				release := Release{Scope: scope, ID: record.ID, Version: record.Published, Spec: compiled.Canonical, Compiled: compiled.Summary}
				result = append(result, FrozenPolicy{Release: release, Compiled: compiled})
			}
			if len(records) < 3 {
				break
			}
		}
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
	for _, item := range c.entries[scope.TenantID].policies {
		if item.Release.Scope == scope && item.Release.ID == id && item.Release.Version == version && item.Compiled.Summary.Digest == digest {
			return item, true
		}
	}
	return FrozenPolicy{}, false
}
