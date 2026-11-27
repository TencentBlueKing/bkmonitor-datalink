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
	tick time.Duration
	// beforeStep 若不为空，在每个 tick 发送请求之前调用，用来在指定时刻让实例退出。
	beforeStep func(step int)
	server     *miniredis.Miniredis
	clock      *fakeClock
	instances  []*trafficLimiter
	base       time.Time
	step       int

	accepted [][]int64 // [step][instance]
	rejected [][]int64
}

func newSimulation(t *testing.T, instances int, bytesPerSecond, burstBytes int64) *simulation {
	t.Helper()
	server := miniredis.RunT(t)
	base := time.Unix(1700000000, 0)
	server.SetTime(base)
	clock := newFakeClock()
	sim := &simulation{tick: simTick, server: server, clock: clock, base: base}

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
		// 仿真用假时钟推进，Redis 往返却按真实时间计量：放大等待上限与健康阈值，
		// 避免 -race 等拖慢执行的环境把 miniredis 判成「太慢」而切到本地降级。
		factory.snapshot().redis.waitBudget = time.Minute
		factory.snapshot().redis.healthyLatency = time.Minute
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
			carry[index] += rate * s.tick.Seconds()
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
		s.clock.Advance(s.tick)
		s.server.SetTime(s.base.Add(time.Duration(s.step) * s.tick))
		if s.step%int(defaultSyncInterval/s.tick) == 0 {
			for _, index := range rand.Perm(len(s.instances)) {
				s.instances[index].syncNow()
			}
		}
		if s.step%int(defaultSurplusCheckInterval/s.tick) == 0 {
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

// arrivals 返回第 step 个 tick 各实例到达的字节数，同一 tick 内的请求在同一时刻（零时长）到达。
type arrivals func(step int) []int64

// every 每 period 个 tick 在第 offset 个 tick 瞬时到达 bytes 字节。
func every(period, offset int, bytes int64) func(step int) int64 {
	return func(step int) int64 {
		if step%period == offset%period {
			return bytes
		}
		return 0
	}
}

// idealAdmitted 用一个能看到全部流量、逐请求判断的理想全局令牌桶处理同样的到达序列，返回每个 tick 放行的字节。
// 它是单机 GCRA 的语义上限：分布式实现在同样配置下不应比它拒绝得更多。
func idealAdmitted(plan arrivals, steps int, size int64, bytesPerSecond, capacity float64) []int64 {
	tokens := capacity
	admitted := make([]int64, steps)
	for step := 0; step < steps; step++ {
		tokens = min(capacity, tokens+bytesPerSecond*simTick.Seconds())
		for _, bytes := range plan(step) {
			for n := bytes / size; n > 0; n-- {
				if tokens >= float64(size) {
					tokens -= float64(size)
					admitted[step] += size
				}
			}
		}
	}
	return admitted
}

// runArrivals 按 plan 推进 steps 个 tick，alive 为假的实例不发请求也不同步（模拟进程直接退出）。
func (s *simulation) runArrivals(t *testing.T, steps int, size int64, plan arrivals, alive func(step, index int) bool) {
	t.Helper()
	for i := 0; i < steps; i++ {
		if s.beforeStep != nil {
			s.beforeStep(s.step)
		}
		accepted := make([]int64, len(s.instances))
		rejected := make([]int64, len(s.instances))
		for index, bytes := range plan(s.step) {
			if !alive(s.step, index) {
				continue
			}
			for n := bytes / size; n > 0; n-- {
				decision, err := s.instances[index].limitSnapshot("token", map[string]int{"checkout": int(size)})
				require.NoError(t, err)
				if decision.accepted {
					accepted[index] += size
				} else {
					rejected[index] += size
				}
			}
		}
		s.accepted = append(s.accepted, accepted)
		s.rejected = append(s.rejected, rejected)

		s.step++
		s.clock.Advance(s.tick)
		s.server.SetTime(s.base.Add(time.Duration(s.step) * s.tick))
		for _, index := range rand.Perm(len(s.instances)) {
			if !alive(s.step-1, index) {
				continue
			}
			if s.step%int(defaultSyncInterval/s.tick) == 0 {
				s.instances[index].syncNow()
			}
			if s.step%int(defaultSurplusCheckInterval/s.tick) == 0 {
				s.instances[index].checkSurplusNow()
			}
		}
	}
}

func alwaysAlive(int, int) bool { return true }

// steady 以 rate 字节/秒连续到达、每个请求 size 字节：返回第 step 个 tick 到达的整请求字节数。
func steady(rate, size int64, step int) int64 {
	perTick := float64(rate) * simTick.Seconds() / float64(size)
	return (int64(float64(step+1)*perTick) - int64(float64(step)*perTick)) * size
}

// TestSimulationBurstsMatchIdealTokenBucket 覆盖「宏观平均速率不变，但流量瞬时到达」：
// 每次突发的全部请求在同一时刻到达，与理想的全局令牌桶（L = 9KB/s，C = 9KB）比较拒绝量。
// 理想桶不拒绝的场景里，分布式实现也不能因为预取、需求估计或公平分配而多拒。
func TestSimulationBurstsMatchIdealTokenBucket(t *testing.T) {
	const steps = 200 // 10 秒
	tests := []struct {
		name string
		size int64
		plan arrivals
		// tolerance 是相对理想桶允许多拒的比例。只有多个实例在同一瞬间突发、且平均负载达到额度时才不为 0。
		tolerance float64
	}{
		{"每秒突发 3KB，三个实例错开，负载 100%", 100, func(s int) []int64 {
			return []int64{every(20, 0, 3000)(s), every(20, 7, 3000)(s), every(20, 13, 3000)(s)}
		}, 0},
		{"每 2 秒突发 4.2KB，错开，负载 70%", 100, func(s int) []int64 {
			return []int64{every(40, 0, 4200)(s), every(40, 13, 4200)(s), every(40, 27, 4200)(s)}
		}, 0},
		{"只有一个实例每 2 秒突发 9KB", 100, func(s int) []int64 { return []int64{every(40, 0, 9000)(s), 0, 0} }, 0},
		{"两个实例轮流每秒突发 6KB", 100, func(s int) []int64 {
			return []int64{every(40, 0, 6000)(s), every(40, 20, 6000)(s), 0}
		}, 0},
		{"三个实例同一瞬间突发，负载 60%，1KB 请求", 1000, func(s int) []int64 {
			return []int64{every(20, 0, 1800)(s), every(20, 0, 1800)(s), every(20, 0, 1800)(s)}
		}, 0},
		{"三个实例同一瞬间突发，负载 80%，100B 请求", 100, func(s int) []int64 {
			return []int64{every(20, 0, 2400)(s), every(20, 0, 2400)(s), every(20, 0, 2400)(s)}
		}, 0.01},
		{"三个实例同一瞬间突发，负载 100%，1KB 请求", 1000, func(s int) []int64 {
			return []int64{every(20, 0, 3000)(s), every(20, 0, 3000)(s), every(20, 0, 3000)(s)}
		}, 0.03},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ideal := idealAdmitted(tt.plan, steps, tt.size, 9000, 9000)
			var arrived int64
			for step := 0; step < steps; step++ {
				for _, bytes := range tt.plan(step) {
					arrived += bytes / tt.size * tt.size
				}
			}
			idealRejected := arrived - total(ideal)
			for run := 0; run < 3; run++ {
				sim := newSimulation(t, 3, 9000, 9000)
				sim.runArrivals(t, steps, tt.size, tt.plan, alwaysAlive)
				rejected := total(sum(sim.rejected, 0, steps))
				t.Logf("拒绝 %d / %d 字节（%.2f%%），理想桶拒绝 %d", rejected, arrived,
					float64(rejected)*100/float64(arrived), idealRejected)
				assert.LessOrEqual(t, float64(rejected-idealRejected), tt.tolerance*float64(arrived),
					"拒绝 %d 字节，理想桶拒绝 %d，共到达 %d", rejected, idealRejected, arrived)
			}
		})
	}
}

