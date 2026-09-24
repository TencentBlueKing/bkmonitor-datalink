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

指标三条：traffic_limiter_bytes_total 看谁被限了，traffic_limiter_decisions_total
看额度是否仍在跨实例共享（缺了它，Redis 故障后额度放大 N 倍在字节指标上完全看不出来），
traffic_limiter_mode 看当前配置处于 disabled / local_only / shared 哪种模式。

redis 段可以整体省略，此时处理器空转，效果等同于额度为 0。

Factory 在任何情况下都不返回错误：Processor 构建失败会让整条 Pipeline 建不起来，
该信号的全部请求随后被判为 400。限流是保护性功能，配置问题只降级、绝不中断数据通路。
redis 段非法时保留额度语义但退化为每实例本地 GCRA，与 Redis 运行时故障走同一条路径。

Redis 侧的 GCRA 使用 Lua 在 TIME 之后写入状态，要求 Redis 5 及以上版本。
更低版本的脚本会直接报错，限流器会持续降级为每个 Collector 独立的本地 GCRA。
*/

package trafficlimiter
