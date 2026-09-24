# 作业参数说明

本文对应当前 `rum-compute` 的应用启动参数，默认值以源码为准。Session 和 View 当前均为：**空闲 15 分钟关闭、最大生命周期 4 小时、首次关闭后保留 2 小时用于修正**。

参数读取入口：[RumPrecomputeJob](../../src/main/java/com/tencent/bk/bkmonitor/rum/RumPrecomputeJob.java)、[RumJobConfig](../../src/main/java/com/tencent/bk/bkmonitor/rum/RumJobConfig.java) 和 [PrecalculateElasticsearchSink](../../src/main/java/com/tencent/bk/bkmonitor/rum/sink/PrecalculateElasticsearchSink.java)。本文不列举 Flink 集群自身的所有配置；状态后端、存储、内存、slot 和部署参数见 [部署](operation.md)。

## 1. 配置加载与作业

优先级从高到低：**命令行参数 → JSON 配置文件 → 代码默认值**。

- 命令行使用 `--key value`，例如 `--session.state.ttl.minutes 120`。
- `--config <path>` 读取扁平 JSON 对象；所有值必须是字符串，包括数值和布尔值，如 `"120"`、`"false"`。不支持嵌套对象、数组、JSON 数字或 JSON 布尔值。
- 文件扩展名不影响解析；即使命名为 `submit.ini`，内容也必须是 JSON。
- 配置文件必须能被执行 `main` 的进程读取。Web UI / REST 提交时，需要放在 JobManager 可读取的位置。
- 本文数值参数均使用整数，单位由参数名决定；不能填写 `2h`、`15m` 之类的时长表达式。
- 枚举参数会去除两端空白并忽略大小写；布尔参数使用 `true` / `false`。
- 参数在启动时加载，没有动态刷新。添加源码未读取的参数不会自动获得相应功能，也不保证报告未知参数错误。

| 参数 | 默认值 | 类型 / 约束 | 说明 |
| --- | --- | --- | --- |
| `config` | 不读取文件 | 文件路径，可选 | 仅通过 `--config <path>` 指定；同名命令行参数覆盖文件内容 |
| `job.name` | `rum-session-precompute` | 非空字符串 | Flink 作业名称；修改它不会自动修改消费组名称 |
| `parallelism` | `4` | 正整数 | 作业显式设置的并行度，调整时使用本参数，并准备足够的集群 slot |

## 2. Kafka 输入

| 参数 | 默认值 | 单位 / 约束 | 说明 |
| --- | --- | --- | --- |
| `kafka.bootstrap.servers` | **必填** | 非空字符串 | Broker 地址，可逗号分隔，如 `kafka-1:9092,kafka-2:9092` |
| `kafka.topic` | **必填** | 非空字符串 | 单个输入 topic 名称；当前入口不把逗号列表拆成多个 topic |
| `kafka.group.id` | `rum-session-precompute` | 非空字符串 | Kafka 消费组；`committed` 模式读取该消费组的位点 |
| `kafka.startup.mode` | `latest` | `latest` / `earliest` / `committed` | 无恢复状态时的起始位点，具体行为见下表 |
| `kafka.fetch.min.bytes` | `1048576`（1 MiB） | 字节，正整数 | 拉取请求希望积累的最小数据量；增大有利于攒批，也可能增加低流量时的等待 |
| `kafka.fetch.max.wait.ms` | `500` | 毫秒，正整数 | 未达到最小拉取量时，Broker 等待凑批的最长时间 |
| `kafka.max.partition.fetch.bytes` | `16777216`（16 MiB） | 字节，正整数 | 单分区拉取数据量配置，按 Kafka 对批次大小的规则生效 |
| `kafka.max.poll.records` | `5000` | 条，正整数 | 单次 `poll` 最多返回的记录数 |
| `kafka.receive.buffer.bytes` | `262144`（256 KiB） | 字节，正整数 | Consumer 网络接收缓冲区大小 |
| `kafka.connections.max.idle.ms` | `540000`（9 分钟） | 毫秒，正整数 | Kafka 连接的空闲回收时长，不是 Session/View 的空闲关闭时间 |
| `kafka.partition.discovery.interval.ms` | `300000`（5 分钟） | 毫秒，正整数 | KafkaSource 发现新增分区的间隔；本应用不接受用零或负数禁用 |

