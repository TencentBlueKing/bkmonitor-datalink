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

## 结构

Redis 中每个服务一个共享 GCRA 桶（速率 L = bytes_per_second，容量 C = max(burst_bytes, L × 1s)），
它是唯一的真实额度；另有一张需求表，记录各 Collector 实例最近的到达速率，只用于额度紧张时的公平分配。
每个实例在本地为每个服务保存一份余额，余额来自向共享桶领取的租约。

## 请求路径

 1. 计费字节超过 burst_bytes 的请求直接拒绝（原因 exceeds_burst）：无论等多久都不可能被放行，不计入需求。
 2. 余额够就放行并扣减，绝大多数请求到此为止，不访问 Redis。
 3. 余额不够、且该服务最近的租约是足额授予的（说明共享桶里还有额度：限额之内的突增或冷启动），
    合并成一次紧急补货，最多等 50ms，补货回来后重新判断。一个请求无论涉及多少服务都只等一次。
 4. 仍不够、或该服务正在退避时，在透支上限之内放行；超出上限的请求被拒，且不扣余额、不入池。

## 补货泵

每个实例一个 goroutine，同一时刻至多一个 Redis 调用在途，调用期间到达的申请并入下一轮：

  - 定时同步：每 500ms（±25% 随机，让各实例的先后顺序每轮不同），结算各服务上一周期的需求，
    补齐约 0.75s 需求量的预取，退还超过预取目标两倍的余额，并上报需求；
  - 异步补货：余额低于预取目标的 1/4 时提交，两次之间至少 20ms；
  - 紧急补货：见请求路径第 3 步，不受间隔限制；
  - 需求下降扫描：每 100ms 扫描一次内存，需求明显下降的服务立即退还多余的余额并上报新需求。
    下降的实例若等到下一次定时同步，它压着的额度与过时的需求会让突增的实例在限额之内被拒。

一次租约不足额后，该服务退避一段时间，期间不再访问 Redis：本实例已超过公平份额时退避 100ms；
只是共享桶暂时被取空时，按桶补回缺口所需的时长退避（20ms 到 500ms）。共享桶空了不会被反复去取。

## 公平与透支

总需求不超过 L 时，租约要多少给多少；超过时按 max-min 算出各实例的公平速率 f，授予量受
max(f × 距上次租约的时长, 桶中可用量 − 其他实例公平份额的预取) 封顶：需求低于水位的实例拿满需求，
先同步的实例拿不走别人那份，不会饿死；桶里有富余时也不会白白浪费。

需求下降最多 200ms 后才被发现，这段时间里突增的实例可能领不到足额。为此共享模式下每个服务在每个实例上
可以透支 min(L × 200ms, C / 存活实例数)，欠账从之后的租约中扣回。大请求不能使用超出剩余透支空间的部分，
不会出现一个大请求挖坑、后面的请求长时间去填。

## 保证

  - 任意时长 t 内全局放行不超过 2C + L × t（一份来自共享桶，一份来自各实例透支之和），之后稳定在 L；
  - 限额之内的负载均衡转移与突增不丢数据，突增的请求至多多等一次 Redis 往返；
  - 持续超限时总量稳定在 L，各实例按 max-min 公平分配；
  - Redis 不可用时总量至多约 2L（见下）。

## Redis 不可用

租约失败、或一次往返超过 50ms 时，关闭等待与按需补货，改由本地降级：每个服务按
max(最近的公平份额, L / 存活实例数) 在本地补充余额。前者让总量大体不变，后者让故障期间流量转移过来的实例
不会被卡在旧份额上。定时同步继续上报需求并探测，一轮成功且耗时不超过 25ms 后立即恢复共享。
距最近一次同步成功超过 1s 同样视为不可用。

实例 ID（主机名 + 随机后缀，进程启动时生成）只用在需求表里区分各实例，2s 不上报即视为离开。

## 配置与降级

redis 段可以整体省略，此时处理器空转，效果等同于额度为 0。

Factory 在任何情况下都不返回错误：Processor 构建失败会让整条 Pipeline 建不起来，
该信号的全部请求随后被判为 400。限流是保护性功能，配置问题只降级、绝不中断数据通路。
redis 段非法时保留额度语义但退化为每实例独立限流。

## 指标

traffic_limiter_bytes_total 看谁被限了；traffic_limiter_rejected_total 看被拒的原因（quota / exceeds_burst）；
traffic_limiter_decisions_total 看判断时用的是共享额度还是本地降级（缺了它，Redis 故障后额度放大在字节指标上
完全看不出来）；traffic_limiter_lease_wait_total 看突增时等待补货的结果；traffic_limiter_sync_total 看租约调用
是否成功；traffic_limiter_mode 看当前配置处于 disabled / local_only / shared 哪种模式。

Redis 侧的脚本使用 TIME 统一各实例的时间源，要求 Redis 5 及以上版本。
更低版本的脚本会直接报错，租约持续失败，限流器停留在本地降级。
*/

package trafficlimiter
