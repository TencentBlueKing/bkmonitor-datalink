// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package eventsource 管理来源配置及不可变发布；执行所有权由调度器负责。
package eventsource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"linkd/internal/config"
)

var (
	// ErrNotFound 表示指定的来源或发布不存在。
	ErrNotFound = errors.New("event source not found")
	// ErrConflict 表示编辑版本不符或另一个发布尚未完成。
	ErrConflict = errors.New("event source revision conflict")
)

// Documents 只提供两个集合的单对象 CAS；空 expected 表示仅创建。
type Documents interface {
	Get(context.Context, string, string) (json.RawMessage, string, error)
	Put(context.Context, string, string, string, json.RawMessage) error
	List(context.Context, string, string, int) ([]json.RawMessage, error)
}

// Record 保存编辑版本、已发布指针和可恢复的待发布快照。
type Record struct {
	ID        string             `json:"id"`
	Revision  int64              `json:"revision"`
	Published int64              `json:"published"`
	Spec      config.EventSource `json:"spec"`
	Pending   *Release           `json:"pending,omitempty"`
	Deleted   bool               `json:"deleted"`
}

// Release 的配置和发布版本一经创建即不可变。
type Release struct {
	ID        string             `json:"id"`
	Version   int64              `json:"version"`
	Spec      config.EventSource `json:"spec"`
	Deleted   bool               `json:"deleted"`
	Actor     string             `json:"actor"`
	CreatedAt time.Time          `json:"created_at"`
}

// Service 将 API、provider 和显式导入统一成来源发布用例。
type Service struct {
	docs     Documents
	severity config.SeverityConfig
	cleaner  config.CleanerRuntimeConfig
}

// New 创建来源服务，不隐式导入配置。
func New(d Documents, severity config.SeverityConfig, defaults ...config.CleanerRuntimeConfig) *Service {
	cleaner := config.DefaultCleanerRuntimeConfig()
	if len(defaults) > 0 {
		cleaner = defaults[0].WithDefaults()
	}
	return &Service{docs: d, severity: severity, cleaner: cleaner}
}

// Get 读取当前编辑记录。
func (s *Service) Get(ctx context.Context, id string) (Record, error) {
	r, _, err := s.get(ctx, id)
	return r, err
}

func (s *Service) get(ctx context.Context, id string) (Record, string, error) {
	var r Record
	b, t, e := s.docs.Get(ctx, "records", id)
	if e != nil {
		return r, t, e
	}
	e = json.Unmarshal(b, &r)
	return r, t, e
}

// List 按来源 ID 游标分页，包括 tombstone 以便控制面恢复。
func (s *Service) List(ctx context.Context, after string, limit int) ([]Record, error) {
	bs, e := s.docs.List(ctx, "records", after, limit)
	if e != nil {
		return nil, e
	}
	rs := make([]Record, 0, len(bs))
	for _, b := range bs {
		var r Record
		if e = json.Unmarshal(b, &r); e != nil {
			return nil, e
		}
		rs = append(rs, r)
	}
	return rs, nil
}

// GetRelease 按来源及版本读取快照，不根据搜索结果猜测最新发布。
func (s *Service) GetRelease(ctx context.Context, id string, v int64) (Release, error) {
	var r Release
	b, _, e := s.docs.Get(ctx, "releases", releaseKey(id, v))
	if e != nil {
		return r, e
	}
	e = json.Unmarshal(b, &r)
	r.Spec.Version = r.Version
	return r, e
}

func releaseKey(id string, v int64) string { return id + ":" + strconv.FormatInt(v, 10) }

