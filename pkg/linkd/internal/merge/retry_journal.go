// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"linkd/internal/policy"
)

// RetryDocuments 以原子 payload/work 写入和有界 pending 查询支撑显式操作队列。
type RetryDocuments interface {
	Documents
	CountMergeRequests(context.Context, string, int) (int, error)
	ListMergeRequests(context.Context, string, string, int) ([]json.RawMessage, error)
}

func decodeRetry(raw json.RawMessage) (RetryRequest, error) {
	var r RetryRequest
	if len(raw) > 64<<10 || json.Unmarshal(raw, &r) != nil || r.Validate() != nil {
		return r, policy.ErrInvalid
	}
	return r, nil
}

func (j *Journal) saveRetry(ctx context.Context, r RetryRequest, version string) error {
	if r.Validate() != nil {
		return policy.ErrInvalid
	}
	b, e := json.Marshal(r)
	if e != nil || len(b) > 64<<10 {
		return policy.ErrInvalid
	}
	return j.docs.Put(ctx, "merge_requests", r.Cursor(), version, b)
}

// FindRetry 先识别完整重复命令，原操作不得更换目标版本、操作者或原因。
func (j *Journal) FindRetry(ctx context.Context, c RetryCommand) (StoredRetry, error) {
	if c.Validate() != nil {
		return StoredRetry{}, policy.ErrInvalid
	}
	r, e := j.GetRetry(ctx, c.TenantID, c.Kind, c.TargetID, c.id())
	if e != nil {
		return r, e
	}
	if !reflect.DeepEqual(c, r.Request.Command) {
		return StoredRetry{}, policy.ErrConflict
	}
	return r, nil
}

// EnqueueRetry 必须在租户准入锁内调用，最多 1024 pending；重复命令不受满额影响。
func (j *Journal) EnqueueRetry(ctx context.Context, c RetryCommand, at time.Time) (StoredRetry, error) {
	if c.Validate() != nil || at.IsZero() {
		return StoredRetry{}, policy.ErrInvalid
	}
	if r, e := j.FindRetry(ctx, c); e == nil {
		return r, nil
	} else if !errors.Is(e, policy.ErrNotFound) {
		return r, e
	}
	point, e := j.ReadControlPoint(ctx, c.TenantID, c.Kind, c.TargetID)
	if e != nil {
		return StoredRetry{}, e
	}
	docs, ok := j.docs.(RetryDocuments)
	if !ok {
		return StoredRetry{}, policy.ErrUnavailable
	}
	n, e := docs.CountMergeRequests(ctx, prefix(c.TenantID), 1024)
	if e != nil {
		return StoredRetry{}, e
	}
	if n < 0 || n > 1024 {
		return StoredRetry{}, policy.ErrInvalid
	}
	if n == 1024 {
		return StoredRetry{}, policy.ErrPreviewCapacity
	}
	r := RetryRequest{ID: c.id(), Command: c, WindowID: point.WindowID, State: "pending", CreatedAt: at.Round(0).UTC()}
	if e := j.saveRetry(ctx, r, ""); e != nil && !errors.Is(e, policy.ErrConflict) {
		return StoredRetry{}, e
	}
	return j.FindRetry(ctx, c)
}

// GetRetry 精确读取原目标的持久请求，不依赖 Redis 窗口。
func (j *Journal) GetRetry(ctx context.Context, tenant, kind, id, requestID string) (StoredRetry, error) {
	if !retryScope(tenant, kind, id) || !retryHash(requestID) {
		return StoredRetry{}, policy.ErrInvalid
	}
	raw, version, e := j.docs.Get(ctx, "merge_requests", retryPrefix(tenant, kind, id)+requestID)
	if e != nil {
		return StoredRetry{}, e
	}
	r, e := decodeRetry(raw)
	if e != nil || version == "" {
		return StoredRetry{}, policy.ErrInvalid
	}
	if r.Command.TenantID != tenant || r.Command.Kind != kind || r.Command.TargetID != id || r.ID != requestID {
		return StoredRetry{}, policy.ErrAccess
	}
	return StoredRetry{r, version}, nil
}

