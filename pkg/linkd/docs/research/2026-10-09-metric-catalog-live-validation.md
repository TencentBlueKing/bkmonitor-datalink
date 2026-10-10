# 指标目录替换：真实活跃告警验证

验证日期：2026-10-09（Asia/Shanghai）。环境为个人配置中明确选定的 `test-bkee5` 根，蓝鲸 5.3 测试环境。Linkd 基线提交 `25848e3d6e4d2fbc3dea9c07d46f3662ded23df8` 加本次未提交的指标目录替换；对照 Kingeye 本地源码提交 `3c4744f2e0`，不能据此推断部署服务的源码版本。

## 范围与取证

- 查询同租户 `system` 的 `linkd-test2-alerts-active`，过滤 `status=active`、`event_source_id=alarmd`；查询时共有 402 条。
- 按策略分组，每组只取最新活动样本，再按 `latest_event_id + bk_tenant_id` 定向读取 `linkd-test2-events`。没有修改事件标签、版本、维度或策略配置来制造匹配。
- 核对 `alarm_strategy_set_split_record.id + bk_tenant_id + source_resource_version`，不使用 `legacy_strategy_id` 代替拆分策略主键；没有当前版本发布材料的样本不进入本轮重放，也不替换成最新配置。
- 选出 22 个不同策略的活跃告警，其最新活动时间在 `2026-10-08T17:44:00Z` 至 `2026-10-09T14:17:00Z`，共 26 个等级求值。
- 用真实 MySQL Reader、真实 OneModel ES Client，在内存中执行 Strategy → Resource → Display → Metric。没有装配其他 Processor，没有写回告警、发布材料、Redis、Kafka offset，也没有部署新服务。
- 独立只读 SQL 导出同租户的 2,813 条 `metric` 定义为有界快照；在内存中核对候选唯一性、身份和字段映射，与真实 Go Reader 输出比较。真实命中的单位同时核对 Metric 输出。原始事件、快照和含密配置仅保存在权限受限的临时目录，未纳入仓库。

## 最终结果

| 样本策略 | 指标或场景 | 告警数 | 四个 Processor 的结果 |
|---|---|---:|---|
| 35、3 | `system.disk` 的 `in_use/total` | 2 | 全部成功；展示名为磁盘空间使用率/磁盘总空间大小，单位为 percent/bytes |
| 11、2、7、27、13、16、22 | `system.cpu_summary` 的 `usage/idle` | 7 | 全部成功；名称、单位、维度定义与目录快照一致 |
| 5、10、83、12 | 同上 | 4 | 指标读取、Display、Metric 成功；Resource 因缺少主机身份维度而 partial |
| 48、47 | APM `bk_apm_count` | 2 | 全部成功；无模型限制的物理引用唯一命中；空单位为目录真实值 |
| 94 | `metric_ref_id=2162`，`disk_usage` | 1 | 全部成功；只按租户与主键读取，目录记录为 native、业务空间 `bkcc__2` |
| 24、40、44、67 | 日志关键字，无目录 ID/物理表 | 4 | 修复后全部成功；不发起不适用的目录查询 |
| 18、73 | 数据集 `cpu_usage`，无目录 ID/物理表 | 2 | Strategy、Resource 成功；Display、Metric 为 dependency_invalid(metric_catalog) |

按告警统计：16 条 succeeded、6 条 partial。按 26 个等级统计：Strategy 26 成功；Resource 22 成功、4 partial；Display 和 Metric 各 24 成功、2 failed。16 次成功的目录读取全部与独立 SQL 快照一致；多等级共用 Scope 缓存，没有重复读取同一定义。

部分可定向回查的 Alert ID：

| 策略 | Alert ID |
|---|---|
| 35 | `20260930005230.system.alarmd.f00907c6c9904968` |
| 48 | `20261009135330.system.alarmd.704496973a33caeb` |
| 94 | `20261009063830.system.alarmd.26e048adf72bd141` |
| 24 | `20261008220031.system.alarmd.629880b93d358cbf` |
| 18 | `20260930094931.system.alarmd.e37c92da1d1fcc0f` |

