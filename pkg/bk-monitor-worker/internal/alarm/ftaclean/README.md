# 告警源清洗

`fta-clean` 队列运行 `daemon:alarm:fta-clean`。启动时注册固定分片，每个 DataID 归属 `bk_data_id % shards` 对应的任务。各分片直接查询元数据库，读取已启用的 `bk_fta_event` 数据源、实际绑定的结果表、字段规则和 Kafka 配置。每次刷新同时按 DataID 查询 `event_plugin_instance`，只接管至少关联一个启用且未删除实例的数据源。最后一个有效实例停用、软删除或物理删除后，下一轮刷新停止该源消费。实例重新启用后可再次发现。清洗规则和 Kafka 路由仍读取元数据。关闭数据源或全部结果表也会停止消费。查询失败保留已有消费者，配置变更会在旧消费者退出后应用到该源。

## 配置与交接

默认关闭。为专用 worker 的 `worker.queues` 增加 `fta-clean`，worker 与 Transfer 共用消费组拼接逻辑，默认按蓝鲸部署模板使用 `bkmonitorv3_transfer` 直接拼接输入 topic，例如 `bkmonitorv3_transfer0bkmonitor_50010`。自定义部署应将实际前缀原样填入 `fta_clean.consumer_group_prefix`；直接使用 Transfer 源码兜底配置的部署也属于此情形。下表给出 worker 配置和对应的 Python 元数据发布配置。

| worker 配置 | 默认值 | 用途 |
|---|---|---|
| `fta_clean.enabled` | `false` | 开启接管，对应 Python 环境变量 `FTA_CLEAN_ENABLED` |
| `fta_clean.data_ids` | `[]` | 灰度清单，对应 `FTA_CLEAN_DATA_IDS`，Python 侧用逗号分隔 |
| `fta_clean.all` | `false` | 接管所有启用的标准源，对应 `FTA_CLEAN_ALL` |
| `fta_clean.shards` | `4` | 常驻任务数，多个 worker 的配置应相同 |
| `fta_clean.max_concurrency` | `8` | 进程内共享的解码与清洗名额，独立于 CPU 核数 |
| `fta_clean.refresh_interval` | `60s` | 元数据刷新周期 |
| `fta_clean.batch_messages / batch_bytes / batch_wait` | `128 / 1048576 / 50ms` | 每个输入分区独立组批，达到任一阈值后处理 |

1. 先以关闭接管的配置部署相关组件。核对该源的输入 Kafka 集群、topic 和 Transfer 实际 group.id，记录分区位点。保持 GSE 上报和旧 Transfer Consul 配置。
2. 给专用清洗 worker 开启本批 DataID，让它先加入原消费组。通过 Kafka 确认新成员与 Transfer 同组；多分区时可分别处理不同分区，单分区时新成员可能暂时空闲。
3. 在 Python 和 Go 元数据发布端设置相同的接管范围，待配置生效后移除本批旧键，让 Transfer 平稳退出并排空在途输出。确认 worker 接管全部分区，原消费组继续推进、结果表有新输出、事件进入 ES。重平衡会短暂停顿，Transfer 延迟提交的位点可能导致边界消息重投。
4. 回滚时先恢复元数据发布并刷新旧配置，让 Transfer 重新加入原消费组，再让专用 worker 退出该批 DataID。确认分区归属、积压和输出恢复后再扩大范围。修改固定分片数仍应先停旧清洗 worker，再以统一的新配置启动。

同一分区一次处理一个批次。批内分块并行，按输入顺序汇集，再批量写各目标 Kafka 集群。收到所有有效输出的确认后才标记输入位点。已知部分失败仅重试未确认的消息，进程中断或 rebalance 可能导致已写消息重投。Kafka 分区和 broker 继续决定实际存储顺序。

## 测试

在 `pkg/bk-monitor-worker` 目录运行。数据库用例通过 `BMW_FTA_TEST_DSN` 选择本地测试 MySQL，每个用例只操作当前连接的临时表，使用合成数据。