| 启动模式 | 没有 Checkpoint/Savepoint 恢复状态时的行为 |
| --- | --- |
| `latest` | 从启动时取得的最新位点读取，不回放之前积压的数据 |
| `earliest` | 从 Kafka 当前仍保留的最早可用位点读取 |
| `committed` | 从消费组已提交位点读取；没有可用位点时回退到最早可用位点 |

从 Checkpoint/Savepoint 恢复时，Source 使用恢复状态中的位点。修改 `kafka.startup.mode` 不是强制重置已恢复位点的方法。

上述拉取参数只传给输入 KafkaSource；任意额外 `kafka.*` 参数不会自动透传，迟到审计 Kafka Sink 也不会继承这些拉取参数。数值最终还受 Kafka 客户端自身的类型和范围校验约束。

## 3. Watermark 与乱序

| 参数 | 默认值 | 单位 / 约束 | 说明 |
| --- | --- | --- | --- |
| `watermark.out-of-orderness.seconds` | `120`（2 分钟） | 秒，非负整数 | 有界乱序 Watermark 的滞后时长；不是事件接收时长的硬上限 |
| `watermark.idle.minutes` | `1` | 分钟，正整数 | 空闲分区检测时长，避免没有数据的分区一直阻碍 Watermark 推进 |

Session/View 按**处理时间**关闭。Watermark 不触发这些窗口的空闲关闭或最大生命周期关闭，也不会让每条事件先等待 2 分钟再聚合。

