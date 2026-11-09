# 可观测指标说明

RUM 预计算作业通过指标观察输入质量、Session/View 窗口状态和 Elasticsearch 写入情况。本文列出应用指标、常用运行指标及 Prometheus 查询方式。

## 应用指标

下表使用指标的相对名称，完整 Prometheus 名称见[采集与查询](#采集与查询)。Counter 表示累计次数，Gauge 表示当前值。

### 输入解析

位于 `rum-events-source` 算子。

| 指标 | 类型 / 单位 | 含义 |
| --- | --- | --- |
| `serde.malformed_records` | Counter / 消息 | 解析失败并跳过的 Kafka 消息数，包括空消息、非法 JSON 和不支持的消息结构 |
| `serde.malformed_items` | Counter / item | 消息内解析失败并跳过的 item 数；同一消息中的合法 item 继续处理 |

一条 Kafka 消息可以包含多个 item，并展开为多条事件。计算错误率时，应使用相同单位的分母。合法的空 `items` 数组不计为解析错误。

### Session/View 窗口

分别位于 `session-precompute` 和 `view-precompute` 算子。

| 指标 | 类型 / 单位 | 含义 |
| --- | --- | --- |
| `active_keys` | Gauge / 窗口 | 已打开且尚未关闭的窗口数，可用于观察窗口积压和各子任务负载分布 |
| `duplicate_events` | Counter / 事件 | 事件身份已存在于当前去重记录中，因此被跳过的事件数 |
| `missing_event_identity` | Counter / 事件 | 缺少稳定事件身份，无法进入聚合的事件数 |
| `closed_window_corrections` | Counter / 事件 | 窗口关闭后，在状态保留期内参与修正并产生新快照的事件数 |
| `expired_window_late_events` | Counter / 事件 | 窗口状态清理后，被判定为过期并拒绝聚合的事件数 |

窗口计数覆盖通过对应分支业务过滤的事件。Session/View 分别统计，同一事件可能进入两个分支。

`active_keys` 在窗口关闭时下降，窗口状态会继续保留一段时间以接收修正。该指标支持故障恢复和扩缩容。窗口时长与状态保留期见[参数说明](parameters.md)。

### Elasticsearch 写入

使用 Elasticsearch 输出时，分别位于 `session-precalculate-es-sink` 和 `view-precalculate-es-sink`。

| 指标 | 类型 / 单位 | 含义 |
| --- | --- | --- |
| `es.bulk_records_total` | Counter / action | 已收到 bulk 响应的写入操作总数，包含成功和失败操作 |
| `es.bulk_records_failed_total` | Counter / action | 已收到 bulk 响应的操作中，写入失败的数量 |

一个 action 表示一次文档写入操作。两项之差表示已收到成功响应的操作数；重复更新同一文档会多次计数。请求未返回响应时的网络失败需结合失败日志和作业重启指标查看。

## 常用运行指标

以下指标由 Flink 或连接器提供，完整名称以 `/metrics` 为准。

| 指标 | 位置 | 含义 / 单位 |
| --- | --- | --- |
| `numRecordsIn`、`numRecordsOut` | 算子 | 输入、输出累计条数 |
| `numRecordsInPerSecond`、`numRecordsOutPerSecond` | 算子 | 输入、输出速率，条/秒 |
| `pendingRecords` | Kafka Source | 已分配分区的消费积压，条 Kafka 消息 |
| `KafkaSourceReader.KafkaConsumer.records-consumed-total`、`bytes-consumed-total` | Kafka Source | 消费的原始 Kafka 消息数、字节数 |
| `busyTimeMsPerSecond`、`backPressuredTimeMsPerSecond`、`idleTimeMsPerSecond` | Task | 忙碌、背压和空闲时间，毫秒/秒 |
| `numRestarts` | JobManager 作业 | 作业重启次数 |
| `numberOfFailedCheckpoints` | JobManager 作业 | Checkpoint 失败次数 |
| `lastCheckpointDuration`、`lastCheckpointCompletedTimestamp` | JobManager 作业 | 最近 Checkpoint 耗时、最近成功完成时间，分别为毫秒和 Unix 毫秒时间戳 |
| `lastCheckpointFullSize`、`lastCheckpointSize` | JobManager 作业 | 完整状态大小、最近 Checkpoint 实际写出大小，字节 |

吞吐统计口径：

- Source 输出统计展开后的事件数；原始 Kafka 消息数使用 `records-consumed-total`。
- 聚合输入统计经过对应分支业务过滤的事件，包含之后被去重或拒绝的事件。
- Session 输出包含快照和审计侧输出。查看快照到达量，应读取 Session sink 的输入指标。
- View 输出统计快照数。

[flink-standalone.yaml](../../k8s-deploy/flink-standalone.yaml) 还启用了 RocksDB 键数、SST 文件大小、有效数据大小及 memtable 大小指标，用于观察状态存储规模。窗口关闭后的状态保留期和文件回收会影响这些指标下降的时间。

## 采集与查询

默认部署提供 Prometheus `/metrics` 接口，端口范围为 `9249-9250`，以实际监听端口为准。分别抓取各 TaskManager 的算子指标和 JobManager 的作业指标。

```bash
curl -fsS http://<taskmanager-pod-ip>:9249/metrics
```

应用指标的 Prometheus 名称均以 `flink_taskmanager_job_task_operator_` 开头，后缀规则如下：

| 相对名称 | Prometheus 后缀 |
| --- | --- |
| `serde.malformed_records`、`serde.malformed_items` | `deserializer_serde_malformed_records`、`deserializer_serde_malformed_items` |
| 窗口指标，如 `active_keys` | 保持原名，如 `active_keys` |
| `es.bulk_records_total`、`es.bulk_records_failed_total` | `es_bulk_records_total`、`es_bulk_records_failed_total` |

使用 `job_id` 选择作业、`operator_name` 区分算子、`subtask_index` 查看子任务。算子标签可能带运行时前后缀，名称中的连字符通常转为下划线，以实际导出值为准。

累计计数使用 `rate()` 查看速率，使用 `increase()` 查看时间段内的增量，先计算各序列再求和。`active_keys` 和 `*PerSecond` 指标直接读取。虽然 Flink 累计计数在 Prometheus 中导出为 Gauge，仍应按累计值查询。

应用 Counter 在算子重建后归零，`active_keys` 随 Checkpoint 恢复。失败后快速重启可能使最后一次增量来不及被抓取，告警应结合重启、Checkpoint 和失败日志。

以下示例中的 `$job_id` 替换为实际 Flink Job ID。

Session/View 聚合输入速率：

```promql
sum by (operator_name) (
  flink_taskmanager_job_task_operator_numRecordsInPerSecond{job_id="$job_id",operator_name=~"(session|view).precompute"}
)
```

各聚合算子的活跃窗口数：

```promql
sum by (operator_name) (flink_taskmanager_job_task_operator_active_keys{job_id="$job_id"})
```

最近 5 分钟解析失败的 Kafka 消息数；查看失败 item 数时，将后缀替换为 `malformed_items`：

```promql
sum(increase(flink_taskmanager_job_task_operator_deserializer_serde_malformed_records{job_id="$job_id"}[5m]))
```

最近 5 分钟各 ES sink 响应中的写入失败数：

```promql
sum by (operator_name) (increase(flink_taskmanager_job_task_operator_es_bulk_records_failed_total{job_id="$job_id"}[5m]))
```
