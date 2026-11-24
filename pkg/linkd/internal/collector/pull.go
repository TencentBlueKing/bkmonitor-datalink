// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"linkd/internal/pluginruntime"
)

// ProgressReader 读取来源上次成功发布后的游标，失败不能退回空游标。
type ProgressReader interface {
	Load(context.Context, string, string) (map[string]any, error)
}

// nextPullTime 保留 KAC minute=*/N 语义：每小时重置分钟匹配，而不是固定间隔累加。
func nextPullTime(now time.Time, minutes int) time.Time {
	next := now.Truncate(time.Minute).Add(time.Minute)
	for next.Minute()%minutes != 0 {
		next = next.Add(time.Minute)
	}
	return next
}

// RunPull 在授权 Context 内串行拉取，不补跑停机期间全部周期，不积压重叠任务。
// 单次失败只结束当前周期；旧游标保留，下一周期仍可继续。退出前等待同步调用取消。
func RunPull(ctx context.Context, request pluginruntime.Request, service *Service, progress ProgressReader, timeout time.Duration, logger *slog.Logger, recovery *Recovery) error {
	initialState := request.State
	if timeout <= 0 || request.RecoveryIntervalMinutes < 0 {
		return fmt.Errorf("invalid source schedule timeout or recovery interval")
	}
	type schedule struct {
		mode     string
		interval int
		due      time.Time
	}
	recoverPending := func() error {
		if recovery == nil {
			return nil
		}
		err := recovery.Drain(ctx, request.TenantID, request.SourceID)
		if err != nil && ctx.Err() == nil {
			logger.ErrorContext(ctx, "source recovery delays next schedule", "module", "collector", "operation", "recover_pending", "error", err, "bk_tenant_id", request.TenantID, "event_source_id", request.SourceID)
		}
		return err
	}
	// 接管时先恢复；失败保持待处理状态，在下一计划时刻再次尝试，不使任务永久停止。
	if err := recoverPending(); errors.Is(err, ErrAuthorityLost) {
		return err
	}
	var schedules []schedule
	now := time.Now().UTC()
	if request.Mode == "pull" {
		if request.IntervalMinutes <= 0 {
			return fmt.Errorf("positive pull interval required")
		}
		schedules = append(schedules, schedule{"pull", request.IntervalMinutes, nextPullTime(now, request.IntervalMinutes)})
	}
	if request.RecoveryIntervalMinutes > 0 {
		schedules = append(schedules, schedule{"recovery", request.RecoveryIntervalMinutes, nextPullTime(now, request.RecoveryIntervalMinutes)})
	}
	if len(schedules) == 0 {
		return fmt.Errorf("source has no scheduled operation")
	}
	for {
		selected := 0
		for i := range schedules {
			if schedules[i].due.Before(schedules[selected].due) {
				selected = i
			}
		}
		current := &schedules[selected]
		slot := current.due
		timer := time.NewTimer(time.Until(slot))
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		if err := recoverPending(); err != nil {
			if errors.Is(err, ErrAuthorityLost) {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			current.due = nextPullTime(time.Now().UTC(), current.interval)
			continue
		}
		request.Mode = current.mode
		request.RequestID = current.mode + "/" + slot.Format(time.RFC3339)
		request.ReceivedAt = slot.Format(time.RFC3339)
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		state, err := progress.Load(callCtx, request.TenantID, request.SourceID)
		if err == nil {
			request.State = initialState
			if state != nil {
				request.State = state
			}
			_, err = service.Run(callCtx, request)
		}
		cancel()
		if errors.Is(err, ErrAuthorityLost) {
			return err
		}
		current.due = nextPullTime(time.Now().UTC(), current.interval)
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			logger.ErrorContext(ctx, "source scheduled execution failed", "module", "collector", "operation", current.mode, "error", err, "bk_tenant_id", request.TenantID, "event_source_id", request.SourceID, "request_id", request.RequestID)
		}
	}
}
