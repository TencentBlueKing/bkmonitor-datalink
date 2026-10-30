# rum-compute

基于 Apache Flink 的 RUM 计算作业。从 Kafka 读取浏览器事件，按 Session 和 View 聚合，将结果写入 Elasticsearch，供会话和页面查询使用。项目背景参考 [蓝鲸监控 bk-monitor](https://github.com/TencentBlueKing/bk-monitor)。

| 聚合对象 | 分组字段 | 输出内容 |
| --- | --- | --- |
| Session | `bk_biz_id`、`app_name`、`session.id` | 会话信息、页面数、动作数、请求数和错误数等 |
| View | `bk_biz_id`、`app_name`、`view.id` | 页面信息、动作数、资源数和错误数等 |

同一事件可以进入两条聚合链路。窗口按处理时间关闭，活动期间定期输出有变化的结果，关闭后在状态保留期内接受修正。具体规则见 [窗口与状态](docs/overview/architecture.md#窗口与状态)。

## 构建

需要 Maven 和 JDK 21 toolchain，配置方式见 [构建与运行](docs/overview/source_compile.md)。项目使用 Flink 2.2.1，编译目标为 Java 11。

```bash
mvn clean package
```

产物为 `target/rum-compute-1.0.jar`。

## 运行

准备 Kafka topic 和 Elasticsearch 后，通过 Flink CLI 提交：

```bash
flink run \
  -c com.tencent.bk.bkmonitor.rum.RumPrecomputeJob \
  target/rum-compute-1.0.jar \
  --kafka.bootstrap.servers localhost:9092 \
  --kafka.topic rum-events \
  --kafka.startup.mode earliest \
  --sink.type es \
  --sink.es.hosts http://localhost:9200
```

默认还会向 `rum-events-late` 写入 Session 审计事件，需提前创建该 topic。完整配置和本地环境准备见 [构建与运行](docs/overview/source_compile.md)，集群提交方式见 [部署](docs/overview/operation.md)。

升级旧作业前需查看 [状态与索引升级说明](docs/overview/architecture.md#恢复与升级)。旧租户 key 或缺少 `window_id` 的状态不能直接恢复。

## 文档

- [文档目录](docs/README.md)
- [参数说明](docs/overview/parameters.md)
- [可观测指标](docs/overview/observability.md)
- [数据流](docs/overview/architecture.md)
- [预计算字段](docs/Precalculate-index-fields.md)与[索引命名](docs/index-naming.md)
- [贡献指南](CONTRIBUTING.md)

## 许可

[MIT License](LICENSE)。