// StartRetry 必须在同请求租约内先于执行保存；再次接续保留此前结果未确认。
func (j *Journal) StartRetry(ctx context.Context, s StoredRetry, at time.Time) (StoredRetry, error) {
	r := s.Request
	if r.Validate() != nil || s.Version == "" || r.State != "pending" || at.IsZero() {
		return StoredRetry{}, policy.ErrInvalid
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
	if e := j.saveRetry(ctx, r, s.Version); e != nil {
		return StoredRetry{}, e
	}
	saved, err := j.GetRetry(ctx, r.Command.TenantID, r.Command.Kind, r.Command.TargetID, r.ID)
	if err != nil {
		return StoredRetry{}, err
	}
	if !reflect.DeepEqual(saved.Request, r) {
		return StoredRetry{}, policy.ErrConflict
	}
	return saved, nil
}

// FinishRetry 不覆盖最终结果；核心存储失败保留 pending，后续仍检查原目标版本。
func (j *Journal) FinishRetry(ctx context.Context, s StoredRetry, result RetryResult) (StoredRetry, error) {
	r := s.Request
	if s.Version == "" || r.Validate() != nil || r.State != "pending" || r.StartedAt == nil {
		return StoredRetry{}, policy.ErrInvalid
	}
	if result.Validate(r.Command, r.WindowID) != nil {
		return StoredRetry{}, policy.ErrInvalid
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
	if e := j.saveRetry(ctx, r, s.Version); e != nil {
		return StoredRetry{}, e
	}
	saved, err := j.GetRetry(ctx, r.Command.TenantID, r.Command.Kind, r.Command.TargetID, r.ID)
	if err != nil {
		return StoredRetry{}, err
	}
	if !reflect.DeepEqual(saved.Request, r) {
		return StoredRetry{}, policy.ErrConflict
	}
	return saved, nil
}

// ListRetries 只返回一个明确裁决或关系的请求历史，不依赖 Redis 窗口仍存在。
func (j *Journal) ListRetries(ctx context.Context, tenant, kind, id, after string, limit int) (RetryPage, error) {
	if !retryScope(tenant, kind, id) {
		return RetryPage{}, policy.ErrInvalid
	}
	p := retryPrefix(tenant, kind, id)
	if after != "" && (!strings.HasPrefix(after, p) || !retryHash(strings.TrimPrefix(after, p))) {
		return RetryPage{}, policy.ErrInvalid
	}
	return j.retryPage(ctx, p, after, limit, false)
}

// RetryWork 只供控制面扫描 pending；执行前需精确读和取得同请求租约。
func (j *Journal) RetryWork(ctx context.Context, after string, limit int) (RetryPage, error) {
	return j.retryPage(ctx, "", after, limit, true)
}

func (j *Journal) retryPage(ctx context.Context, prefix, after string, limit int, work bool) (RetryPage, error) {
	if len(after) > 256 || limit < 1 || limit > 16 {
		return RetryPage{}, policy.ErrInvalid
	}
	var rows []json.RawMessage
	var e error
	if work {
		docs, ok := j.docs.(RetryDocuments)
		if !ok {
			return RetryPage{}, policy.ErrUnavailable
		}
		rows, e = docs.ListMergeRequests(ctx, prefix, after, limit)
	} else {
		rows, e = j.docs.List(ctx, "merge_requests", prefix, after, limit)
	}
	if e != nil {
		return RetryPage{}, e
	}
	if len(rows) > limit {
		return RetryPage{}, policy.ErrInvalid
	}
	page := RetryPage{Items: []RetryRequest{}}
	last := after
	for _, raw := range rows {
		r, e := decodeRetry(raw)
		if e != nil {
			return RetryPage{}, e
		}
		key := r.Cursor()
		if key <= last || !strings.HasPrefix(key, prefix) || (work && r.State != "pending") {
			return RetryPage{}, policy.ErrInvalid
		}
		last = key
		page.Items = append(page.Items, r)
	}
	if len(rows) == limit {
		page.Next = last
	}
	return page, nil
}
