# 构建与运行

## 环境

- Maven。
- JDK 21 toolchain；编译目标为 Java 11。
- 运行时使用 Flink 2.2.1、Kafka 和 Elasticsearch 7.x。
- Docker 用于本地依赖和集成测试。

在 `~/.m2/toolchains.xml` 中配置本机 JDK 21 路径：

```xml
<toolchains>
  <toolchain>
    <type>jdk</type>
    <provides><version>21</version></provides>
    <configuration><jdkHome>/path/to/jdk-21</jdkHome></configuration>
  </toolchain>
</toolchains>
```

## 构建和测试

在项目根目录执行：

```bash
mvn test
mvn package
```

产物为 `target/rum-compute-1.0.jar`，Flink 核心依赖由运行集群提供。需要 Docker 的测试见 [集成测试](integration-tests.md)。

## 本地依赖与提交

仓库提供的 Compose 配置包含 Kafka、Elasticsearch，以及 topic 和索引模板初始化任务：

```bash
docker compose up -d
```

使用本地 Flink 2.2.1 集群提交：

```bash
flink run \
  -c com.tencent.bk.bkmonitor.rum.RumPrecomputeJob \
  target/rum-compute-1.0.jar \
  --kafka.bootstrap.servers localhost:9092 \
  --kafka.topic rum-events \
  --kafka.startup.mode earliest \
  --sink.type stdout \
  --late.sink.type stdout
```

写入 Elasticsearch 时改为 `--sink.type es --sink.es.hosts http://localhost:9200`。容器内运行时，地址应使用容器可访问的 Kafka 和 Elasticsearch 地址。

## 配置文件

`--config <path>` 读取扁平 JSON 对象，所有值必须是字符串；同名命令行参数覆盖文件值。现有 `submit.ini` 也按 JSON 读取，文件扩展名不影响解析。

```json
{
  "job.name": "rum-compute",
  "kafka.bootstrap.servers": "localhost:9092",
  "kafka.topic": "rum-events",
  "kafka.startup.mode": "earliest",
  "sink.type": "es",
  "sink.es.hosts": "http://localhost:9200",
  "late.sink.type": "kafka"
}
```

将上述内容保存为本地 `submit.ini`，提交时使用 `--config /absolute/path/submit.ini`。该文件需要能被执行 `main` 的进程读取；通过 Web UI 或 REST 提交时，应放在 JobManager 可读取的位置。含凭据的文件不要提交到仓库。

## 常用参数

全部应用参数的默认值、单位、约束和生效边界见 [参数说明](parameters.md)。

### 作业与 Kafka

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `job.name` | `rum-session-precompute` | Flink 作业名 |
| `parallelism` | `4` | 作业设置的并行度 |
| `kafka.bootstrap.servers` | 必填 | Kafka 地址 |
| `kafka.topic` | 必填 | 输入 topic |
| `kafka.group.id` | `rum-session-precompute` | 提交或读取 Kafka 位点的消费组 |
| `kafka.startup.mode` | `latest` | `latest`、`earliest` 或 `committed` |
| `watermark.out-of-orderness.seconds` | `120` | Watermark 乱序时长 |
| `watermark.idle.minutes` | `1` | 空闲分区检测时长 |

`committed` 没有已提交位点时从最早可用位点读取。从 Checkpoint/Savepoint 恢复时使用状态中的位点。`job.name` 和 `kafka.group.id` 的默认值分别读取，设置前者不会修改后者。

### 窗口

参数前缀分别为 `session` 和 `view`，例如 `session.gap.minutes`。

| 参数后缀 | Session 默认值 | View 默认值 | 含义 |
| --- | --- | --- | --- |
| `emit.interval.seconds` | `30` | `30` | 活动窗口有变化时的输出间隔 |
| `gap.minutes` | `15` | `15` | 连续无新事件的关闭时长 |
| `max.life.minutes` | `240` | `240` | 自窗口创建起的最大时长 |
| `state.ttl.minutes` | `120` | `120` | 首次关闭后的状态保留期；清理后再保留同等时长的标记 |

`session.max.string.chars` 默认 `512`，限制部分 Session 展示字段的长度；应用名和实体 ID 保留原值。窗口行为见 [窗口与状态](architecture.md#窗口与状态)。

### 输出

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `sink.type` | `es` | `es`、`elasticsearch` 或 `stdout` |
| `sink.es.hosts` | `http://localhost:9200` | 多个 ES 地址用逗号分隔 |
| `sink.es.username` / `sink.es.password` | 无 | 认证时成对设置 |
| `precalculate.es.cluster-id` | `1` | 分表路由使用的 ES 集群 ID |
| `precalculate.dispersed-count` | `5` | 分表数，需与存储配置一致 |
| `late.sink.type` | `kafka` | Session 审计输出，可选 `kafka`、`stdout`、`none` |
| `late.kafka.topic` | `rum-events-late` | 审计 topic |
| `late.kafka.bootstrap.servers` | 输入 Kafka 地址 | 审计 Kafka 地址 |

索引前缀由[命名规则](../index-naming.md)固定，旧的 `sink.es.index*`、`view.es.index*` 和 `*.daily-index` 参数不被读取。

### Checkpoint 与客户端停止

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `checkpoint.interval.ms` | `60000` | Checkpoint 间隔 |
| `checkpoint.timeout.ms` | `600000` | Checkpoint 超时 |
| `checkpoint.tolerable-failures` | `3` | 容忍的 Checkpoint 失败次数 |
| `checkpoint.min-pause.ms` | `30000` | 两次 Checkpoint 的最小间隔 |
| `shutdown.timeout.seconds` | `30` | 附着式客户端关闭钩子的等待时间 |
| `shutdown.savepoint.path` | 无 | 配置后调用 `stopWithSavepoint`，否则调用 `cancel` |

集群作业的停止与恢复见 [部署](operation.md)。其他参数见 [RumJobConfig](../../src/main/java/com/tencent/bk/bkmonitor/rum/RumJobConfig.java) 和 [PrecalculateElasticsearchSink](../../src/main/java/com/tencent/bk/bkmonitor/rum/sink/PrecalculateElasticsearchSink.java)。
