// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"linkd/internal/onemodel"
	"linkd/internal/policy"
)

// DependencyRequest 标识可由多个策略共享的读取；身份包含租户，不包含策略 ID。
type DependencyRequest struct {
	Kind      string                     `json:"kind"`
	TenantID  string                     `json:"bk_tenant_id"`
	SpaceCode string                     `json:"space_code,omitempty"`
	Target    *onemodel.TargetDescriptor `json:"target,omitempty"`
	Origin    onemodel.InstanceRef       `json:"origin,omitempty"`
	Relation  string                     `json:"relation,omitempty"`
	ModelID   string                     `json:"model_id,omitempty"`
}

// DependencySnapshot 区分源健康读取与内容变更时间，跨控制面/Worker 原样传递。
type DependencySnapshot struct {
	LoadedAt  time.Time              `json:"loaded_at"`
	ChangedAt time.Time              `json:"changed_at"`
	Scope     onemodel.TargetScope   `json:"scope"`
	Targets   onemodel.TargetResult  `json:"targets"`
	Related   []onemodel.InstanceRef `json:"related,omitempty"`
}

type dependencyReadTimeKey struct{}

// DependencyReadTime 记录本次规则判断使用的源内容版本时间，供资格更新排序。
type DependencyReadTime struct{ at time.Time }

// Time 返回最后一次实际用于本规则判断的源内容变更时间，未读外部依赖时为零。
func (t *DependencyReadTime) Time() time.Time { return t.at }

// TrackDependencyReads 的跟踪范围是一条规则；目标展开自身包含业务范围，后续读取取代先前范围读取。
func TrackDependencyReads(ctx context.Context) (context.Context, *DependencyReadTime) {
	t := &DependencyReadTime{}
	return context.WithValue(ctx, dependencyReadTimeKey{}, t), t
}

func recordDependencyRead(ctx context.Context, at time.Time) {
	if t, ok := ctx.Value(dependencyReadTimeKey{}).(*DependencyReadTime); ok {
		t.at = at
	}
}

type dependencyEntry struct {
	request   DependencyRequest
	value     DependencySnapshot
	instances map[onemodel.InstanceRef]struct{}
	loading   chan struct{}
	next      time.Time
	used      time.Time
	err       error
}

// Dependencies 在进程内复用目标/业务/关系读取，后台刷新不占 Event 的查询路径。
// 首次读取会等待同键的一次装载；成功结果最多使用一分钟，失败不变成空集合。
type Dependencies struct {
	mu         sync.Mutex
	entries    map[string]*dependencyEntry
	load       func(context.Context, DependencyRequest) (DependencySnapshot, error)
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	now        func() time.Time
	slots      chan struct{}
	targetKeys map[targetDependencyKey]targetDependencyIdentity
	changed    map[string]struct{}
}

type targetDependencyKey struct {
	tenant, space string
	target        *onemodel.TargetDescriptor
}

type targetDependencyIdentity struct {
	key  string
	used time.Time
}

const dependencyMaxAge = time.Minute

const dependencyRefreshAge = 30 * time.Second

// NewDependencies 启动共享刷新器；Close 必须在底层连接关闭前调用。
func NewDependencies(ctx context.Context, load func(context.Context, DependencyRequest) (DependencySnapshot, error)) *Dependencies {
	ctx, cancel := context.WithCancel(ctx)
	d := &Dependencies{entries: map[string]*dependencyEntry{}, targetKeys: map[targetDependencyKey]targetDependencyIdentity{}, changed: map[string]struct{}{}, slots: make(chan struct{}, 4), load: load, ctx: ctx, cancel: cancel, now: time.Now}
	d.wg.Add(1)
	go d.run()
	return d
}

// Close 取消未完成的源读取并等待后台刷新停止。
func (d *Dependencies) Close() {
	d.mu.Lock()
	d.cancel()
	d.mu.Unlock()
	d.wg.Wait()
}

func dependencyKey(r DependencyRequest) (string, error) {
	b, err := json.Marshal(r)
	return string(b), err
}

func (d *Dependencies) entry(ctx context.Context, r DependencyRequest) (*dependencyEntry, error) {
	key, err := dependencyKey(r)
	if err != nil {
		return nil, err
	}
	return d.entryKey(ctx, r, key)
}

