# 故障恢复集成测试

`RumJobRecoveryIT` 使用 Kafka、Elasticsearch 和 Flink MiniCluster，执行 `RumJobTopology.build` 生成的拓扑。

## 运行

需要 JDK 21 toolchain、Maven 和可访问的 Docker daemon：

```bash
mvn -Pintegration-tests verify
```

普通 `mvn test` 不运行该测试。启用 profile 后，Docker 不可用会导致失败。测试结果位于 `target/failsafe-reports/`。

## 覆盖范围

测试比较无故障执行与 TaskManager 故障恢复后的结果，检查：

- 两个租户使用相同 Session/View ID 时的状态和文档隔离。
- Checkpoint 恢复后的 Kafka 重放、事件去重和关闭后修正。
- `window_id`、ES `_id`、索引日期和首次关闭信息。
- Session/View 业务字段及 Session 审计事件的 Kafka 投递。
- 仓库中的两份 Elasticsearch 模板能被测试实例接受。

场景限定为同一版本、关闭后的状态保留期内恢复。它未覆盖 JobManager HA、Pod/节点故障、超保留期恢复、旧版 Savepoint 迁移和 ES 乱序写入保护。

升级要求见 [恢复与升级](architecture.md#恢复与升级)，测试实现见 [RumJobRecoveryIT](../../src/test/java/com/tencent/bk/bkmonitor/rum/integration/RumJobRecoveryIT.java)。