```sh
go test -race ./internal/alarm/ftaclean
go test -race ./config -run TestFTACleanConfig
go test -race -gcflags='github.com/facebookincubator/go-qfext=-d=checkptr=0' ./service/scheduler/daemon -run TestFTAShardRegistration
BMW_FTA_TEST_DSN='root@tcp(127.0.0.1:3306)/test?parseTime=true' go test -race -vet=off ./internal/alarm/ftaclean ./internal/metadata/service -run 'Test(Discovery|FTAMetadata)$'
go test -tags jsonsonic -run '^$' -bench='Benchmark(Clean|Pipeline)' -benchmem -benchtime=200ms -count=5 -cpu=8 ./internal/alarm/ftaclean
go test -tags jsonsonic -run '^$' -bench='BenchmarkClean/limit8$' -benchmem -benchtime=200ms -count=5 -cpu=1,2,4 ./internal/alarm/ftaclean
```

daemon 的 race 命令仅为既有依赖 go-qfext 关闭 checkptr。worker 清洗包保持默认 race 检查。用例覆盖规则与 Transfer 的兼容性、并发上限、三个刷批条件、部分写入失败、取消与位点、慢分区隔离、元数据查询失败、租户隔离和规则热更新。

## 性能基线

2026-11-27，Apple M4 Pro，14 个逻辑 CPU，darwin/arm64，Go 1.24.4，发布构建标签 `jsonsonic`。以下是 5 次采样的中位数。清洗用例每批一条 Kafka 输入、512 个事件、两个结果表。`events/s` 统计输入事件，`ns/op` 和分配量按批计算。

| GOMAXPROCS | 并发上限 | events/s | ns/op | B/op | allocs/op |
|---:|---:|---:|---:|---:|---:|
| 8 | 1 | 134,395 | 3,809,675 | 6,324,629 | 57,946 |
| 8 | 2 | 192,228 | 2,663,505 | 7,132,911 | 58,016 |
| 8 | 4 | 264,388 | 1,936,548 | 7,543,978 | 58,025 |
| 8 | 8 | 329,508 | 1,553,833 | 7,292,824 | 57,997 |
| 8 | 16 | 303,474 | 1,687,133 | 7,261,116 | 57,997 |
| 1 | 8 | 114,642 | 4,466,081 | 5,219,536 | 57,947 |
| 2 | 8 | 177,280 | 2,888,097 | 6,174,977 | 57,961 |
| 4 | 8 | 280,893 | 1,822,765 | 6,844,009 | 57,983 |

内存管线使用相同的 Kafka claim 处理入口，以及在内存中确认批次的 producer。每个热点输入包含 256 个事件，混合场景有 3 个热点分区和 1 个单事件源。慢输出场景让一个分区的确认延迟 1ms。下表固定 GOMAXPROCS 和并发上限为 8。

| 场景 | events/s | ns/op | B/op | allocs/op | 低流量源平均交付延迟 |
|---|---:|---:|---:|---:|---:|
| 单热点分区 | 300,802 | 851,061 | 4,403,146 | 29,035 | — |
| 同源四分区 | 468,772 | 2,184,436 | 15,677,990 | 116,073 | — |
| 多源混合 | 443,062 | 1,735,655 | 13,191,304 | 87,218 | 85.8 µs |
| 单分区慢写 | 305,859 | 3,347,959 | 16,118,091 | 116,080 | — |

这些数值覆盖解码、规则匹配、生成输出及进程内批量交付。线上吞吐和时延还取决于 Kafka、分区分布、消息大小及实际规则。

## 日志与指标口径

分片启动、退出，以及 DataID 加入、配置替换、移除时写一条 INFO。周期刷新遇到相同配置时保持安静。发现、配置、消费连接和输出重试在首次失败时记录错误，持续失败时每分钟最多提醒一次，恢复时记录累计失败次数。写出日志按分区和目标集群区分，包含输入位点范围及待写条数。解码异常按 DataID 限频，只打印分区和位点；规则匹配不打印事件正文。

以下指标使用前缀 `bmw_fta_clean_`：

- `slots_in_use` 和 `slots_limit` 是进程级指标；`slot_wait_seconds` 记录每个 DataID 的工作块成功取得名额所花的时间，包括无需等待的情况。判断低流量源是否受到影响，可在有样本的时间窗口按 DataID 看 P99。
- `batches_total{reason}` 和 `batch_messages` 在开始处理批次时统计。原因包括消息数、字节数、超时和输入通道关闭；同时达到数量和字节阈值时归到数量。
- `expanded_events_total` 统计展开后的事件。对于 `discards_total{reason}`，decode 按输入消息计，flatten 按展开事件计，transform、encode 和 filtered 按结果表记录计。filtered 表示输出过滤，字段级转换失败后仍能输出的记录不计入丢弃。一条输入可以展开为多个事件并投递多张结果表。
