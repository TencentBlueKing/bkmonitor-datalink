# Standard Event 模拟器

`linkd-eventgen` 是独立的开发和压测辅助进程。它可以直接连接 Kafka，也可以读取 Linkd YAML 中已有的 EventSource，按周期构造
`standard` payload，并把消息写入该来源的 Kafka topic。模拟器不直接写 Event、Alert 或 AlertLog，
也不会修改 YAML schema。

## 1. 启动

### 直接配置 Kafka

从镜像 `0.1.1` 起，可以不提供 `linkd.yaml`，直接指定 brokers 和 topic：

```bash
linkd-eventgen \
  --kafka-brokers kafka.example.com:9092,kafka2.example.com:9092 \
  --kafka-topic linkd-demo \
  --event-source-id demo-source --tenant-id tenant-a \
  --new-alerts-per-minute 60 --cycle-duration 1s --duplicate-percent 0
```

这表示每秒新增 1 条告警，恢复消息另计。直连模式必须同时提供 brokers、topic、来源 ID 和租户，
固定使用 standard 清洗与 `source_alert_id` fingerprint；下游来源应保持相同定义。producer 不消费消息，因此无需配置 consumer group。
直连模式使用无认证的 plaintext Kafka。需要 TLS、SASL、自定义 fingerprint 或 severity mapping 时，使用下面的配置文件模式。
显式 `--config` 与直连参数互斥，避免两份配置产生不明确的优先级。

### 配置文件模式

所选 EventSource 必须满足以下条件：

- `enabled: true`；
- `cleaner.type: standard`；
- `storage.type: kafka`；
- fingerprint 包含 `source_alert_id`、`subject_id` 或 `dimensions.generator_id` 中至少一个。

来源未配置 `related_tenant_id` 时必须显式提供 `--tenant-id`；来源已固定租户时可以省略，若同时提供
则必须与配置相同。

下面的配置表示每分钟新增 20 条告警、每 30 秒运行一个周期，因此每周期新增 10 条；每条活动告警
每周期以 `1 / 4` 的概率恢复：

```bash
go run ./cmd/linkd-eventgen \
  --config ./configs/linkd.yaml \
  --event-source-id demo-source \
  --tenant-id tenant-a \
  --new-alerts-per-minute 20 \
  --cycle-duration 30s \
  --mean-lifetime-cycles 4 \
  --duplicate-percent 20
```

第一个周期在进程启动后立即执行。收到 `SIGINT` 或 `SIGTERM` 后，模拟器停止创建新周期，等待当前
Kafka 同步发送调用结束并关闭 producer。

### 容器镜像

模拟器镜像独立版本化，当前版本为 `0.1.1`（首次为 `0.1.0`），不跟随 Linkd / Console 的版本变化。
GitHub workflow 的默认 `all` 不构建它；需显式选择 `component=linkd-eventgen` 并填写
`eventgen_version`。发布操作见 [手动构建与发布](image-release.md)。

本地构建通过独立 Makefile 目标执行，版本参数也与 `VERSION` / `IMAGE_TAG` 分开：

```bash
make eventgen-image EVENTGEN_VERSION=0.1.1
docker run --rm linkd-eventgen:0.1.1 version
docker run --rm \
  -v /absolute/path/linkd.yaml:/data/linkd/configs/linkd.yaml:ro \
  linkd-eventgen:0.1.1 \
  --event-source-id demo-source --tenant-id tenant-a --cycles 2
```

镜像默认以 UID/GID `65532` 运行，挂载配置需允许该用户读取；配置中的 Kafka 地址须能从容器访问。
无参数启动只显示帮助；`version` 无需配置或 Kafka，输出镜像版本与完整 Git commit。
未注入构建信息时显示 `dev` / `unknown`。本地命令使用 Docker；Apple Container 可将 `docker run` 换成 `container run`。

