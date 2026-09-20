// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cleaner

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
)

type backpressureRedis interface {
	XInfoGroups(ctx context.Context, key string) *redis.XInfoGroupsCmd
}

// BackpressureConfig 描述 Signal Consumer Group 积压的采样和迟滞边界。
type BackpressureConfig struct {
	Stream        string
	Group         string
	CacheTTL      time.Duration
	QueryTimeout  time.Duration
	HighWatermark int64
	LowWatermark  int64
}

func (c BackpressureConfig) validate() error {
	if strings.TrimSpace(c.Stream) == "" || strings.TrimSpace(c.Group) == "" {
		return fmt.Errorf("stream and group are required")
	}
	if c.CacheTTL < time.Second || c.CacheTTL > time.Minute {
		return fmt.Errorf("cache TTL must be between 1s and 1m")
	}
	if c.QueryTimeout <= 0 || c.QueryTimeout > c.CacheTTL {
		return fmt.Errorf("query timeout must be positive and not exceed cache TTL")
	}
	if c.LowWatermark <= 0 || c.HighWatermark <= c.LowWatermark {
		return fmt.Errorf("watermarks must satisfy 0 < low < high")
	}
	return nil
}

// BackpressureObservation 是一次真实 Redis 采样的低基数结果。
type BackpressureObservation struct {
	Outcome    string
	Unresolved int64
	Paused     bool
}

// BackpressureObserver 记录进程共享背压检查器的状态，不得阻塞或改变检查结果。
type BackpressureObserver interface {
	BackpressureChecked(ctx context.Context, observation BackpressureObservation)
	BackpressureTransition(ctx context.Context, action string)
}

type noopBackpressureObserver struct{}

func (noopBackpressureObserver) BackpressureChecked(context.Context, BackpressureObservation) {}

func (noopBackpressureObserver) BackpressureTransition(context.Context, string) {}

// ReceiveDecision 表示 Cleaner 当前是否可以请求下一批消息，以及最早重查时间。
type ReceiveDecision struct {
	Allowed   bool
	RecheckAt time.Time
}

// ReceiveGate 在 Cleaner 发起下一次 Receive 前提供进程共享的准入判断。
type ReceiveGate interface {
	Check(ctx context.Context) ReceiveDecision
}

// SignalBackpressureChecker 懒采样目标 Consumer Group 的 lag + pending。
// 第一个遇到缓存过期的调用方同步查询 Redis；查询期间其他调用方继续使用旧状态。
type SignalBackpressureChecker struct {
	client   backpressureRedis
	config   BackpressureConfig
	observer BackpressureObserver
	now      func() time.Time

	mu          sync.Mutex
	allowed     bool
	checking    bool
	nextCheckAt time.Time
}

// NewSignalBackpressureChecker 创建进程级共享检查器；构造过程不访问 Redis。
func NewSignalBackpressureChecker(
	client redis.UniversalClient,
	config BackpressureConfig,
	observer BackpressureObserver,
) (*SignalBackpressureChecker, error) {
	if client == nil {
		return nil, fmt.Errorf("create signal backpressure checker: redis client must not be nil")
	}
	return newSignalBackpressureChecker(client, config, observer, time.Now)
}

func newSignalBackpressureChecker(
	client backpressureRedis,
	config BackpressureConfig,
	observer BackpressureObserver,
	now func() time.Time,
) (*SignalBackpressureChecker, error) {
	if client == nil {
		return nil, fmt.Errorf("create signal backpressure checker: redis client must not be nil")
	}
	if err := config.validate(); err != nil {
		return nil, fmt.Errorf("create signal backpressure checker: %w", err)
	}
	if observer == nil {
		observer = noopBackpressureObserver{}
	}
	if now == nil {
		return nil, fmt.Errorf("create signal backpressure checker: clock must not be nil")
	}
	return &SignalBackpressureChecker{
		client: client, config: config, observer: observer, now: now, allowed: true,
	}, nil
}

// Check 返回缓存的准入状态；缓存过期时最多由一个调用方刷新。
// Redis 查询失败或 lag 未知时 fail-open，Group 明确缺失时 fail-closed。
func (c *SignalBackpressureChecker) Check(ctx context.Context) ReceiveDecision {
	now := c.now()
	c.mu.Lock()
	if c.checking || now.Before(c.nextCheckAt) {
		decision := ReceiveDecision{Allowed: c.allowed, RecheckAt: c.nextCheckAt}
		c.mu.Unlock()
		return decision
	}
	previous := c.allowed
	c.checking = true
	c.nextCheckAt = now.Add(c.config.CacheTTL)
	c.mu.Unlock()

	queryCtx, cancel := context.WithTimeout(ctx, c.config.QueryTimeout)
	groups, err := c.client.XInfoGroups(queryCtx, c.config.Stream).Result()
	cancel()

	allowed := previous
	unresolved := int64(-1)
	outcome := "sampled"
	if err != nil && redis.HasErrorPrefix(err, "no such key") {
		allowed = false
		outcome = "group_missing"
	} else if err != nil {
		allowed = true
		outcome = "query_failed"
	} else {
		found := false
		for _, group := range groups {
			if group.Name != c.config.Group {
				continue
			}
			found = true
			if group.Lag < 0 {
				allowed = true
				outcome = "lag_unknown"
				break
			}
			if group.Pending > math.MaxInt64-group.Lag {
				unresolved = math.MaxInt64
			} else {
				unresolved = group.Lag + group.Pending
			}
			switch {
			case unresolved >= c.config.HighWatermark:
				allowed = false
			case unresolved <= c.config.LowWatermark:
				allowed = true
			}
			break
		}
		if !found {
			allowed = false
			outcome = "group_missing"
		}
	}

	c.mu.Lock()
	c.allowed = allowed
	c.checking = false
	c.nextCheckAt = c.now().Add(c.config.CacheTTL)
	decision := ReceiveDecision{Allowed: allowed, RecheckAt: c.nextCheckAt}
	c.mu.Unlock()

	c.observer.BackpressureChecked(ctx, BackpressureObservation{
		Outcome: outcome, Unresolved: unresolved, Paused: !allowed,
	})
	if previous != allowed {
		action := "resume"
		if !allowed {
			action = "pause"
		}
		c.observer.BackpressureTransition(ctx, action)
	}
	return decision
}

var _ ReceiveGate = (*SignalBackpressureChecker)(nil)
