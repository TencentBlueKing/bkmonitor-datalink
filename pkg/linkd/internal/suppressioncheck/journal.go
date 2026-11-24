// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package suppressioncheck

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/suppressioncleanup"
)

// Journal 保存本部署的显式复核命令；调用者在租户准入锁内 Enqueue、在同请求租约内 Start/Finish。
type Journal struct{ docs Documents }

// NewJournal 仅绑定依赖，不隐式执行清理。
func NewJournal(d Documents) (*Journal, error) {
	if d == nil {
		return nil, policy.ErrInvalid
	}
	return &Journal{d}, nil
}

func validWindow(tenant, kind, id string) error {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || (suppressioncleanup.Window{ID: id, Epoch: "validate"}).Validate(kind) != nil {
		return policy.ErrInvalid
	}
	return nil
}

func decode(raw json.RawMessage) (Request, error) {
	var r Request
	if len(raw) > 64<<10 || json.Unmarshal(raw, &r) != nil || r.Validate() != nil {
		return r, policy.ErrInvalid
	}
	return r, nil
}

func (j *Journal) save(ctx context.Context, r Request, version string) error {
	if r.Validate() != nil {
		return policy.ErrInvalid
	}
	b, e := json.Marshal(r)
	if e != nil || len(b) > 64<<10 {
		return policy.ErrInvalid
	}
	return j.docs.Put(ctx, "suppression_requests", recordKey(r), version, b)
}

// Find 先识别完整重复命令，原操作不得更换 owner、代次、操作者或原因。
func (j *Journal) Find(ctx context.Context, c Command) (Stored, error) {
	if c.Validate() != nil {
		return Stored{}, policy.ErrInvalid
	}
	r, e := j.Get(ctx, c.TenantID, c.Kind, c.WindowID, c.id())
	if e != nil {
		return r, e
	}
	if !reflect.DeepEqual(c, r.Request.Command) {
		return Stored{}, policy.ErrConflict
	}
	return r, nil
}

// Enqueue 在租户锁内限制最多 1024 pending；已完成的相同请求仍可复用。
func (j *Journal) Enqueue(ctx context.Context, c Command, at time.Time) (Stored, error) {
	if c.Validate() != nil || at.IsZero() {
		return Stored{}, policy.ErrInvalid
	}
	if r, e := j.Find(ctx, c); e == nil {
		return r, nil
	} else if !errors.Is(e, policy.ErrNotFound) {
		return r, e
	}
	n, e := j.docs.CountSuppressionCheckRequests(ctx, tenantPrefix(c.TenantID), 1024)
	if e != nil {
		return Stored{}, e
	}
	if n < 0 || n > 1024 {
		return Stored{}, policy.ErrInvalid
	}
	if n == 1024 {
		return Stored{}, policy.ErrPreviewCapacity
	}
	r := Request{ID: c.id(), Command: c, State: "pending", CreatedAt: at.Round(0).UTC()}
	if e := j.save(ctx, r, ""); e != nil && !errors.Is(e, policy.ErrConflict) {
		return Stored{}, e
	}
	return j.Find(ctx, c)
}

// Get 精确读取原窗口的请求，窗口已消失也可查最终结果。
func (j *Journal) Get(ctx context.Context, tenant, kind, id, requestID string) (Stored, error) {
	if validWindow(tenant, kind, id) != nil || !hashID(requestID) {
		return Stored{}, policy.ErrInvalid
	}
	raw, version, e := j.docs.Get(ctx, "suppression_requests", windowPrefix(tenant, kind, id)+requestID)
	if e != nil {
		return Stored{}, e
	}
	r, e := decode(raw)
	if e != nil || version == "" {
		return Stored{}, policy.ErrInvalid
	}
	if r.Command.TenantID != tenant || r.Command.Kind != kind || r.Command.WindowID != id || r.ID != requestID {
		return Stored{}, policy.ErrAccess
	}
	return Stored{r, version}, nil
}

