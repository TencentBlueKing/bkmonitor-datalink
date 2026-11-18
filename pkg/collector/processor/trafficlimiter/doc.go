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
它是唯一的真实额度，按量而不是按速率计算：bytes_per_second 是长期平均速率，容量是一瞬间最多能放行的量。
另有一张需求表，记录各 Collector 实例最近的到达速率与公平额度，只用于持续超限时的公平分配。
每个实例在本地为每个服务保存一份余额，余额来自向共享桶领取的租约。

## 请求路径

 1. 计费字节超过 burst_bytes 的请求直接拒绝（原因 exceeds_burst）：无论等多久都不可能被放行，不计入需求。
 2. 余额够就放行并扣减，绝大多数请求到此为止，不访问 Redis。
 3. 余额不够、且该服务最近的租约是足额授予的（说明共享桶里还有额度：限额之内的突增或冷启动），
    登记为等待者，合并成一次紧急补货，最多等 50ms。一个请求无论涉及多少服务都只等一次。
    补货写回时按到达顺序直接把额度扣给等待者，余额只代表空闲额度：下一轮申请、需求下降时的退还、
    后到的请求都拿不走已经许给等待者的那部分。
 4. 没被兑现、或该服务正在退避时，在透支上限之内放行；超出上限的请求被拒，且不扣余额、不入池。

## 补货泵

每个实例一个 goroutine，同一时刻至多一个 Redis 调用在途，调用期间到达的申请并入下一轮：

  - 定时同步：每 500ms（±25% 随机，让各实例的先后顺序每轮不同），结算各服务上一周期的需求，
    补齐预取（约 0.75s 的需求量，至多 C / (2 × 存活实例数)，冷启动还不知道实例数时不预取），
    补回透支欠账，退还超过预取目标两倍的余额，并上报需求；
  - 异步补货：余额低于预取目标的 1/4 时提交，两次之间至少 20ms；
  - 紧急补货：见请求路径第 3 步，不受间隔限制；
  - 需求下降扫描：每 100ms 扫描一次内存，需求明显下降的服务立即退还多余的余额并上报新需求。
    下降的实例若等到下一次定时同步，它压着的额度与过时的需求会让突增的实例在限额之内被拒。

每次申请分成两部分：必须满足的部分（等待中请求的真实需要 + 透支欠账，这些字节已经到达或已经放行）
与投机性的预取。租约不足额后的退避分两种：额度真正紧张、或连必须满足的部分都没领到时，
之后的请求不再等待（本实例超过公平份额时 100ms，否则按桶补回缺口所需的时长，20ms 到 500ms）；
只是预取没领满时，只暂停异步预取，不挡住真实请求的等待。共享桶空了不会被反复去取。

## 分配与透支

Lua 按共享桶的实际存量分三种情况授予，而不是只看上报的需求速率：
突发会让 100ms 窗口里的瞬时速率虚高，刚退出的实例的需求也要 2s 才过期，只看速率会把「桶还满着」误判成争抢。

  - 上报的总需求不超过 L：申请多少给多少；
  - 总需求超过 L，但桶中可用量不少于一半，或跌破一半还不到一个同步周期：多为突发或过时的需求。
    必须满足的部分先到先得，与理想的全局令牌桶一致；预取只能用扣掉它之后超出半个桶的部分，
    不挤占同一时刻其他实例真实到达的请求；
  - 可用量持续一个同步周期以上不足一半：持续超限。按 max-min 算出各实例的公平速率 f，每个实例的公平额度
    按 f 累积、至多 2 个周期，只有正向授予才扣减；授予量受 max(公平额度, 桶中可用量 − 其他实例公平份额的预取)
    封顶：需求低于水位的实例拿满需求，先同步的实例拿不走别人那份，不会饿死；桶里有富余时也不会白白浪费。

需求下降最多 200ms 后才被发现，这段时间里突增的实例可能领不到足额。为此共享模式下每个服务在每个实例上
可以透支 min(L × 200ms, C / 存活实例数)，欠账从之后的租约中扣回。大请求不能使用超出剩余透支空间的部分，
不会出现一个大请求挖坑、后面的请求长时间去填。

## 保证

  - 任意时长 t 内全局放行不超过 2C + L × t（一份来自共享桶，一份来自各实例透支之和），之后稳定在 L；
  - 限额之内的负载均衡转移与突增不丢数据，突增的请求至多多等一次 Redis 往返；
  - 流量瞬时到达（宏观平均不变）时与理想的全局令牌桶一致：只有多个实例在同一瞬间突发、且平均负载达到额度时，
    才会因各实例的预取与欠账比理想桶多拒约 1%；
  - 发布切流不丢数据：实例正常退出时（Collector 停止时经 processor.Stopper 调用）归还预取的额度并删除需求记录；
    即使直接退出不归还，接手的实例也会按先到先得领到桶里的额度；
  - 持续超限时总量稳定在 L，各实例按 max-min 公平分配；
  - Redis 抖动时限额之内不丢数据，Redis 不可用时总量至多约 2L（见下）。

## Redis 变慢与不可用

等待补货超过 50ms、或一轮租约成功但超过 50ms 时，视为 Redis 变慢：请求不再等待，仍按共享额度记账，
余额不够时先欠着放行，上限是最近一次租约之后共享桶的可用量加上之后按速率补回的量（不超过容量）。
限额之内的额度本来就在桶里，补货回来就把欠账还上，Redis 抖动几十毫秒不会让突增或切流被拒。
一轮成功且耗时不超过 25ms 后恢复等待。

租约出错、或距最近一次同步成功超过 1s 时，视为不可用，改由本地降级：每个服务按
max(最近的公平份额, L / 存活实例数) 在本地补充余额。前者让总量大体不变，后者让故障期间流量转移过来的实例
不会被卡在旧份额上。定时同步继续上报需求并探测，Redis 恢复应答后先进入变慢，足够快时恢复正常。

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
