# 指标目录与资源丰富对齐：分阶段验证记录

- 日期：2026-10-30。
- 初始验证基线：`a6d1d9e9d214c7b798a1ea3205dc8998c0b441f1`。提交前快进到 `e98efbfaabb3e9cb08f558e02c0adcecc7e40b22`，本次丰富代码及测试的文件哈希保持一致，并重新执行受影响模块检查。
- 交付分支：`feat/linkd-dev`；本记录不表示已经部署。
- 环境：`test-bkee5`，租户 `system`。告警、Event、来源配置通过 `kingeye-admin` 只读获取；当前 `built_in_bk` 发布版本为 41。
- 验证方式：本地修改后的代码使用真实 MySQL / OneModel，按版本 41 的九个 Processor 配置进行内存重放；不向环境写告警、策略、目录或消息，不触发 Hook。
- 当前状态：三阶段修复已实现。用户已确认通用修复可以影响策略 207，不增加该策略的专项资源映射或策略 ID 特判。

## 1. 修复内容

### 阶段一：目录投影

- 从 `metric_object` 读取适用对象集合 `model_ids`，不以 `metric.model_id` 的 NULL 判断未关联，也不用旧派生值补集合。
- 单对象派生 `ObjectModelCode`，零个和多个对象时为空。无 ID 反查通过关系表验证成员资格；仍要求唯一结果，不任取第一条。
- 保留定义来源 `metricset_code`、实际取数来源 `ref_metricset_code` 和物化规则 `record_rule_id`。
- 保留租户、指标 ID、关系响应的精确复核与超时；关系读取有 8192 条上限，超限、重复、非法或跨租户关系失败。

新增回归先复现了丢失多对象、合法成员漏查、关系读取失败未报告等问题，修复后通过。

### 阶段二：目录合法状态和指标类型

- 已绑定的 `predefined` 可以使用有效物理主引用；未绑定或引用残缺时仍失败。
- 物化型 `derived` 保留结果表和字段；展开计算的 `derived` 保留父定义语义，不采用执行面的成员位置。规则 ID 非法、主引用与类型冲突时失败。
- 物化型计算指标有旧 `derived_metric` 标记时，名称更新为命中的目录定义，`metric_unique_id` 仍保留历史首查询兼容规则。
- 合法未关联定义在没有明确对象上下文时跳过资源推断，不报目录故障；有上下文时独立校验 OneModel。该规则统一覆盖 K8s 三种来源、`report.*` 等，不按策略 ID 特判。
- 目录不存在、读取失败、必填来源为空、JSON 非法等真实目录问题不被降级成跳过。

新增回归先复现了已绑定预定义指标失败、物化引用丢失和错误规则被接受等问题，修复后通过。

### 阶段三：资源选择与冲突

- 使用所有输入的非空适用对象集合；显式对象必须属于各集合，没有显式上下文时只能选择唯一公共模型。
- 标准身份可以来自当前 evaluation 的 `labels`、`extra_data` 或 `dimensions`，模型与实例必须在同一位置成对，重复声明必须一致。
- 策略、Event 和适用集合的冲突使用 `identity_conflict`，不改报 `metric_catalog` 故障，不发布冲突实例绑定。
- APM 优先识别目录实际取数来源，支持 `apm_application.<标识>` 和预定义指标绑定 APM；混合来源或不同单应用来源不选择首个输入。
- APM 应用、服务、服务实例遵守明确对象上下文和适用集合。接口仅接受经 OneModel 校验的完整标准身份，并核对应用/服务范围；不从 `span_name` 猜接口 ID。
- K8s 检查重复模型声明和显式实例身份，原生维度不能覆盖另一显式实例。

新增回归复现并覆盖了多对象误报、遗漏 labels/extra_data 身份、输入顺序影响资源、跨应用混合输入和 K8s 身份覆盖。

## 2. 每阶段使用的真实告警

| 阶段 | 告警 ID | Event ID | 结果与覆盖 |
| --- | --- | --- | --- |
| 一：目录投影 | `alert-20261030040008-hkzndoreuiize` | `event-20261030040949-pghepulo46fmk` | 九 Processor 整体 succeeded；内置 K8s 单对象集合与独立 SQL 快照一致 |
| 一：未关联集合 | `alert-20261028140715-m6zkbil3trobq` | `event-20261030014414-2btzdeu3b62iw` | 整体 succeeded；目录集合为空，monitor-resource skipped，k8s/metric succeeded |
| 二：指标类型回归 | `alert-20261030040008-hkzndoreuiize` | `event-20261030042248-qwvc2pcypsn2k` | 整体 succeeded；真实展开计算指标 `249` 的父定义和既有兼容输出保留 |
| 二：未关联定义回归 | `alert-20261028140715-m6zkbil3trobq` | `event-20261030045014-fuumh3ipbply4` | 最终代码重放 succeeded；monitor-resource skipped，k8s/metric succeeded |
| 二：report 未关联模型误报 | `alert-20261029180614-z5omi7vdps65i` | `event-20261030050014-n5hog2zvos25c` | 同一真实 Event 对照重放：monitor-resource 从 failed/metric_catalog 变为 skipped，整体从 partial 变为 succeeded；metric 始终 succeeded |
| 三：多对象 APM | `alert-20261028235215-pl5h4wqnhpfbi` | `event-20261028235714-62cyphqxkky3s` | 整体 succeeded；monitor-resource/apm/metric succeeded；目录 `10539` 的四对象集合与独立 SQL 快照一致 |

