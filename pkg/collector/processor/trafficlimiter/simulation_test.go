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
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	simTick        = 50 * time.Millisecond
	simRequestSize = 100
)

// simulation 让多个 Collector 实例共用一个 miniredis 与一个假时钟，按设定的到达速率逐 tick 发送请求，
// 每个同步周期以随机顺序执行一次定时同步，记录每个 tick 每个实例放行与被拒的字节。
type simulation struct {
	server    *miniredis.Miniredis
	clock     *fakeClock
	instances []*trafficLimiter
	base      time.Time
	step      int

	accepted [][]int64 // [step][instance]
	rejected [][]int64
}

func newSimulation(t *testing.T, instances int, bytesPerSecond, burstBytes int64) *simulation {
	t.Helper()
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	clock := newFakeClock()
	sim := &simulation{server: server, clock: clock, base: base}

	conf := policyMap(bytesPerSecond, burstBytes)
	conf["redis"] = map[string]any{
		"mode": redisModeStandalone, "db": 0,
		"key": "sim.traffic_limiter", "addrs": []string{server.Addr()},
	}
	for i := 0; i < instances; i++ {
		id := fmt.Sprintf("collector-%d", i)
		factory, err := newFactoryWithLeaser(conf, nil, clock.Now, func(config RedisConfig) quotaLeaser {
			return newRedisLeaserWithID(config, id)
		}, 0)
		require.NoError(t, err)
		t.Cleanup(factory.Clean)
		sim.instances = append(sim.instances, factory)
	}
	return sim
}

// run 按 rates(step) 给出的各实例到达速率（字节/秒）推进 steps 个 tick。
func (s *simulation) run(t *testing.T, steps int, rates func(step int) []float64) {
	t.Helper()
	carry := make([]float64, len(s.instances))
	for i := 0; i < steps; i++ {
		accepted := make([]int64, len(s.instances))
		rejected := make([]int64, len(s.instances))
		for index, rate := range rates(s.step) {
			carry[index] += rate * simTick.Seconds()
			for carry[index] >= simRequestSize {
				carry[index] -= simRequestSize
				decision, err := s.instances[index].limitSnapshot("token", map[string]int{"checkout": simRequestSize})
				require.NoError(t, err)
				if decision.accepted {
					accepted[index] += simRequestSize
				} else {
					rejected[index] += simRequestSize
				}
			}
		}
		s.accepted = append(s.accepted, accepted)
		s.rejected = append(s.rejected, rejected)

		s.step++
		s.clock.Advance(simTick)
		s.server.SetTime(s.base.Add(time.Duration(s.step) * simTick))
		if s.step%int(defaultSyncInterval/simTick) == 0 {
			for _, index := range rand.Perm(len(s.instances)) {
				s.instances[index].syncNow()
			}
		}
		if s.step%int(defaultSurplusCheckInterval/simTick) == 0 {
			for _, index := range rand.Perm(len(s.instances)) {
				s.instances[index].checkSurplusNow()
			}
		}
	}
}

// sum 返回 [from, to) 区间内各实例放行或被拒字节之和。
func sum(rows [][]int64, from, to int) []int64 {
	totals := make([]int64, len(rows[0]))
	for _, row := range rows[from:to] {
		for index, value := range row {
			totals[index] += value
		}
	}
	return totals
}

func total(values []int64) int64 {
	var result int64
	for _, value := range values {
		result += value
	}
	return result
}

// TestSimulationLoadShiftWithinLimit 覆盖「限额之内负载均衡转移不丢数据」：三个实例的流量
// 5/4/1 → 5/5/0 → 0/2/8 → 8/0/2（KB/s），总量分别为额度的 100% 与 90%。
// 每次转移都有一个实例骤降、另一个骤增，需求下降被发现前的滞后由透支覆盖。
func TestSimulationLoadShiftWithinLimit(t *testing.T) {
	for _, scale := range []float64{1.0, 0.9} {
		t.Run(fmt.Sprintf("load %.0f%%", scale*100), func(t *testing.T) {
			for run := 0; run < 5; run++ {
				sim := newSimulation(t, 3, 10000, 10000)
				sim.run(t, 80, func(step int) []float64 {
					rates := [][]float64{{5000, 4000, 1000}, {5000, 5000, 0}, {0, 2000, 8000}, {8000, 0, 2000}}[step/20]
					scaled := make([]float64, len(rates))
					for i, rate := range rates {
						scaled[i] = rate * scale
					}
					return scaled
				})
				assert.Zero(t, total(sum(sim.rejected, 0, 80)), "限额之内的流量转移不得丢数据")
			}
		})
	}
}

// TestSimulationSurgeWithinLimit 覆盖「某实例突增但总量在限额内」：C 从 0.5 突增到 8KB/s。
func TestSimulationSurgeWithinLimit(t *testing.T) {
	sim := newSimulation(t, 10, 10000, 10000)
	sim.run(t, 60, func(step int) []float64 {
		rates := make([]float64, 10)
		for i := range rates {
			rates[i] = 200
		}
		rates[2] = 500
		if step >= 20 {
			rates[2] = 8000
		}
		return rates
	})
	assert.Zero(t, total(sum(sim.rejected, 0, 60)), "限额之内的突增不得丢数据")
}

// TestSimulationOverLimitIsFlatAndFair 覆盖持续超限：总放行稳定在额度附近，
// 需求低于公平份额的实例拿满需求，不出现饿死。
func TestSimulationOverLimitIsFlatAndFair(t *testing.T) {
	sim := newSimulation(t, 10, 10000, 10000)
	rates := []float64{8000, 500, 500, 500, 500, 500, 500, 500, 500, 500}
	sim.run(t, 120, func(int) []float64 { return rates })

	// 前 3 秒消化共享桶的存量与透支（允许的毛刺），之后每秒都必须稳定在额度附近。
	for second := 3; second < 6; second++ {
		accepted := sum(sim.accepted, second*20, (second+1)*20)
		assert.InDelta(t, 10000, total(accepted), 1500, "第 %d 秒总放行应稳定在额度附近", second)
		for index := 1; index < 10; index++ {
			assert.InDelta(t, 500, accepted[index], 100, "第 %d 秒小实例 %d 应拿满需求", second, index)
		}
		assert.InDelta(t, 5500, accepted[0], 1500, "第 %d 秒大实例拿到剩余额度", second)
	}
}

// TestSimulationColdStartPeak 覆盖冷启动的峰值上界：10 个实例同时以 2KB/s 冷启动（总需求 20KB/s），
// 任意时长 t 内放行不超过「2 × 容量 + L × t」（容量来自共享桶，另一份来自各实例透支之和），之后稳定在额度。
func TestSimulationColdStartPeak(t *testing.T) {
	sim := newSimulation(t, 10, 10000, 10000)
	rates := make([]float64, 10)
	for i := range rates {
		rates[i] = 2000
	}
	sim.run(t, 80, func(int) []float64 { return rates })

	capacity := limiterConfig(10000, 10000).leaseCapacity()
	for end := 1; end <= 80; end++ {
		window := time.Duration(end) * simTick
		bound := 2*capacity + int64(10000*window.Seconds())
		assert.LessOrEqual(t, total(sum(sim.accepted, 0, end)), bound, "前 %s 的放行超过峰值上界", window)
	}
	for second := 3; second < 4; second++ {
		assert.InDelta(t, 10000, total(sum(sim.accepted, second*20, (second+1)*20)), 1500, "之后稳定在额度")
	}
}