func (d *Dependencies) entryKey(ctx context.Context, r DependencyRequest, key string) (*dependencyEntry, error) {
	d.mu.Lock()
	if err := d.ctx.Err(); err != nil {
		d.mu.Unlock()
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		d.mu.Unlock()
		return nil, err
	}
	var err error
	now := d.now()
	e := d.entries[key]
	if e == nil {
		e = &dependencyEntry{request: r, used: now, loading: make(chan struct{})}
		d.entries[key] = e
		d.wg.Add(1)
		go func() { defer d.wg.Done(); d.refresh(key, e) }()
	}
	e.used = now
	if !e.value.LoadedAt.IsZero() && now.Sub(e.value.LoadedAt) < dependencyMaxAge {
		// 刷新发布新的 entry，已返回的集合与时间戳在本次匹配期间保持不变。
		d.mu.Unlock()
		return e, nil
	}
	loading := e.loading
	err = e.err
	d.mu.Unlock()
	if loading == nil {
		return nil, errors.Join(policy.ErrUnavailable, err)
	}
	select {
	case <-loading:
		d.mu.Lock()
		e = d.entries[key]
		if e != nil && !e.value.LoadedAt.IsZero() && d.now().Sub(e.value.LoadedAt) < dependencyMaxAge {
			d.mu.Unlock()
			return e, nil
		}
		if e != nil {
			err = e.err
		}
		d.mu.Unlock()
		return nil, errors.Join(policy.ErrUnavailable, err)
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-d.ctx.Done():
		return nil, d.ctx.Err()
	}
}

func (d *Dependencies) refresh(key string, old *dependencyEntry) {
	call, cancel := context.WithTimeout(d.ctx, 10*time.Second)
	var value DependencySnapshot
	var err error
	select {
	case d.slots <- struct{}{}:
		value, err = d.load(call, old.request)
		<-d.slots
	case <-call.Done():
		err = call.Err()
	}
	cancel()
	if err == nil {
		err = validateDependencySnapshot(old.request, value)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	now := d.now()
	next := &dependencyEntry{request: old.request, value: old.value, instances: old.instances, used: old.used, err: err, next: now.Add(5 * time.Second)}
	if err == nil {
		if value.LoadedAt.IsZero() || now.Sub(value.LoadedAt) >= dependencyMaxAge {
			next.err = policy.ErrUnavailable
		} else {
			before, after := old.value, value
			before.LoadedAt = time.Time{}
			after.LoadedAt = time.Time{}
			before.ChangedAt = time.Time{}
			after.ChangedAt = time.Time{}
			changed := !reflect.DeepEqual(before, after)
			if value.ChangedAt.IsZero() {
				value.ChangedAt = old.value.ChangedAt
				if old.value.LoadedAt.IsZero() || changed || value.ChangedAt.IsZero() {
					value.ChangedAt = value.LoadedAt
				}
			}
			if old.request.Kind != "relations" && !old.value.LoadedAt.IsZero() && changed {
				d.changed[old.request.TenantID] = struct{}{}
			}
			next.value = value
			next.next = value.LoadedAt.Add(dependencyRefreshAge)
			if next.next.Before(now.Add(5 * time.Second)) {
				next.next = now.Add(5 * time.Second)
			}
			next.instances = make(map[onemodel.InstanceRef]struct{}, len(value.Targets.Instances))
			for _, ref := range value.Targets.Instances {
				next.instances[ref] = struct{}{}
			}
		}
	}
	d.entries[key] = next
	close(old.loading)
}

// PrepareTargets 固定本次求值的完整目标快照，业务范围不再读取另一种依赖 key。
func (d *Dependencies) PrepareTargets(ctx context.Context, tenant, space string, target *onemodel.TargetDescriptor) (policy.TargetReader, error) {
	e, err := d.targetEntry(ctx, tenant, space, target)
	if err != nil {
		return nil, err
	}
	return preparedTargetView{entry: e}, nil
}

type preparedTargetView struct{ entry *dependencyEntry }

func (p preparedTargetView) ResolvePolicyScope(ctx context.Context, _, _ string) (onemodel.TargetScope, error) {
	if err := ctx.Err(); err != nil {
		return onemodel.TargetScope{}, err
	}
	recordDependencyRead(ctx, p.entry.value.ChangedAt)
	return p.entry.value.Targets.Scope, nil
}

func (p preparedTargetView) ContainsTarget(ctx context.Context, _, _ string, _ *onemodel.TargetDescriptor, ref onemodel.InstanceRef) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	recordDependencyRead(ctx, p.entry.value.ChangedAt)
	_, found := p.entry.instances[ref]
	return found, nil
}

func (p preparedTargetView) Resolve(ctx context.Context, _, _ string, _ onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	if err := ctx.Err(); err != nil {
		return onemodel.TargetResult{}, err
	}
	recordDependencyRead(ctx, p.entry.value.ChangedAt)
	return p.entry.value.Targets, nil
}

// DrainChangedTenants 合并实际依赖内容变化，用于重算依赖主资格；刷新时间变化不会触发重算。
func (d *Dependencies) DrainChangedTenants() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	result := make([]string, 0, len(d.changed))
	for tenant := range d.changed {
		result = append(result, tenant)
		delete(d.changed, tenant)
	}
	return result
}