// Apply 预留版本后完成发布；发生部分成功时后续 Recover 可继续，不能撤销已写事实。
func (s *Service) Apply(ctx context.Context, spec config.EventSource, expected int64, deleted bool, actor string) (Record, error) {
	spec = spec.WithDefaults()
	if err := spec.Cleaner.RuntimeConfig(s.cleaner).Validate(); err != nil {
		return Record{}, err
	}
	if e := config.ValidateEventSources([]config.EventSource{spec}, s.severity); e != nil {
		return Record{}, e
	}
	r, t, e := s.get(ctx, spec.EventSourceID)
	if errors.Is(e, ErrNotFound) {
		r = Record{ID: spec.EventSourceID}
		t = ""
	} else if e != nil {
		return r, e
	}
	if (expected+1 == r.Revision || expected == r.Revision) && r.Revision > 0 && r.Deleted == deleted {
		a, b := r.Spec.WithDefaults(), spec.WithDefaults()
		a.Version = 0
		b.Version = 0
		if reflect.DeepEqual(a, b) {
			return s.Recover(ctx, r.ID)
		}
	}
	if r.Revision != expected || r.Pending != nil {
		return r, ErrConflict
	}
	if r.Revision > 0 {
		// 身份/订阅/关联键迁移不是普通配置更新，避免重投生成另一业务身份。
		a, b := r.Spec, spec
		if a.RelatedTenantID != b.RelatedTenantID || a.Cleaner.Type != b.Cleaner.Type || a.FingerprintMode != b.FingerprintMode || a.FingerprintField != b.FingerprintField || !reflect.DeepEqual(a.FingerprintFields, b.FingerprintFields) || a.Storage.Type != b.Storage.Type || !reflect.DeepEqual(a.Storage.Kafka.Brokers, b.Storage.Kafka.Brokers) || a.Storage.Kafka.Topic != b.Storage.Kafka.Topic || a.Storage.Kafka.ConsumerGroup != b.Storage.Kafka.ConsumerGroup {
			return r, fmt.Errorf("source identity, fingerprint and subscription migration is not supported")
		}
	}
	if deleted {
		spec.Enabled = false
	}
	r.Revision++
	r.Spec = spec
	r.Deleted = deleted
	r.Pending = &Release{ID: r.ID, Version: r.Revision, Spec: spec, Deleted: deleted, Actor: actor, CreatedAt: time.Now().UTC()}
	b, e := json.Marshal(r)
	if e != nil {
		return r, e
	}
	if e = s.docs.Put(ctx, "records", r.ID, t, b); e != nil {
		return r, e
	}
	return s.Recover(ctx, r.ID)
}

// Recover 幂等补齐 Release 后 CAS 发布指针，不覆盖后来的编辑。
func (s *Service) Recover(ctx context.Context, id string) (Record, error) {
	r, t, e := s.get(ctx, id)
	if e != nil || r.Pending == nil {
		return r, e
	}
	release := *r.Pending
	b, e := json.Marshal(release)
	if e != nil {
		return r, e
	}
	e = s.docs.Put(ctx, "releases", releaseKey(id, release.Version), "", b)
	if errors.Is(e, ErrConflict) {
		existing, _, getErr := s.docs.Get(ctx, "releases", releaseKey(id, release.Version))
		if getErr != nil {
			return r, getErr
		}
		if !reflect.DeepEqual(json.RawMessage(existing), json.RawMessage(b)) {
			var x Release
			if json.Unmarshal(existing, &x) != nil || !reflect.DeepEqual(x, release) {
				return r, ErrConflict
			}
		}
	} else if e != nil {
		return r, e
	}
	r.Published = release.Version
	r.Pending = nil
	b, e = json.Marshal(r)
	if e != nil {
		return r, e
	}
	e = s.docs.Put(ctx, "records", id, t, b)
	// API 与后台恢复可能完成同一份 pending；发布版本单调推进，后续版本只能在它完成后预留。
	if errors.Is(e, ErrConflict) {
		current, _, readErr := s.get(ctx, id)
		if readErr == nil && current.Published >= release.Version {
			return r, nil
		}
	}
	return r, e
}

// Redacted 返回可在管理接口展示的深拷贝。
func (r Record) Redacted() Record {
	r.Spec = r.Spec.Redacted()
	if r.Pending != nil {
		p := *r.Pending
		p.Spec = p.Spec.Redacted()
		r.Pending = &p
	}
	return r
}

// Change 是 provider 显式提出的增量操作，缺失条目不代表删除。
type Change struct {
	Spec     config.EventSource
	Expected int64
	Delete   bool
}

// Reader 为 provider 提供当前版本，只暴露读取能力。
type Reader interface {
	Get(context.Context, string) (Record, error)
	List(context.Context, string, int) ([]Record, error)
}

// Provider 由装配显式注入；返回可重放增量，不持有配置所有权。
type Provider interface {
	Pull(context.Context, Reader) ([]Change, error)
}

// RunProvider 仅在本轮全部成功后开始下一轮，限制单轮操作数并传播取消。
func (s *Service) RunProvider(ctx context.Context, p Provider, interval time.Duration, onError func(error)) error {
	if interval <= 0 {
		return fmt.Errorf("provider interval must be positive")
	}
	for {
		call, cancel := context.WithTimeout(ctx, 30*time.Second)
		changes, e := p.Pull(call, s)
		if e == nil && len(changes) > 1000 {
			e = fmt.Errorf("provider batch exceeds 1000 sources")
		}
		if e == nil {
			for _, c := range changes {
				_, e = s.Apply(call, c.Spec, c.Expected, c.Delete, "provider")
				if e != nil {
					break
				}
			}
		}
		cancel()
		if e != nil && onError != nil {
			onError(e)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