## 2. 参数

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--kafka-brokers` | 无 | 直连 brokers，逗号分隔，最多 32 个 |
| `--kafka-topic` | 无 | 直连目标 topic，与 brokers 配套使用 |
| `--config` | `./configs/linkd.yaml` | Linkd YAML 配置路径 |
| `--event-source-id` | 无 | 必填的 EventSource ID |
| `--tenant-id` | 无 | 来源未固定租户时必填 |
| `--new-alerts-per-minute` | `20` | 每分钟新告警数，范围 `1..1000000` |
| `--cycle-duration` | `30s` | 周期长度，范围 `10ms..10m` |
| `--mean-lifetime-cycles` | `4` | 几何分布的平均存活周期数，最小为 1 |
| `--duplicate-percent` | `0` | 每条本周期 Event 随机追加一条完全相同 delivery 的概率，范围 `0..100` |
| `--scenarios` | 全部场景 | 逗号分隔的场景枚举 |
| `--seed` | `0` | `0` 自动生成 seed；非零值使场景和维度序列可复现 |
| `--max-active-alerts` | `100000` | 进程内活动告警硬上限 |
| `--cycles` | `0` | `0` 持续运行；正数表示有限周期数 |

每分钟速率使用余数累加，而不是逐周期四舍五入。例如每分钟 1 条、20 秒周期会依次新增
`0、0、1、0、0、1...`，不会产生长期速率漂移。若某周期的新告警会使活动池超过硬上限，进程明确
失败，不会静默降低配置速率。

## 3. 内置场景

| 场景 | 主要稳定维度 |
| --- | --- |
| `cpu_high` | `ip`、`cpu_core` |
| `memory_high` | `ip`、`memory_total_gb` |
| `disk_full` | `ip`、`device`、`mount_point`、`filesystem` |
| `disk_read_only` | `ip`、`device`、`mount_point`、`filesystem` |
| `disk_io_latency_high` | `ip`、`device`、`mount_point` |
| `oom_killed` | `ip`、`namespace`、`pod`、`container`、`process` |
| `process_down` | `ip`、`process_name`、`service_name` |
| `host_unreachable` | `ip`、`zone` |
| `network_packet_loss_high` | `ip`、`peer_ip`、`interface` |
| `service_unavailable` | `service`、`instance`、`ip`、`port`、`protocol` |
| `http_error_rate_high` | `service`、`route`、`method`、`status_class` |
| `database_connections_high` | `db_instance`、`engine`、`region` |
| `online_users_zero` | `app`、`region`、`channel` |
| `queue_backlog_high` | `queue`、`consumer_group`、`cluster` |

所有场景还包含唯一 `dimensions.generator_id`。测量值、阈值、单位和 firing/resolved 状态放在
`extra_data`，不会在恢复时改变稳定维度。场景在 `--scenarios` 选中集合中均匀随机选择。

## 4. 身份、生命周期和 Kafka

每次进程启动使用一个新的随机 run ID；新告警通过 run ID 加自增序号构造 `source_alert_id`、
`subject_id` 和 `dimensions.generator_id`，每条事件再使用独立序号构造 `source_event_id`。发送前，
模拟器使用当前 StandardCleaner 和 EventFactory 校验 payload，并确认同一告警的 `triggered` 与
`resolved` 会得到相同 fingerprint。

每个周期只对周期开始前已经活动的告警抽样。平均生命周期为 `N` 时，每条告警以 `1/N` 的概率发送
`resolved`；因此新告警不会在创建周期立即恢复，`N=1` 时会在下一周期全部恢复。恢复消息排在本周期
新触发消息之前。

Kafka record 使用最终 fingerprint 作为 key，并携带 `message_id`、`bk_tenant_id` 和 `order_key`
headers。producer 复用 EventSource 的 brokers、TLS 和 SASL 配置，要求 all-ISR ACK，并按最多 1000 条
进行有界同步分块。任一消息最终失败时进程返回非零；错误可能发生在一批消息部分成功之后，日志会保留
周期和发送数量，但不会打印完整 payload。

`--duplicate-percent` 命中时，重复项紧邻原项发送，并复用完全相同的 body、headers、message_id、
event_id 和时间字段。它用于验证 Repository 幂等和 Mailbox 重复引用收敛；不会用相同 ID 重新构造带
新时间的 payload，因此不会把身份冲突误当成重复投递。

## 5. 重启边界与 rawgen

`linkd-eventgen` 不持久化活动池、自增序号或随机状态。重启会创建新的 run ID，因此不会与旧告警身份
冲突，但新进程也不会恢复上次运行遗留的活动告警，退出时同样不会批量发送恢复事件。

[`rawgen`](../../tests/tools/rawgen/README.md) 仍用于一次性、可计算预期结果的 JSONL/E2E 数据集；
`linkd-eventgen` 用于持续速率、随机生命周期和真实 Kafka 推送，两者不共享运行状态或生产实现。

真实 Kafka 集成测试默认跳过，显式指定测试 broker 后会创建并清理独立临时 topic：

```bash
LINKD_TEST_KAFKA_BROKER=127.0.0.1:9092 \
  go test ./internal/eventgen -run TestKafkaPublisherIntegration -count=1
```