// TestSimulationBurstsOverCapacityStayBounded 覆盖单次突发超过容量：超出部分必须被拒，
// 任意时长 t 内放行不超过「2 × 容量 + L × t」，长期速率不超过额度。
func TestSimulationBurstsOverCapacityStayBounded(t *testing.T) {
	const steps = 200
	plan := func(s int) []int64 {
		return []int64{every(40, 0, 6000)(s), every(40, 0, 6000)(s), every(40, 0, 6000)(s)}
	}
	sim := newSimulation(t, 3, 9000, 9000)
	sim.runArrivals(t, steps, 100, plan, alwaysAlive)

	capacity := limiterConfig(9000, 9000).leaseCapacity()
	for end := 1; end <= steps; end++ {
		bound := 2*capacity + int64(9000*(time.Duration(end)*simTick).Seconds())
		assert.LessOrEqual(t, total(sum(sim.accepted, 0, end)), bound)
	}
	assert.Positive(t, total(sum(sim.rejected, 0, steps)), "单次突发超过容量，超出部分必须被拒")
	assert.LessOrEqual(t, total(sum(sim.accepted, 0, steps)), int64(9000*10)+capacity, "长期速率不超过额度")
}

// TestSimulationRestart 覆盖服务重启：三个实例各 3KB/s，之后全部直接退出（不退还预取、需求表要 2s 才过期），
// 新实例承接全部 9KB/s。流量连续或每秒在同一瞬间到达都不能丢数据。
func TestSimulationRestart(t *testing.T) {
	for _, tt := range []struct {
		name   string
		bursty bool
		size   int64
	}{
		{"连续，100B 请求", false, 100},
		{"连续，1KB 请求", false, 1000},
		{"每秒瞬时突发，100B 请求", true, 100},
		{"每秒瞬时突发，3KB 请求", true, 3000},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for run := 0; run < 3; run++ {
				sim := newSimulation(t, 4, 9000, 9000)
				plan := func(s int) []int64 {
					rates := []int64{3000, 3000, 3000, 0}
					if s >= 60 {
						rates = []int64{0, 0, 0, 9000}
					}
					for i, rate := range rates {
						if tt.bursty {
							rates[i] = every(20, 0, rate)(s)
						} else {
							rates[i] = steady(rate, tt.size, s)
						}
					}
					return rates
				}
				alive := func(step, index int) bool { return (index == 3) == (step >= 60) }
				sim.runArrivals(t, 120, tt.size, plan, alive)
				arrived := sum(sim.accepted, 60, 120)[3] + sum(sim.rejected, 60, 120)[3]
				require.Equal(t, int64(27000), arrived, "新实例 3 秒内承接 27KB")
				assert.Zero(t, sum(sim.rejected, 60, 120)[3], "新实例承接全部流量后不得丢数据")
			}
		})
	}
}

