# KAC 输出 V3 本地验收

日期：2026-10-14。范围：Linkd 当前工作区与 Kingeye 配套 V3 接收端；尚未提交或部署到生产。

## 实现范围

普通流程保存 Linkd Alert 后只发布轻量 Redis Streams 消息。控制面批量读取最新 Alert、创建或局部更新 KAC，独立刷新及并发发送动作。
主动关闭共用投影通道并优先执行，直接 `refresh=true`，失败返回 502 且保留 Linkd 关闭事实，原命令可重试。
默认 64 投影通道、500 条/10ms、4MiB 请求、64 动作并发、10,000 条缓冲。读取连接预算随投影通道增加，避免仍受原查询池 8 个连接限制。

删除独立任务存储、补偿扫描、ActionPending、快照/hash/水位/Receipt、相关 API 与页面。生命周期业务 CAS、策略裁决和审计保留。
两端采用 V3 配套部署；旧任务数据不迁移、不自动删除。行为权威位置为[动作 V3](../reference/contracts/kac-action-delivery-v3.md)和[投影契约](../reference/contracts/kac-alert-projection-v1.md)。

## 正确性与联调

- Linkd 单元和真实 ES 用例覆盖新建、更新、恢复、同步关闭、屏蔽解除、合并解联、KAC 处置字段与标签保留、rollover 后更新原文档、租户隔离、部分失败和请求字节拆批。
- 并发用例覆盖重复通知合并且动作独立保留、同通道关闭、取消、动作缓冲满与优雅退出；竞态检测通过。
- Kingeye 受影响测试共 82 项通过，其中真实联调用 Go 写 KAC ES，再由 Go V3 发送器调用 HTTP 接收端、投递真实 Redis Celery、执行 alarm_process，核对 MySQL 终态操作与 ES 审计。
- 联调隔离许可与订阅通知外部副作用；未真实发送通知、创建生产工单或运行生产自愈。HTTP 测试服务使用生产 view 和 JWT 校验，不包含部署网关。
- Console 相关浏览器用例 3 项通过；首次误复用本机另一服务的 5173 端口返回 Not Found，改用独占 5199 端口后通过。

真实服务：本地 Elasticsearch 7.17.7（4 CPU/4GiB 容器）、Redis 及独立临时 MySQL 8。测试资源均使用随机测试前缀并只清理自身。

## 5 万条突发压测

执行 `TestRedisElasticsearchBurst`，使用真实 Linkd ES Repository、实时批量读取、Redis Stream 与 KAC ES 写入。
预先保存并入队 50,000 条独立轻量告警；从消费者启动开始计时，不包含数据准备与发布耗时。
默认 64 通道、500 条/10ms、4MiB；每条告警为精简字段样本，未附大体积丰富数据。

| 指标 | 首次 | 资源补测 |
| --- | ---: | ---: |
| 投影清空耗时 | 2.565 秒 | 2.383 秒 |
| 投影吞吐 | 19,491 条/秒 | 20,978 条/秒 |
| 从消费者启动到完成的 P95 | 2.480 秒 | 2.345 秒 |
| 从消费者启动到完成的 P99 | 2.518 秒 | 2.371 秒 |
| 完成后 Go HeapAlloc（非峰值） | 32.0 MiB | 51.1 MiB |

资源补测通过独立编译的测试二进制配合 `/usr/bin/time -l` 记录：全测试 7.83 秒，CPU user 17.58 秒、system 2.21 秒，峰值 RSS 378,011,648 字节（约 360.5 MiB）。
这些进程资源包含源数据准备、两组 ES 用例、突发及重启测试；不是仅投影阶段，也不包含 ES/Redis 服务端资源。
此本机样本不能外推为复杂丰富数据或生产集群的容量承诺。

## 慢动作与优雅重启

将动作发送器阻塞后，另 5,000 条真实投影约 0.403 秒完成，投影没有等待动作接口。
停止消费时释放已开始的 64 个动作调用；运行器等待其完成并提交消费确认，剩余 4,936 条保留在 Stream PEL。
新运行器启动后处理余下消息，累计动作 5,000 条，无丢失或重复。这里的慢动作发送器是可控测试实现，真实 KAC 接收及 Celery 在上一节独立验证。

停止信号不取消在途 I/O，已开始任务在现有 10 秒超时内结束；未开始工作不启动，保留消息供重启恢复。
HTTP 服务等待在途关闭请求的预算为 30 秒，Helm 默认退出宽限 60 秒。强制杀进程和网络确认丢失仍可能重复，未承诺 exactly-once。

## 验证入口

```bash
make check
LINKD_TEST_ELASTICSEARCH_URL=<测试 ES> go test ./internal/kaccompat -run TestElasticsearch -v
LINKD_TEST_ELASTICSEARCH_URL=<测试 ES> LINKD_TEST_REDIS_ADDRESS=<测试 Redis> \
  LINKD_TEST_KAC_BURST=50000 go test ./internal/kaccompat -run TestRedisElasticsearchBurst -v -timeout 5m
cd console
LINKD_CONSOLE_E2E_PORT=5199 pnpm exec playwright test \
  tests/e2e/kac-output.spec.ts tests/e2e/alert-policy.spec.ts tests/e2e/policy-diagnostics.spec.ts
```

Kingeye 按仓库 `task dev-test` 入口执行 `test_action_protocol.py`、`test_integration_boundaries.py`、`test_processing.py`、`test_close_audit.py`、`test_action_live_integration.py`。
真实联调需显式设置 `KAC_LINKD_V3_LIVE=1`、`KAC_LINKD_V3_REDIS`、`KAC_LINKD_V3_SOURCE` 和 `LINKD_TEST_ELASTICSEARCH_URL`，数据库名称必须以 `linkd_kac_v3_` 开头。
Kingeye Admin `pnpm check` 通过（531 项测试），Python Ruff 通过，文档生命周期及受影响本地链接/锚点检查通过。
Linkd `make check` 包含格式、Go 测试、vet、race、lint、Console 测试及构建、Helm 与发布脚本检查，全部通过。