APM 样本取自历史告警窗口，接入版本为 38，重放使用当前发布的丰富版本 41。没有为本次验证生成新告警。

阶段证据：[目录投影](2026-10-30-metric-resource-alignment/stage1-projection.json)、[指标类型回归](2026-10-30-metric-resource-alignment/stage2-kinds.json)、[多对象资源](2026-10-30-metric-resource-alignment/stage3-models.json)。最终重放证据见[最终样本](2026-10-30-metric-resource-alignment/final-replay.json)。这些文件仅保留身份、目录投影、状态和必要指标字段，不含原始事件正文、连接配置或凭据。

## 3. 本地检查

| 命令 | 实际结果 |
| --- | --- |
| `go test ./internal/enrich/... -count=1` | 15 个包，3803 个测试通过；显式外部环境测试默认跳过 |
| `go test -race ./internal/enrich/... -count=1` | 15 个包，3803 个测试通过，无竞态报告 |
| `go vet ./internal/enrich/...` | 通过 |
| `golangci-lint run ./internal/enrich/...` | v2.13.2，0 issues |
| `git diff --check` | 通过 |
| `TestMetricResourceLiveReplay`，显式连接 test-bkee5 | 最终四种真实样本的九 Processor 链重放通过，见最终样本证据 |

沿用此前暂停 `make check` 的要求，没有执行完整仓库门禁。上述结果仅覆盖受影响的丰富模块，不能描述为整个仓库门禁通过。

## 4. 真实环境验证的边界

- 环境中有预定义指标 `6247/6248` 和物化型计算指标 `6632/10008/10011/11223`，但对当前租户全部策略历史的定向 JSON 查询没有找到这些指标 ID 的声明引用。同名历史字段不足以证明目录身份。见[发布引用查询结果](2026-10-30-metric-resource-alignment/publication-audit.json)。这两类新增分支有回归单测，没有真实对应告警重放，不将 K8s 衍生指标样本冒充该覆盖。
- `apm_application.*`、预定义指标绑定 APM、跨应用输入、接口标准身份和身份冲突的失败路径由单测覆盖；真实 APM 样本验证的是 `apm` 聚合来源的多对象路径。
- K8s 仍沿用既有维度字段，不将 `pod/container` 映射为 `pod_name/container_name`。PV/PVC 的自然键定位没有新增；缺少标准身份时仍保留相应资源诊断。
- 真实重放使用 MySQL 和 OneModel，没有装配 Redis 展示或动态分组 Reader，不声称覆盖这些补偿路径。生命周期、持久化、消息投递和部署后新告警不在本次重放中验证。
- 策略 207 的变化仅来自通用目录合法状态修复，没有修改其策略、指标定义或字段映射。[修复前](2026-10-30-metric-resource-alignment/report-before.json)与[修复后](2026-10-30-metric-resource-alignment/report-after.json)使用同一 Event、同一历史策略版本和相同外部依赖。

## 5. 代码与复测入口

- [目录 Reader](../../internal/enrich/datasources/metric_client.go)
- [指标类型与物理引用](../../internal/enrich/processors/metric.go)
- [资源模型与来源规则](../../internal/enrich/processors/metric_resource.go)
- [监控资源](../../internal/enrich/processors/monitor_resource.go)、[APM](../../internal/enrich/processors/apm.go)、[K8s](../../internal/enrich/processors/k8s.go)
- [显式只读重放测试](../../internal/enrich/assembly/metric_resource_live_test.go)
- [当前实现说明](../design/enrich.md#指标定义的数据源)

重放需要操作者显式提供三个文件路径环境变量：`LINKD_METRIC_RESOURCE_LIVE_CONNECTION_FILE`、`LINKD_METRIC_RESOURCE_LIVE_FIXTURE_FILE`、`LINKD_METRIC_RESOURCE_LIVE_REPORT_FILE`。连接只取个人环境配置的 `test-bkee5` 根；样本包含明确租户、已发布丰富版本、Admin 获取的告警/Event 关联和独立目录快照。连接与原始样本文件不得进入仓库。
