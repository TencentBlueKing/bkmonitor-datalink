// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

/*
# TrafficLimiter: 服务级字节流量限流器

processor:
  - name: "traffic_limiter/gcra"
    config:
      bytes_per_second: 0
      burst_bytes: 0
      redis:
        mode: standalone
        db: 8
        key: bkcollector.traffic_limiter
        addrs: ["127.0.0.1:6379"]
        password: ""

bytes_per_second 为 0 表示该范围未开启限流，请求在计算逻辑字节之前就返回，
因此既不访问 Redis 也不产生流量指标；灰度期间的开销只落在已开启额度的 Token 上。

weights 是各信号的计费单价，扣减额度时按「逻辑字节 × 权重」折算，默认 1。它不参与
Redis Key 的摘要，因此调整权重立即生效且不会重置任何桶。指标记录的仍是原始逻辑字节。
三种信号折算后扣同一个服务的余额，额度按 {token, service} 划分，服务之间互不挤占。

## 请求路径与 Redis 解耦

请求从不访问 Redis。每个 {token, service} 在本实例上有一份计费字节余额，请求只做内存扣减：
余额高于透支线（负的一个同步周期额度，即 bytes_per_second × 0.5s）就放行并扣减，扣减后可以为负。
因此不欠账时单个超过 burst_bytes 的请求也能通过，随后按速率还清，长期平均速率不变。

后台每 500ms 同步一次：取出本周期有用量或仍有欠账的服务，申请「用量 + 被拒需求 − 余额」
（即预取一个周期并还清欠账），用一次 Lua 按共享 GCRA 批量授予，桶里剩多少给多少，再加回各自的余额。
共享桶已空时授予 0，各实例的余额停在透支线上拒绝请求，跨实例共享额度就这样生效。
各实例按需申请、不按实例数平分，流量不均时额度跟着流量走。没有活跃服务的周期不访问 Redis。

共享桶的容量取 max(burst_bytes, bytes_per_second × 1s)，单次申请也不超过它：一次同步至多领到一个桶，
若容量小于一个周期的额度，实际速率会被压到「容量 / 周期」。因此 burst_bytes 小于 1 秒额度时，
共享模式下的实际突发会按 1 秒额度放大；本地模式仍按 burst_bytes 补充。

代价是允许小幅波动：生效延迟最多一个同步周期，跨实例的超额至多每实例「透支线 + 一个请求」，
空闲下来的实例手上至多留着一个周期的预取量不归还。

## Redis 不可用

不熔断。距最近一次同步成功超过 1s（两个周期）时，请求路径自行切到本地模式，余额按速率在本地补充、
上限 burst_bytes，即每实例独立限流，额度放大为「实例数 × 额度」；下一次同步成功立即回到共享。
判断在请求路径上读一个时间戳，同步卡在一次慢调用里也不影响请求。Redis 偏慢但能在 1s 内返回时
仍是共享模式，只是额度更新慢一些。同步对 Redis 的压力是每实例每 500ms 一次批量调用，与请求量无关。

## 配置与降级

redis 段可以整体省略，此时处理器空转，效果等同于额度为 0。

Factory 在任何情况下都不返回错误：Processor 构建失败会让整条 Pipeline 建不起来，
该信号的全部请求随后被判为 400。限流是保护性功能，配置问题只降级、绝不中断数据通路。
redis 段非法时保留额度语义但退化为本地模式，与 Redis 运行时故障走同一条路径。

## 指标

traffic_limiter_bytes_total 看谁被限了；traffic_limiter_decisions_total 看判断时用的是共享额度还是
本地降级（缺了它，Redis 故障后额度放大 N 倍在字节指标上完全看不出来）；traffic_limiter_sync_total
看后台同步是否成功；traffic_limiter_mode 看当前配置处于 disabled / local_only / shared 哪种模式。

Redis 侧的 GCRA 使用 Lua 在 TIME 之后写入状态，要求 Redis 5 及以上版本。
更低版本的脚本会直接报错，同步持续失败，限流器停留在本地模式。
*/

package trafficlimiter