这些 ID 只定位本次采样，后续告警状态、最新事件和策略发布版本可能变化。

## 本轮发现与修复

第一轮 4 条日志关键字告警的 Display 失败：其发布查询没有物理表或目录 ID，新 Reader 的完整引用校验拒绝了旧 Display 不加区分的元数据请求。现在日志无目录引用时保留日志配置与原始维度；日志显式引用的目录错误仍然传播。

回归测试 `TestLogDisplayWithoutCatalogReference` 在修复前的 log/log_keyword 两个场景均失败（partial，目录请求一次）；修复后通过，并验证显式 ID 的查询失败仍产生 partial。重新只读重放后，4 条真实日志告警的 Display 成功。

策略 18/73 的发布配置仍是 `data_source=数据集`、空 `result_table_id`、无 `metric_ref_id`，同租户目录中也没有 `metric_name=cpu_usage`。本次不按名称猜测其他目录行或读取旧 MetricLibrary。应在上游发布材料中补齐有效目录 ID 或物理引用，再验证这两条策略。

## 可复现入口与限制

集成测试入口为 [metric_catalog_live_test.go](../../internal/enrich/assembly/metric_catalog_live_test.go)。显式提供以下环境变量；缺少时普通测试跳过，文件大小、样本数、目录行数与执行时间均有硬上限：

```text
LINKD_METRIC_CATALOG_LIVE_CONNECTION_FILE
LINKD_METRIC_CATALOG_LIVE_FIXTURE_FILE
LINKD_METRIC_CATALOG_LIVE_REPORT_FILE
```

连接文件 JSON 为 `mysql`（MySQLConfig 字段）与 `es_address`；fixture 为 `Samples`（实际 `alert/event`）和 `Catalog`（独立 SQL 导出后映射为 liveCatalogRow）。测试校验活跃状态、同租户最新事件绑定、精确发布版本、目录映射、单位和输入不变。Report 保留状态、指标定义与查询，不输出完整事件或连接信息，写入权限 0600。

执行命令：

```bash
go test -race ./internal/enrich/assembly -run '^TestMetricCatalogLiveActiveEvents$' -count=1 -v
go test ./internal/enrich/... -count=1
```

上述两个命令通过。完整门禁使用 `NODE_OPTIONS=--no-experimental-webstorage make check`（本机 Node 26 的 Web Storage 环境约束）：格式、Go 普通测试、vet、竞态、golangci-lint 和控制台 lint/类型检查/测试/构建均通过；控制台 420 个测试通过、7 个跳过。随后 `helm-check` 因本机缺少 Helm 失败，后续 release-check 未由本轮完整门禁执行。受影响文档的 28 个本地链接与锚点、`git diff --check` 通过。

真实读取和内存重放不等于已经部署新 Reader，也不证明 Kafka/Redis 生命周期或所有 Processor 的端到端行为。本轮未覆盖真实衍生指标、同引用多义、跨租户碰撞、K8s、拨测及云指标；相关身份、歧义和取消边界由本地测试覆盖。

现行数据源规则见 [Enrich 指标定义](../design/enrich.md#指标定义的数据源)。

## 2026-10-10 交付前复验

工作树 rebase 到 `feat/linkd-dev` 的 `95a586e2233f7af36e3daa941dbd7195767ec27c`，原有改动恢复时没有文本冲突。本轮完整 `make check` 通过：Go 格式、普通测试、vet、竞态、静态分析，控制台 lint/类型检查/测试/构建，Helm lint/31 个 Chart 测试和发布脚本测试。控制台 431 个测试通过、8 个跳过；受影响文档 65 个本地链接与锚点通过。

验证使用临时工具目录中的 CI 固定 Helm 3.17.3、已有 Python 3.11，以及本机 Node 的 `NODE_OPTIONS=--no-experimental-webstorage`；没有更改门禁脚本或测试断言。此次复验没有重新连接 test-bkee5 执行线上重放，前述真实告警结果仍以 2026-10-09 采样为准。
