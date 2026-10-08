// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/policy"
	"linkd/internal/shieldcheck"
	"linkd/internal/store"
)

type shieldRequestChecker interface {
	CheckRequest(context.Context, shieldcheck.Request) (shieldcheck.Check, error)
}

// ShieldRequests 以租户准入锁保护 1024 条 pending 预算，再以请求锁和 fingerprint lease 串行执行。
// 请求记录和业务状态分别保存；重试先读记录，业务版本变化时标记失效，不能强制作用于新状态。
type ShieldRequests struct {
	journal *shieldcheck.Journal
	locker  scheduler.Locker
	reader  func(context.Context, string, string) (store.StoredAlert, error)
	checker shieldRequestChecker
	slots   chan struct{}
}

// NewShieldRequests 注入请求存储、分布式租约和受业务版本保护的执行器。
func NewShieldRequests(journal *shieldcheck.Journal, locker scheduler.Locker, reader func(context.Context, string, string) (store.StoredAlert, error), checker shieldRequestChecker) *ShieldRequests {
	return &ShieldRequests{journal: journal, locker: locker, reader: reader, checker: checker, slots: make(chan struct{}, 4)}
}

func shieldRequestLock(kind, tenant, id string) string {
	raw, _ := json.Marshal([]string{"shield-check-control", kind, tenant, id})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:])
}

// Request 接收一个明确命令，成功只证明已排队或同一命令已有记录，不证明屏蔽已经解除。
func (s *ShieldRequests) Request(ctx context.Context, c shieldcheck.Command) (shieldcheck.Request, error) {
	if c.Validate() != nil {
		return shieldcheck.Request{}, policy.ErrInvalid
	}
	if s.journal == nil || s.locker == nil || s.reader == nil || s.checker == nil {
		return shieldcheck.Request{}, policy.ErrUnavailable
	}
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	default:
		return shieldcheck.Request{}, policy.ErrPreviewCapacity
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var result shieldcheck.StoredRequest
	err := withAlertLease(ctx, s.locker, shieldRequestLock("admission", c.TenantID, ""), func() error {
		if old, err := s.journal.FindCommand(ctx, c); err == nil {
			result = old
			return nil
		} else if !errors.Is(err, policy.ErrNotFound) {
			return err
		}
		current, err := s.reader(ctx, c.TenantID, c.AlertID)
		if err != nil {
			return err
		}
		if current.Alert.BKTenantID != c.TenantID || current.Alert.AlertID != c.AlertID {
			return policy.ErrAccess
		}
		if current.Version.IsZero() || current.Alert.Validate() != nil {
			return policy.ErrInvalid
		}
		if current.Alert.Revision != c.ExpectedRevision {
			return policy.ErrConflict
		}
		result, err = s.journal.Enqueue(ctx, c, time.Now().UTC())
		return err
	})
	return result.Request, err
}

// Execute 只推进已有 pending；锁忙延后。最终记录一旦完成，再次扫描不会执行第二次检查。
func (s *ShieldRequests) Execute(ctx context.Context, request shieldcheck.Request) error {
	if request.Validate() != nil {
		return policy.ErrInvalid
	}
	if s.journal == nil || s.locker == nil || s.checker == nil {
		return policy.ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return withAlertLease(ctx, s.locker, shieldRequestLock("execute", request.Command.TenantID, request.ID), func() error {
		current, err := s.journal.GetRequest(ctx, request.Command.TenantID, request.Command.AlertID, request.ID)
		if err != nil {
			return err
		}
		if current.Request.State != "pending" {
			return nil
		}
		result, checkErr := s.checker.CheckRequest(ctx, current.Request)
		if errors.Is(checkErr, scheduler.ErrLockBusy) {
			return checkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if result.Validate() != nil {
			return errors.Join(checkErr, policy.ErrInvalid)
		}
		_, finishErr := s.journal.Finish(ctx, current, result)
		// 已保存的 failed/superseded 是请求的真实完成结果；扫描故障只统计记录未能保存等基础设施错误。
		if finishErr != nil {
			return errors.Join(checkErr, finishErr)
		}
		return nil
	})
}