// TestSimulationReleaseSwitchToExistingInstance 是评审第三轮压测的回归：L = B = 10,240,000，
// A/B/C 每秒前 100ms 各发约 3.33MB、后 900ms 空闲；B/C 在空闲期的任意相位退出后，
// 下一波约 10MB 在 100ms 内全部切到存量实例 A。单请求 24KB，仿真粒度 10ms。
//
// 发布只改变实例间的流量分布，全局每秒总量与单波数据都没有超过额度，任何 100ms 窗口都不允许出现拒绝。
// 只按 1 秒平均值检查会掩盖这类问题：同样的总量均匀铺满 1 秒时，旧实现也不会拒绝。
func TestSimulationReleaseSwitchToExistingInstance(t *testing.T) {
	const (
		tick  = 10 * time.Millisecond
		size  = int64(24000)
		limit = int64(10240000)
	)
	ticksPerSecond := int(time.Second / tick)
	window := int(100 * time.Millisecond / tick)
	for _, graceful := range []bool{true, false} {
		name := "正常退出并归还额度"
		if !graceful {
			name = "直接退出（不归还）"
		}
		t.Run(name, func(t *testing.T) {
			for killAt := window; killAt < ticksPerSecond; killAt += 5 {
				sim := newSimulation(t, 3, limit, limit)
				sim.tick = tick
				killStep := 4*ticksPerSecond + killAt
				sim.beforeStep = func(step int) {
					if graceful && step == killStep {
						sim.instances[1].Stop()
						sim.instances[2].Stop()
					}
				}
				plan := func(step int) []int64 {
					if step%ticksPerSecond >= window {
						return []int64{0, 0, 0}
					}
					if step < killStep {
						per := limit / 3 / int64(window) / size * size
						return []int64{per, per, per}
					}
					return []int64{limit / int64(window) / size * size, 0, 0}
				}
				alive := func(step, index int) bool { return index == 0 || step < killStep }
				sim.runArrivals(t, 9*ticksPerSecond, size, plan, alive)

				for from := 0; from < len(sim.rejected); from += window {
					rejected := total(sum(sim.rejected, from, from+window))
					if !assert.Zero(t, rejected, "B/C 在空闲期第 %dms 退出：第 %dms 起的 100ms 内拒绝 %d 字节",
						killAt*int(tick/time.Millisecond), from*int(tick/time.Millisecond), rejected) {
						break
					}
				}
				require.Equal(t, int64(4*10*(limit/int64(window)/size*size)),
					total(sum(sim.accepted, 5*ticksPerSecond, 9*ticksPerSecond)), "切流后 4 波全部放行")
			}
		})
	}
}
