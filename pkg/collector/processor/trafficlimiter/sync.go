// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trafficlimiter

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/pkg/errors"
)

const (
	// 后台同步间隔。每个周期把活跃服务的用量批量提交给 Redis，并预取下一个周期的额度。
	// 透支线也取一个周期的额度，周期越长，跨实例的超额与生效延迟越大。
	defaultSyncInterval = 500 * time.Millisecond

	// 单次同步（全部批次）的上限。同步不阻塞业务请求，可以比请求路径宽松得多；
	// Lua 已在 Redis 执行但客户端超时的那次授予会丢失，放宽超时让它只在真正断网时发生。
	defaultSyncTimeout = time.Second

	// 距最近一次同步成功超过这么久，请求路径切到本地模式按速率补充余额。
	// 取两个周期：单次同步慢一点或偶尔失败一次仍保持共享语义，不会来回切换。
	defaultSyncStaleAfter = 2 * defaultSyncInterval

	// 单次 EVAL 携带的服务数上限，服务很多时分批提交，避免一次脚本执行太久占住 Redis。
	defaultLeaseBatchSize = 256
)

// quotaLeaser 是共享额度的来源。
type quotaLeaser interface {
	// Lease 为一批服务申请计费字节，返回值与 requests 一一对应，为实际授予量（0 表示共享桶已空）。
	Lease(ctx context.Context, requests []leaseRequest) ([]int64, error)
	Close() error
}

type quotaLeaserFactory func(RedisConfig) quotaLeaser

// quotaSyncer 在后台定期把本实例的用量同步到共享额度，生命周期与一个 Redis 客户端绑定：
// Reload 换客户端、Clean 释放客户端时一并停止。
type quotaSyncer struct {
	leaser quotaLeaser
	quota  *quotaManager
	now    func() time.Time

	done      chan struct{}
	closeOnce sync.Once
	closeErr  error
}

// newQuotaSyncer 创建同步器，interval 为 0 时不启动后台 goroutine，由调用方手动 sync（测试使用）。
//
// 新客户端从共享模式开始：若从本地模式开始，每次 Reload 重建客户端都会让每个实例按满桶放行一次。
// Redis 若确实不可用，最多 defaultSyncStaleAfter 后切到本地模式。
func newQuotaSyncer(
	leaser quotaLeaser,
	quota *quotaManager,
	now func() time.Time,
	interval time.Duration,
) *quotaSyncer {
	s := &quotaSyncer{leaser: leaser, quota: quota, now: now, done: make(chan struct{})}
	quota.markSynced(now())
	if interval > 0 {
		go s.run(interval)
	}
	return s
}

func (s *quotaSyncer) run(interval time.Duration) {
	// 首次同步加随机偏移，避免同时启动的实例总在同一时刻访问 Redis；
	// 额度紧张时谁先同步谁先拿到，偏移也让各实例的先后顺序不固定。
	jitter := time.NewTimer(rand.N(interval))
	select {
	case <-s.done:
		jitter.Stop()
		return
	case <-jitter.C:
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		s.sync()
		select {
		case <-s.done:
			return
		case <-ticker.C:
		}
	}
}

// sync 完成一次同步：取出各服务的申请量，分批向 Redis 申请并写回余额。
// 中途某一批失败时，之前批次领到的额度照常写回，未提交的用量体现在负余额里，下次同步一并补扣。
//
// 没有需要申请的服务时不访问 Redis，直接记为同步成功：平台可能给所有 Collector 下发 Redis 而额度为 0，
// 空闲实例每个周期一次空调用，乘上实例数就是对 Redis 的无谓压力。这样做不会多放行：
// 流量到来后的第一个周期只能在透支线内放行，与冷启动相同，Redis 若不可用，1 个周期后同步失败、再过 1s 切到本地。
func (s *quotaSyncer) sync() {
	requests := s.quota.collect()
	if len(requests) == 0 {
		s.quota.markSynced(s.now())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), defaultSyncTimeout)
	defer cancel()

	for start := 0; start < len(requests); start += defaultLeaseBatchSize {
		batch := requests[start:min(start+defaultLeaseBatchSize, len(requests))]
		grants, err := s.leaser.Lease(ctx, batch)
		if err == nil && len(grants) != len(batch) {
			err = errors.Errorf("traffic limiter lease returned %d grants for %d requests", len(grants), len(batch))
		}
		if err != nil {
			s.quota.markFailed(err)
			syncFailed.Inc()
			warnRedisFailure(err)
			return
		}
		s.quota.apply(batch, grants)
	}
	s.quota.markSynced(s.now())
	syncSucceeded.Inc()
}

// Close 可能被重复调用（Reload 关闭旧客户端后 Clean 仍持有同一快照），只执行一次。
// 不等待在途的同步返回：关闭客户端会让它尽快失败，迟到写回的授予本身也是合法额度。
func (s *quotaSyncer) Close() error {
	s.closeOnce.Do(func() {
		close(s.done)
		s.closeErr = s.leaser.Close()
	})
	return s.closeErr
}