func (d *Dependencies) run() {
	defer d.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-d.ctx.Done():
			return
		case <-ticker.C:
		}
		// 最多四个并发源读取；在待刷项之间推进，避免慢依赖串行阻塞全部快照。
		type pending struct {
			key   string
			entry *dependencyEntry
		}
		var work []pending
		d.mu.Lock()
		now := d.now()
		for key, e := range d.entries {
			if e.loading != nil {
				continue
			}
			if now.Sub(e.used) > 5*time.Minute {
				delete(d.entries, key)
				continue
			}
			if !now.Before(e.next) {
				e.loading = make(chan struct{})
				work = append(work, pending{key, e})
			}
		}
		for key, id := range d.targetKeys {
			if now.Sub(id.used) > 5*time.Minute {
				delete(d.targetKeys, key)
			}
		}
		d.mu.Unlock()
		jobs := make(chan pending, len(work))
		for _, item := range work {
			jobs <- item
		}
		close(jobs)
		var group sync.WaitGroup
		for range min(4, len(work)) {
			group.Go(func() {
				for item := range jobs {
					d.refresh(item.key, item.entry)
				}
			})
		}
		group.Wait()
	}
}

// Read 供控制面分发实际源时间戳；返回内容是不可变的共享快照。
func (d *Dependencies) Read(ctx context.Context, r DependencyRequest) (DependencySnapshot, error) {
	e, err := d.entry(ctx, r)
	if err != nil {
		return DependencySnapshot{}, err
	}
	return e.value, nil
}

// ResolvePolicyScope 复用真实空间身份，全局标记与目标快照一起传递。
func (d *Dependencies) ResolvePolicyScope(ctx context.Context, tenant, space string) (onemodel.TargetScope, error) {
	e, err := d.entry(ctx, DependencyRequest{Kind: "scope", TenantID: tenant, SpaceCode: space})
	if err != nil {
		return onemodel.TargetScope{}, err
	}
	recordDependencyRead(ctx, e.value.ChangedAt)
	return e.value.Scope, nil
}

// Resolve 复用同一租户、空间和目标描述的完整展开结果。
func (d *Dependencies) Resolve(ctx context.Context, tenant, space string, target onemodel.TargetDescriptor) (onemodel.TargetResult, error) {
	e, err := d.entry(ctx, DependencyRequest{Kind: "targets", TenantID: tenant, SpaceCode: space, Target: &target})
	if err != nil {
		return onemodel.TargetResult{}, err
	}
	recordDependencyRead(ctx, e.value.ChangedAt)
	return e.value.Targets, nil
}

// ContainsTarget 使用预先构建的集合，避免对每个 Event 遍历全部目标成员。
func (d *Dependencies) ContainsTarget(ctx context.Context, tenant, space string, target *onemodel.TargetDescriptor, instance onemodel.InstanceRef) (bool, error) {
	e, err := d.targetEntry(ctx, tenant, space, target)
	if err != nil {
		return false, err
	}
	recordDependencyRead(ctx, e.value.ChangedAt)
	_, found := e.instances[instance]
	return found, nil
}

func (d *Dependencies) targetEntry(ctx context.Context, tenant, space string, target *onemodel.TargetDescriptor) (*dependencyEntry, error) {
	request := DependencyRequest{Kind: "targets", TenantID: tenant, SpaceCode: space, Target: target}
	identity := targetDependencyKey{tenant, space, target}
	d.mu.Lock()
	id, ok := d.targetKeys[identity]
	if !ok {
		key, err := dependencyKey(request)
		if err != nil {
			d.mu.Unlock()
			return nil, err
		}
		id.key = key
	}
	id.used = d.now()
	d.targetKeys[identity] = id
	d.mu.Unlock()
	return d.entryKey(ctx, request, id.key)
}

// Lookup 共享同一起点与关系的读取；主告警本身的实时状态不在此缓存。
func (d *Dependencies) Lookup(ctx context.Context, tenant string, origin onemodel.InstanceRef, relation, target string) ([]onemodel.InstanceRef, error) {
	e, err := d.entry(ctx, DependencyRequest{Kind: "relations", TenantID: tenant, Origin: origin, Relation: relation, ModelID: target})
	if err != nil {
		return nil, err
	}
	recordDependencyRead(ctx, e.value.ChangedAt)
	return e.value.Related, nil
}

func sourceDependencyLoader(targets *onemodel.TargetResolver, relations policy.RelationLookup) func(context.Context, DependencyRequest) (DependencySnapshot, error) {
	return func(ctx context.Context, r DependencyRequest) (DependencySnapshot, error) {
		value := DependencySnapshot{LoadedAt: time.Now()}
		var err error
		switch r.Kind {
		case "scope":
			value.Scope, err = targets.ResolvePolicyScope(ctx, r.TenantID, r.SpaceCode)
		case "targets":
			if r.Target == nil {
				return value, policy.ErrInvalid
			}
			value.Targets, err = targets.Resolve(ctx, r.TenantID, r.SpaceCode, *r.Target)
		case "relations":
			if relations == nil {
				return value, policy.ErrUnavailable
			}
			value.Related, err = relations.Lookup(ctx, r.TenantID, r.Origin, r.Relation, r.ModelID)
		default:
			err = fmt.Errorf("%w: unknown dependency kind", policy.ErrInvalid)
		}
		return value, err
	}
}
