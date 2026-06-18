// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package throttle

import (
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// ResourceSampler 是后台采样回路：单例 goroutine 按 sample_interval 读一次 cgroup 水位，
// 算 CPU 快慢两路 EWMA 与内存占比，再交给 Manager 发布。请求路径只读发布结果，不碰 /proc 和 /sys。
type ResourceSampler struct {
	reader  Reader
	config  Config
	manager *Manager

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

	// 以下字段只在采样 goroutine 内读写，无需加锁。
	prevUsage uint64    // 上一次的 CPU 累计耗时，用来求差
	prevAt    time.Time // 上一次采样时刻
	slow      float64   // 慢信号 EWMA，驱动分级
	fast      float64   // 快信号 EWMA，驱动熔断
	hasCPU    bool      // 是否已拿到第一帧利用率
}

func NewResourceSampler(reader Reader, config Config, manager *Manager) *ResourceSampler {
	return &ResourceSampler{
		reader:  reader,
		config:  config,
		manager: manager,
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}
}

// Start 拉起采样 goroutine，按周期 tick，直到 Stop。
func (s *ResourceSampler) Start() {
	go func() {
		defer close(s.doneCh)
		ticker := time.NewTicker(s.config.SampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.tick()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop 停采样并等 goroutine 退出，可重复调用。
func (s *ResourceSampler) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopCh)
		<-s.doneCh
	})
}

func (s *ResourceSampler) tick() {
	s.tickAt(time.Now())
}

// tickAt 是一次完整采样：求 CPU 利用率、更新两路 EWMA、算内存占比，最后发布水位。
func (s *ResourceSampler) tickAt(now time.Time) { // now 抽成入参，方便测试注入时间
	usage, err := s.reader.CPUUsageNanos()
	if err != nil {
		logger.Warnf("failed to read cgroup cpu usage: %v", err)
	}

	// CPU 利用率是速率量，得两次采样求差。
	// 未获取到第一帧、时钟回拨、计数器回退都跳过。
	cpuOK := false
	cpuRaw := 0.0
	if err == nil && !s.prevAt.IsZero() && now.After(s.prevAt) && usage >= s.prevUsage {
		// 分母用容器配额下的有效核数，读不到才回退 fallback_cores，绝不用宿主全核数。
		cores, ok := s.reader.EffectiveCores()
		if !ok || cores <= 0 {
			cores = fallbackCores(s.config)
		}
		elapsed := now.Sub(s.prevAt).Seconds()
		if elapsed > 0 && cores > 0 {
			// 利用率 = Δusage / (Δwall × effCores)，过载时会大于 1，不要截断，留给状态机判分级/熔断。
			cpuRaw = float64(usage-s.prevUsage) / (elapsed * float64(time.Second) * cores)
			cpuOK = true
		}
	}
	// 读失败就不挪基线，下次采样成功时跨更长区间回补。
	if err == nil {
		s.prevUsage = usage
		s.prevAt = now
	}

	if cpuOK {
		if !s.hasCPU {
			// 第一帧直接当基线，避免冷启动误判。
			s.slow = cpuRaw
			s.fast = cpuRaw
			s.hasCPU = true
		} else {
			s.slow = ewma(s.slow, cpuRaw, s.config.Signal.CPUSlowBeta)
			s.fast = ewma(s.fast, cpuRaw, s.config.Signal.CPUFastBeta)
		}
	}

	level := WaterLevel{CPUSlow: s.slow, CPUFast: s.fast}

	// 读取内存使用率（不含 Cache）。
	if workingSet, ok := s.reader.MemWorkingSet(); ok {
		if limit, limitOK := s.reader.MemLimit(); limitOK && limit > 0 {
			level.Mem = float64(workingSet) / float64(limit)
			level.MemValid = true
		}
	}

	// 发布水位。
	s.manager.Publish(level)
}

func ewma(prev, current, beta float64) float64 {
	// 一阶低通（同 RFC 6298 的 RTT 平滑）：beta 是历史权重，越大越平滑、越滞后。
	// 例：慢信号 beta=0.95 时新采样只占 5%，抗抖；快信号 beta=0.7 占 30%，更跟得上尖刺。
	return (1-beta)*current + beta*prev
}
