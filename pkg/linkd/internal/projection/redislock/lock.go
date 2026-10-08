// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package redislock 复用 owner-token Redis 租约串行化同一 Alert/投影目标的不同业务版本。
package redislock

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	redis "github.com/redis/go-redis/v9"
	"linkd/internal/lifecycle/scheduler"
	"linkd/internal/projection"
)

// Locker 隔离部署内的可靠输出；投影与动作共用目标键，不与 Lifecycle fingerprint 锁共用 key。
type Locker struct{ inner scheduler.Locker }

// New 建立固定 30 秒租约，覆盖 Service 的 10 秒执行上限；不启动续租 goroutine。
func New(client redis.UniversalClient, deployment string) (*Locker, error) {
	if deployment == "" || len(deployment) > 256 {
		return nil, projection.ErrInvalid
	}
	sum := sha256.Sum256([]byte(deployment))
	cfg := scheduler.DefaultConfig()
	cfg.LockKeyPrefix = "linkd:projection:lease:" + hex.EncodeToString(sum[:])
	cfg.LockTTL = 30 * time.Second
	cfg.RenewInterval = 10 * time.Second
	inner, err := scheduler.NewRedisLocker(client, cfg)
	if err != nil {
		return nil, err
	}
	return &Locker{inner: inner}, nil
}

// Acquire 使用 Service 的租户/Alert/目标摘要；锁忙明确延后，释放结果不确定仍返回错误。
func (l *Locker) Acquire(ctx context.Context, key string) (func(context.Context) error, error) {
	raw, err := hex.DecodeString(key)
	if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != key {
		return nil, projection.ErrInvalid
	}
	lease, err := l.inner.Acquire(ctx, key)
	if errors.Is(err, scheduler.ErrLockBusy) {
		return nil, projection.ErrBusy
	}
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context) error { return l.inner.Release(ctx, lease) }, nil
}

var _ projection.TargetLocker = (*Locker)(nil)