// Start 在执行前持久标记；中断后的重试保留此前结果未确认，不把再次空读解释为首次未执行。
func (j *Journal) Start(ctx context.Context, s Stored, at time.Time) (Stored, error) {
	r := s.Request
	if r.Validate() != nil || s.Version == "" || r.State != "pending" || at.IsZero() {
		return Stored{}, policy.ErrInvalid
	}
	if r.StartedAt == nil {
		v := at.Round(0).UTC()
		if v.Before(r.CreatedAt) {
			v = r.CreatedAt
		}
		r.StartedAt = &v
	} else {
		r.PreviousUnconfirmed = true
	}
	if e := j.save(ctx, r, s.Version); e != nil {
		return Stored{}, e
	}
	saved, err := j.Get(ctx, r.Command.TenantID, r.Command.Kind, r.Command.WindowID, r.ID)
	if err != nil {
		return Stored{}, err
	}
	if !reflect.DeepEqual(saved.Request, r) {
		return Stored{}, policy.ErrConflict
	}
	return saved, nil
}

// Finish 不覆盖最终结果；核心存储失败保留 pending，后续仍检查原 owner/代次。
func (j *Journal) Finish(ctx context.Context, s Stored, result Check) (Stored, error) {
	r := s.Request
	if s.Version == "" || r.Validate() != nil || r.State != "pending" || r.StartedAt == nil {
		return Stored{}, policy.ErrInvalid
	}
	if result.Validate(r.Command) != nil {
		return Stored{}, policy.ErrInvalid
	}
	// JSON 不保留进程内单调时钟；在 CAS 后读回核对前固定 UTC 精度，避免成功写入被误报冲突。
	result.CheckedAt = result.CheckedAt.Round(0).UTC()
	if result.CheckedAt.Before(*r.StartedAt) {
		result.CheckedAt = *r.StartedAt
	}
	r.State = "completed"
	if result.Outcome == "failed" {
		r.State = "failed"
	}
	if result.Outcome == "superseded" {
		r.State = "superseded"
	}
	r.Result = &result
	r.CompletedAt = &result.CheckedAt
	if e := j.save(ctx, r, s.Version); e != nil {
		return Stored{}, e
	}
	saved, err := j.Get(ctx, r.Command.TenantID, r.Command.Kind, r.Command.WindowID, r.ID)
	if err != nil {
		return Stored{}, err
	}
	if !reflect.DeepEqual(saved.Request, r) {
		return Stored{}, policy.ErrConflict
	}
	return saved, nil
}

// List 只返回一个明确窗口的请求历史，不依赖该 Redis 窗口仍存在。
func (j *Journal) List(ctx context.Context, tenant, kind, id, after string, limit int) (Page, error) {
	if validWindow(tenant, kind, id) != nil {
		return Page{}, policy.ErrInvalid
	}
	p := windowPrefix(tenant, kind, id)
	if after != "" && !strings.HasPrefix(after, p) {
		return Page{}, policy.ErrInvalid
	}
	return j.page(ctx, p, after, limit, false)
}

// Work 只供控制面扫描 pending；执行前需精确读和取得同请求租约。
func (j *Journal) Work(ctx context.Context, after string, limit int) (Page, error) {
	return j.page(ctx, "", after, limit, true)
}

func (j *Journal) page(ctx context.Context, prefix, after string, limit int, work bool) (Page, error) {
	if len(after) > 256 || limit < 1 || limit > 16 {
		return Page{}, policy.ErrInvalid
	}
	var rows []json.RawMessage
	var e error
	if work {
		rows, e = j.docs.ListSuppressionCheckRequests(ctx, prefix, after, limit)
	} else {
		rows, e = j.docs.List(ctx, "suppression_requests", prefix, after, limit)
	}
	if e != nil {
		return Page{}, e
	}
	if len(rows) > limit {
		return Page{}, policy.ErrInvalid
	}
	page := Page{Items: []Request{}}
	last := after
	for _, raw := range rows {
		r, e := decode(raw)
		if e != nil {
			return Page{}, e
		}
		key := recordKey(r)
		if key <= last || !strings.HasPrefix(key, prefix) || (work && r.State != "pending") {
			return Page{}, policy.ErrInvalid
		}
		last = key
		page.Items = append(page.Items, r)
	}
	if len(rows) == limit {
		page.Next = last
	}
	return page, nil
}
