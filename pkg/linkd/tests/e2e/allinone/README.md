# all-in-one 双 Repository E2E

测试分别启动 `storage.repository: elasticsearch` 和 `storage.repository: mysql` 的
`linkd run all-in-one`，验证同一条链路：

```text
RawEventMessage(standard)
  -> SourceCleaner / EventFactory
  -> Event + processing metadata
  -> Redis Mailbox Event ID + lifecycle signal
  -> 上游 Kafka offset 确认
  -> Alert / AlertLog
  -> Kafka Alert V1 snapshot
```

[`rawgen`](../../tools/rawgen) 使用固定 seed 生成 triggered、重复投递、resolved、closed、等级升级、低等级抑制、坏消息和跨租户场景。断言覆盖稳定 Event ID、租户覆盖、时间回退、Severity mapping、`related_alert_id`、处理 outcome、Alert 不可变继承字段、双快照升级输出和 Kafka cause。

每次运行使用唯一的 MySQL database 或 Elasticsearch index prefix，以及独立 Kafka topic/group 和
Redis stream/group/mailbox/lock 前缀；结束时只清理本次资源。Raw topic 固定创建 3 个 partition，并
验证最终 Event、Alert、AlertLog 和输出数量。Mailbox Signal 按空到非空产生并允许冗余，因此只断言
consumer group 无 pending/lag，并完整扫描确认不存在非空 Mailbox List，不断言 Signal 与 Event 一一对应。普通 `go test ./...` 不访问
外部服务，未设置 `LINKD_E2E=1` 时跳过。

运行两套后端：

```bash
./tests/e2e/allinone/run.sh
```

只运行单一后端：

```bash
LINKD_E2E=1 go test -count=1 -run '^TestAllInOneElasticsearchE2E$' -v ./tests/e2e/allinone
LINKD_E2E=1 go test -count=1 -run '^TestAllInOneMySQLE2E$' -v ./tests/e2e/allinone
```

连接可由 `LINKD_E2E_ELASTICSEARCH_URL`、`LINKD_E2E_REDIS_ADDRESS`、`LINKD_E2E_REDIS_PASSWORD`、`LINKD_E2E_REDIS_DATABASE`、`LINKD_E2E_KAFKA_BROKER`、`LINKD_E2E_MYSQL_ADDRESS`、`LINKD_E2E_MYSQL_USERNAME` 和 `LINKD_E2E_MYSQL_PASSWORD` 覆盖。

生成独立 JSONL 数据集：

```bash
go run ./tests/tools/rawgen \
  -seed 42 \
  -mix active=1000,recovered=1000,closed=1000,severity_rotation=500,cross_tenant=100 \
  -tenant-count 20 \
  -duplicates 100 \
  -invalid 20 \
  -out /tmp/linkd-raw-events.jsonl \
  -expected-out /tmp/linkd-expected.json
```

生成器的随机性只影响稳定输入字段和生命周期块顺序；相同 seed/profile 产生字节级一致记录，同一生命周期内部顺序不变。

动态调度 E2E 使用测试专属部署命名空间和临时 API 端口，启动后显式调用 API 导入来源。
随后增加 3 个 Cleaner worker 和 1 个 Lifecycle worker，验证 4 个 Cleaner 候选受 3 partition 限制、2 个 Lifecycle 副本共享来源 Stream，再校验业务处理和输出。
普通测试不自动访问外部服务；所有清理仅针对本次测试生成的专属资源。

独立的多进程故障演练入口是 `TestSchedulingDrillE2E`，步骤和断言见
[核心任务调度验证流程](../../../docs/guides/task-scheduling-validation.md)。该演练保留本轮外部资源与证据，
只停止本轮子进程，不使用上述业务 E2E 的资源自动清理逻辑。