活动窗口或闭窗状态仍保留时，未处理过的有效事件可以参与聚合，即使其事件时间落后于 Watermark。只有窗口已清理且清理标记仍在时，才按 Watermark 拒绝极迟事件。完整状态规则见 [窗口与状态](architecture.md#窗口与状态)。

## 4. Session 与 View 窗口

| 参数 | 默认值 | 单位 / 约束 | 说明 |
| --- | --- | --- | --- |
| `session.emit.interval.seconds` | `30` | 秒，正整数 | Session 活动窗口有变化时的增量输出间隔 |
| `session.gap.minutes` | `15` | 分钟，正整数 | Session 连续没有新的有效事件时关闭 |
| `session.max.life.minutes` | `240`（4 小时） | 分钟，正整数 | 从 Session 窗口创建起计算的最大活动时长 |
| `session.state.ttl.minutes` | `120`（2 小时） | 分钟，正整数 | Session 首次关闭后的聚合状态和事件去重状态保留期 |
| `session.max.string.chars` | `512` | Java 字符长度，正整数 | 限制部分 Session 展示字段长度；应用名和实体 ID 保留原值 |
| `view.emit.interval.seconds` | `30` | 秒，正整数 | View 活动窗口有变化时的增量输出间隔 |
| `view.gap.minutes` | `15` | 分钟，正整数 | View 连续没有新的有效事件时关闭 |
| `view.max.life.minutes` | `240`（4 小时） | 分钟，正整数 | 从 View 窗口创建起计算的最大活动时长 |
| `view.state.ttl.minutes` | `120`（2 小时） | 分钟，正整数 | View 首次关闭后的聚合状态和事件去重状态保留期 |

窗口时长换算为毫秒时会检查溢出。TTL 独立于 gap 和 max-life，可以比它们短，不会自动随最大生命周期放大。

### 关闭与 TTL 的关系

- gap 到期、max-life 到期或对应 SDK 的 `session.phase=end` / `view.phase=end` 都可关闭窗口；业务结束事件立即关闭。
- 已去重的重复事件不重新累计，也不刷新活动窗口的 gap。
- TTL 从**首次关闭时刻**开始计算；保留期内每个未见过的有效事件会立即产生关闭快照的修正，不受活动窗口 emit 间隔节流。
- 修正沿用原 `window_id`、ES 文档 ID、关闭时间和关闭原因，也不会延长 TTL。
- TTL 到期后清理聚合和去重状态，并最长再保留同等时长的清理标记；标记期间允许的新窗口会清除旧标记。
- 状态清理后不能继续修正原窗口。重新创建窗口时使用新的 `window_id`。

例如采用默认值、窗口在 10:00 首次关闭：聚合和去重状态的清理截止时间为 12:00，随后清理标记最多保留到 14:00。后两小时只保留标记，不再保留原窗口的聚合结果。

这里的 TTL 是作业通过 processing-time timer 实现的保留期，不是 Flink 集群级 State TTL 开关，也不是“所有事件最多可迟到两小时”的承诺。

## 5. Checkpoint

| 参数 | 默认值 | 单位 / 约束 | 说明 |
| --- | --- | --- | --- |
| `checkpoint.interval.ms` | `60000`（1 分钟） | 毫秒，至少 `10` | Checkpoint 调度间隔；本应用要求正数，Flink 2.2.1 另要求至少 10 ms |
| `checkpoint.timeout.ms` | `600000`（10 分钟） | 毫秒，至少 `10` | 单次 Checkpoint 允许的最长执行时间 |
| `checkpoint.tolerable-failures` | `3` | 次，非负整数 | Flink Checkpoint 失败管理的容忍次数；`0` 表示不容忍，不是作业重启次数 |
| `checkpoint.min-pause.ms` | `30000`（30 秒） | 毫秒，非负整数 | 下一次 Checkpoint 触发前的最小暂停；`0` 表示不额外暂停 |

入口固定启用 Flink `EXACTLY_ONCE` Checkpoint 模式。ES Sink 和迟到 Kafka Sink 使用 `AT_LEAST_ONCE` 投递，不能据此宣称整个外部存储链路具有 exactly-once 结果保证。

状态后端、Checkpoint 存储目录、HA 和重启策略由 Flink 集群配置管理，不通过上述四个应用参数设置。恢复兼容性见 [恢复与升级](architecture.md#恢复与升级)。

## 6. 输出目标与迟到审计

| 参数 | 默认值 | 类型 / 约束 | 说明 |
| --- | --- | --- | --- |
| `sink.type` | `es` | `es` / `elasticsearch` / `stdout` | Session 和 View 的主输出目标；前两个值等价 |
| `late.sink.type` | `kafka` | `kafka` / `stdout` / `none` | Session 审计侧输出的目标；`none` 不注册审计 Sink |
| `late.kafka.bootstrap.servers` | 继承输入 `kafka.bootstrap.servers` | 非空字符串 | 审计 Kafka 地址，可与输入 Kafka 不同 |
| `late.kafka.topic` | `rum-events-late` | 非空字符串 | 审计 topic，需要可用的目标 topic |

审计来自 Session 分支，包含进入该算子后缺少稳定身份的事件、已关闭窗口的有效修正事件，以及清理标记期间拒绝的极迟事件。View 没有独立的审计 Sink，解析失败或被上游过滤的所有数据也不会自动进入该 topic。

`late.kafka.bootstrap.servers` 和 `late.kafka.topic` 即使在 `stdout` / `none` 模式下显式提供，也仍会经过非空校验。主输出和审计输出独立选择；只设 `sink.type=stdout` 不会关闭默认的 Kafka 审计输出。

## 7. Elasticsearch 连接与批量写入

本节仅在 `sink.type=es` 或 `elasticsearch` 时用于构建两个主 Sink。

### 连接

| 参数 | 默认值 | 类型 / 约束 | 说明 |
| --- | --- | --- | --- |
| `sink.es.hosts` | `http://localhost:9200` | 地址字符串 | 逗号分隔的 ES HTTP(S) 地址，如 `http://es-1:9200,http://es-2:9200` |
| `sink.es.username` | 不设置 | 字符串，可选 | HTTP 认证用户名；与 password 同时设置才会传给客户端 |
| `sink.es.password` | 不设置 | 字符串，可选 | HTTP 认证密码；只设置其中一项不会启用这组认证 |
| `sink.es.path-prefix` | 不设置 | 非空路径，可选 | REST 路径前缀，用于代理路径，如 `/elasticsearch`；空白值被忽略 |
| `sink.es.allow-insecure` | `false` | 布尔值 | `true` 跳过 HTTPS 证书链验证，通常保持 `false` |
| `sink.es.connection.request.timeout.ms` | `5000`（5 秒） | 毫秒，非负整数 | 从连接管理器申请连接的超时 |
| `sink.es.connection.timeout.ms` | `5000`（5 秒） | 毫秒，非负整数 | 建立连接的超时 |
| `sink.es.socket.timeout.ms` | `60000`（1 分钟） | 毫秒，非负整数 | 等待 socket 数据、连续数据包之间无活动的超时 |

### Bulk

| 参数 | 默认值 | 单位 / 约束 | 说明 |
| --- | --- | --- | --- |
| `sink.es.bulk.max.actions` | `1000` | 条，正整数或 `-1` | 按缓冲操作数触发提交；`-1` 禁用该条件 |
| `sink.es.bulk.max.size.mb` | `5` | MB，正整数或 `-1` | 按缓冲大小触发提交；`-1` 禁用该条件 |
| `sink.es.bulk.flush.interval.ms` | `2000`（2 秒） | 毫秒，连接器接受 `≥0` 或 `-1` | 定时提交间隔；使用正数设置周期，`-1` 禁用定时触发 |
| `sink.es.bulk.backoff.retries` | `3` | 次，正整数 | Bulk 指数退避的最大重试次数；当前构建器不接受 `0` |
| `sink.es.bulk.backoff.delay.ms` | `1000`（1 秒） | 毫秒，非负整数 | 指数退避初始延迟 |

条数、大小和时间是各自的提交触发条件。Checkpoint 还会等待待写请求完成；批量配置可能影响输出延迟和 Checkpoint 耗时。当前退避类型固定为指数退避，没有暴露类型切换参数。

窗口 `emit.interval.seconds` 控制产生聚合快照的节奏，Bulk 参数控制 Sink 提交请求的节奏；ES 查询可见时间还受索引 refresh 配置影响。

### 分表路由与缓存

| 参数 | 默认值 | 单位 / 约束 | 说明 |
| --- | --- | --- | --- |
| `precalculate.dispersed-count` | `5` | 个，正整数 | 每个预计算家族的分表数量，需与目标存储约定一致 |
| `precalculate.es.cluster-id` | `1` | 非负整数 | 参与分表节点 key 的逻辑 ES 集群 ID；不替代 `sink.es.hosts` |
| `precalculate.es.cache.max-size` | `10000` | 条，正整数 | 每个 Sink emitter 的路由缓存最大条目数；部分约束在 Sink 初始化时校验 |
| `precalculate.es.cache.ttl-minutes` | `60`（1 小时） | 分钟，正整数 | 路由缓存条目写入后的过期时长，与 Session/View 状态 TTL 无关 |

改变 cluster-id 或分表数可能改变写入路由；这些值应与目标索引规划一致。路由缓存按家族、业务、应用、集群、日期和分表数区分条目，失效后按同一配置重建。

索引命名和日期规则由代码确定，见 [索引命名](../index-naming.md)。历史的 `sink.es.index*`、`view.es.index*` 和 `*.daily-index` 参数当前不被读取。TTL 也不控制 ES 已写文档的保留或删除。

## 8. 客户端停止

| 参数 | 默认值 | 单位 / 约束 | 说明 |
| --- | --- | --- | --- |
| `shutdown.timeout.seconds` | `30` | 秒，正整数 | JVM 关闭钩子等待停止请求完成的最长时间；超时或失败记录日志后继续退出 |
| `shutdown.savepoint.path` | 不设置 | 路径，可选 | 设置后调用 `stopWithSavepoint(true, ..., CANONICAL)`；未设置或为空白时调用 `cancel()` |

这两个参数用于附着运行客户端的关闭钩子。Web UI 提交分支不注册该钩子；独立集群的停止和恢复应使用 Flink CLI / REST，见 [部署](operation.md)。Savepoint 路径需要对执行保存的集群可写，不能仅在提交客户端存在。

## 9. 配置示例

### 本地观察结果

该示例读取 Kafka 保留的历史数据，两个主输出和 Session 审计都写 stdout，不连接 ES：

```bash
flink run \
  -c com.tencent.bk.bkmonitor.rum.RumPrecomputeJob \
  target/rum-compute-1.0.jar \
  --kafka.bootstrap.servers localhost:9092 \
  --kafka.topic rum-events \
  --kafka.startup.mode earliest \
  --sink.type stdout \
  --late.sink.type stdout \
  --session.state.ttl.minutes 120 \
  --view.state.ttl.minutes 120
```

### 写入 ES 并保留审计

将以下扁平 JSON 保存为 `rum-config.json`，替换 Kafka/ES 地址。示例保留窗口、Watermark、Checkpoint 和批量写入的默认值；省略的参数使用本文列出的默认值。

```json
{
  "job.name": "rum-session-precompute",
  "parallelism": "4",
  "kafka.bootstrap.servers": "kafka-1:9092,kafka-2:9092",
  "kafka.topic": "rum-events",
  "kafka.group.id": "rum-session-precompute",
  "kafka.startup.mode": "latest",
  "watermark.out-of-orderness.seconds": "120",
  "watermark.idle.minutes": "1",
  "session.emit.interval.seconds": "30",
  "session.gap.minutes": "15",
  "session.max.life.minutes": "240",
  "session.state.ttl.minutes": "120",
  "view.emit.interval.seconds": "30",
  "view.gap.minutes": "15",
  "view.max.life.minutes": "240",
  "view.state.ttl.minutes": "120",
  "checkpoint.interval.ms": "60000",
  "checkpoint.timeout.ms": "600000",
  "checkpoint.tolerable-failures": "3",
  "checkpoint.min-pause.ms": "30000",
  "sink.type": "es",
  "sink.es.hosts": "http://es-1:9200,http://es-2:9200",
  "sink.es.bulk.max.actions": "1000",
  "sink.es.bulk.max.size.mb": "5",
  "sink.es.bulk.flush.interval.ms": "2000",
  "precalculate.dispersed-count": "5",
  "precalculate.es.cluster-id": "1",
  "late.sink.type": "kafka",
  "late.kafka.topic": "rum-events-late"
}
```

提交并通过命令行把并行度覆盖为 8：

```bash
flink run \
  -c com.tencent.bk.bkmonitor.rum.RumPrecomputeJob \
  target/rum-compute-1.0.jar \
  --config /absolute/path/rum-config.json \
  --parallelism 8
```

配置文件的环境变量占位符不会由当前 JSON 加载器自动展开。认证信息应由部署环境生成或挂载到配置文件中，不应把真实凭据写入示例或提交到仓库。

## 10. 修改参数后的生效边界

- 修改文件不会更新正在运行的作业，需要重新提交或按发布流程恢复作业。
- Checkpoint/Savepoint 中已经保存的 processing-time timer 截止时刻，不会仅因修改 TTL、gap 或 max-life 参数就自动重算；恢复后新调度的 timer 才按对应新配置计算。
- 修改启动位点、窗口时长或 TTL 不会重建已写入 ES 的历史结果；修改分表数也不会自动迁移旧索引。
- 参数校验通过只代表配置可被解析或相应组件接受，不代表 Kafka/ES 可达、权限正确或资源足够。部署验证和恢复测试分别见 [部署](operation.md) 与 [集成测试](integration-tests.md)。
