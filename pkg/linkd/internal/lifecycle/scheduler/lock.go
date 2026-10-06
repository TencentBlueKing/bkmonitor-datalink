// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	redis "github.com/redis/go-redis/v9"
)

var (
	// ErrLockBusy 表示 fingerprint 当前由另一个 Worker 持有。
	ErrLockBusy = errors.New("lifecycle fingerprint lock is busy")
	// ErrLeaseLost 表示当前 token 已不再拥有 fingerprint lease。
	ErrLeaseLost = errors.New("lifecycle fingerprint lease is lost")
)

const (
	renewScript   = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("PEXPIRE", KEYS[1], ARGV[2]) else return 0 end`
	releaseScript = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`
)

type redisClient interface {
	SetNX(ctx context.Context, key string, value any, expiration time.Duration) *redis.BoolCmd
	Eval(ctx context.Context, script string, keys []string, args ...any) *redis.Cmd
}

// Lease 是一次 fingerprint 处理权，只能由创建它的 Locker 续租和释放。
type Lease struct {
	key   string
	token string
}

// Locker 定义 Handler 所需的 fingerprint lease 操作。
type Locker interface {
	Acquire(ctx context.Context, correlationKey string) (Lease, error)
	Renew(ctx context.Context, lease Lease) error
	Release(ctx context.Context, lease Lease) error
}

// RedisLocker 使用 SET NX PX 和 compare-token Lua 管理 fingerprint lease。
type RedisLocker struct {
	client      redisClient
	config      Config
	tokenSource func() (string, error)
}

// NewRedisLocker 创建 Redis lease 实现。
func NewRedisLocker(client redis.UniversalClient, config Config) (*RedisLocker, error) {
	if client == nil {
		return nil, fmt.Errorf("create lifecycle Redis locker: client must not be nil")
	}
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("create lifecycle Redis locker: %w", err)
	}
	return &RedisLocker{client: client, config: config, tokenSource: randomToken}, nil
}

func newRedisLocker(client redisClient, config Config, tokenSource func() (string, error)) *RedisLocker {
	return &RedisLocker{client: client, config: config, tokenSource: tokenSource}
}

// Acquire 尝试取得 correlation key 的处理权。
func (l *RedisLocker) Acquire(ctx context.Context, correlationKey string) (Lease, error) {
	token, err := l.tokenSource()
	if err != nil {
		return Lease{}, fmt.Errorf("generate lifecycle lease token: %w", err)
	}
	lease := Lease{key: l.config.LockKeyPrefix + ":" + correlationKey, token: token}
	acquired, err := l.client.SetNX(ctx, lease.key, lease.token, l.config.LockTTL).Result()
	if err != nil {
		return Lease{}, fmt.Errorf("acquire lifecycle lease: %w", err)
	}
	if !acquired {
		return Lease{}, ErrLockBusy
	}
	return lease, nil
}

// Renew 只允许当前 token 延长自己的 lease。
func (l *RedisLocker) Renew(ctx context.Context, lease Lease) error {
	result, err := l.client.Eval(
		ctx,
		renewScript,
		[]string{lease.key},
		lease.token,
		l.config.LockTTL.Milliseconds(),
	).Int64()
	if err != nil {
		return fmt.Errorf("renew lifecycle lease: %w", err)
	}
	if result != 1 {
		return ErrLeaseLost
	}
	return nil
}

// Release 只删除 token 仍匹配的 lease，不能误删新 owner 的锁。
func (l *RedisLocker) Release(ctx context.Context, lease Lease) error {
	result, err := l.client.Eval(ctx, releaseScript, []string{lease.key}, lease.token).Int64()
	if err != nil {
		return fmt.Errorf("release lifecycle lease: %w", err)
	}
	if result != 1 {
		return ErrLeaseLost
	}
	return nil
}

func randomToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

var _ Locker = (*RedisLocker)(nil)
