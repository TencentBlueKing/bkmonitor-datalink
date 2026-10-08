// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package producer 在正式 Lifecycle 指纹租约内补齐持久动作意图，独立于进程和具体任务仓储装配。
package producer

import (
	"context"
	"errors"
	"strings"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/actiondelivery"
	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/store"
)

type actionCurrentReader interface {
	GetAlertCurrent(context.Context, string, string) (store.StoredAlert, error)
}

type actionFinisher interface {
	FinishActionDelivery(context.Context, string, string) (store.StoredAlert, error)
}

// Producer 在正式 Worker 的 tenant/source/fingerprint 租约内补齐动作入队，不读取最新来源配置。
// finisher 必须只补齐持久意图，并配置正确的 RecentAlertCache；生产不等待远端受理。
type Producer struct {
	alerts   actionCurrentReader
	finisher actionFinisher
	locker   func(string) (scheduler.Locker, error)
	slots    chan struct{}
}

// New 复用 Lifecycle.ForSource 的锁空间；client 由装配方持有并负责关闭。
// 单项十秒包含排队和重读，租约至少三十秒，退出时仍有两秒有界释放。
func New(cfg config.LifecycleConfig, deployment string, client redis.UniversalClient, alerts actionCurrentReader, finisher actionFinisher) (*Producer, error) {
	if client == nil || alerts == nil || finisher == nil || strings.TrimSpace(deployment) == "" || len(deployment) > 128 || cfg.Validate() != nil {
		return nil, actiondelivery.ErrInvalid
	}
	return &Producer{alerts: alerts, finisher: finisher, slots: make(chan struct{}, 4), locker: func(source string) (scheduler.Locker, error) {
		c := cfg.ForSource(deployment, source).SchedulerConfig()
		c.LockTTL = max(c.LockTTL, 30*time.Second)
		return scheduler.NewRedisLocker(client, c)
	}}, nil
}

// FinishActionDelivery 不接受扫描快照；租约前后均校验身份，取消及释放错误不伪装成入队完成。
func (p *Producer) FinishActionDelivery(ctx context.Context, tenant, id string) (result store.StoredAlert, err error) {
	if ctx == nil || domain.ValidateIdentityPart("tenant", tenant, 64) != nil || id == "" || len(id) > domain.EntityIDMaxBytes {
		return store.StoredAlert{}, actiondelivery.ErrInvalid
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case p.slots <- struct{}{}:
		defer func() { <-p.slots }()
	case <-call.Done():
		return result, call.Err()
	}
	current, err := p.alerts.GetAlertCurrent(call, tenant, id)
	if err != nil {
		return result, err
	}
	valid := func(v store.StoredAlert) bool {
		return !v.Version.IsZero() && v.Alert.Validate() == nil && v.Alert.BKTenantID == tenant && v.Alert.AlertID == id
	}
	if !valid(current) {
		return result, actiondelivery.ErrInvalid
	}
	locker, err := p.locker(current.Alert.EventSourceID)
	if err != nil {
		return result, err
	}
	lease, err := locker.Acquire(call, scheduler.CorrelationKey(tenant, current.Alert.EventSourceID, current.Alert.Fingerprint))
	if actionLeaseBusy(err) {
		return result, actiondelivery.ErrBusy
	}
	if err != nil {
		return result, err
	}
	defer func() {
		release, stop := context.WithTimeout(context.WithoutCancel(call), 2*time.Second)
		defer stop()
		err = errors.Join(err, locker.Release(release, lease))
	}()
	locked, err := p.alerts.GetAlertCurrent(call, tenant, id)
	if err != nil {
		return result, err
	}
	if !valid(locked) || locked.Alert.EventSourceID != current.Alert.EventSourceID || locked.Alert.Fingerprint != current.Alert.Fingerprint {
		return result, actiondelivery.ErrInvalid
	}
	if locked.Alert.ActionPending == nil {
		return locked, nil
	}
	result, err = p.finisher.FinishActionDelivery(call, tenant, id)
	if err != nil {
		return result, err
	}
	if !valid(result) || result.Alert.ActionPending != nil || result.Alert.Revision < locked.Alert.Revision || result.Alert.EventSourceID != locked.Alert.EventSourceID || result.Alert.Fingerprint != locked.Alert.Fingerprint {
		return store.StoredAlert{}, actiondelivery.ErrInvalid
	}
	return result, nil
}

// 只有整条错误链都表示锁忙才延后；Join 中的基础设施或取消错误仍向上返回。
func actionLeaseBusy(err error) bool {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		parts := joined.Unwrap()
		if len(parts) == 0 {
			return false
		}
		for _, part := range parts {
			if !actionLeaseBusy(part) {
				return false
			}
		}
		return true
	}
	if inner := errors.Unwrap(err); inner != nil {
		return actionLeaseBusy(inner)
	}
	return errors.Is(err, scheduler.ErrLockBusy)
}
