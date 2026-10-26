# 活跃告警缓存与软限流本地验证

日期：2026-10-26。源码为基于 `9978a73fa` 的当前工作区，包含未提交的软限流实现；不作为已发布版本。
环境：Apple M4 Pro、darwin/arm64、Go 1.26.10、golangci-lint 2.13.2、临时独立 Redis 8.0.0。没有使用业务 Redis。
功能配置与运行边界见[使用指南](../guides/active-alert-by-strategy.md)，设计见[活跃缓存](../design/active-alert-index.md)。

## 性能样本

以下是单次本地微基准，批次为 128 条，数量缓存预热、窗口为 1 秒。只测 Checker，不包含 Event 写入、MQ、
Enrich 或完整生命周期，不代表生产吞吐和端到端延迟。不同场景运行时负载不同，不推导稳定的性能上界。

| 路径 | 平均每批耗时 | 折合每条耗时 | Redis 命令/条 |
| --- | --- | --- | --- |
| 关闭检查入口 | 0.172µs | 0.0013µs | 0 |
| 热点分组未满 | 13.744µs | 0.1074µs | 0 |
| 满额、新 fingerprint | 94.304µs | 0.7368µs | 0.007812 |
| 满额、已有 fingerprint | 44.161µs | 0.3450µs | 0.007812 |

0.007812 约等于每 128 条一条 SMISMEMBER 命令，共一次往返；仅查询当前满额分组，相同 fingerprint 会去重。
未满场景的 0 命令仅指本次数量缓存窗口内；过期后按分组批量读取 SCARD。不再读取就绪标记、成员 Hash 或其他辅助 key。
实际 Runtime 对未配置或关闭拦截的来源不装配 Checker；表中第一行是直接调用关闭入口的对照组。
主流程仅在活跃成员变化时写缓存，持续触发不额外写 Redis。Checker 不持有数据库接口，不增加逐事件数据库读取。

热路径避免生成通知 JSON 和诊断 map，满额才构建限流诊断。完整原始输出保存在
[benchmark 输出](benchmarks/2026-10-26-active-alert-soft-limit.txt)。复现：

```bash
LINKD_TEST_REDIS_ADDRESS=127.0.0.1:16397 go test ./internal/activeindex \
  -run '^$' -bench 'Benchmark(Redis)?Checker$' -benchmem -benchtime=500ms -count=1
```

复现时使用独立临时 Redis；集成用例只清理自身随机部署/前缀，不使用 FLUSHDB。

## 验证边界

普通测试和 race 覆盖配置、控制面补齐及不重复发版、已有规则/显式关闭、Provider 共用发布入口、
Cleaner 诊断与重投、创建/持续触发/等级轮换/恢复的缓存观察、历史旧配置可读且可替换但不能重新发布、Redis 分组 Set 与通知、多规则、联合分组、租户/来源隔离、
缓存重建、终态残留、扫描期间新增成员及失败保留。断言仅有规则分组 Set，无辅助 key；修复复用已有 fingerprint 锁，锁忙留待下轮。
MySQL/Elasticsearch 的存储读取单元测试覆盖窄投影、原始标签、分页、字节预算、PIT 清理与部分结果拒绝；
其真实集成用例需要独立连接，未提供连接时明确跳过。本轮没有进行真实 MySQL/Elasticsearch 联调、
alarmd 接入、线上吞吐测试、提交、镜像发布或部署。

完整门禁使用 `make fmt` 后执行 `make check`，包含普通测试、go vet、race、golangci-lint、Helm 和发布脚本检查。
本地完整门禁通过不代表部署验收通过。

本轮最终 `make check` 返回 0，静态分析为 `0 issues`；完整输出保存在
[门禁原始输出](benchmarks/2026-10-26-active-alert-soft-limit-check.txt)。受影响的新增/改写本地链接与锚点检查无新增错误；指南目录中原有的 `version-upgrade-and-deployment.md` 链接目标不存在，本轮未修改该链接。
