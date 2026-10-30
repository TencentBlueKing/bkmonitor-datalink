# Flink Web UI 与 REST 提交

先按 [构建与运行](../docs/overview/source_compile.md) 生成 `target/rum-compute-1.0.jar`，准备 Kafka、Elasticsearch 和 Flink 2.2.1 集群。仓库的 Kubernetes 配置说明见 [部署](../docs/overview/operation.md)。

## 连接 JobManager

使用仓库中的 Service 配置时，可在本地转发 REST 端口：

```bash
kubectl -n flink port-forward svc/flink-jobmanager 8081:8081
```

以下示例使用 `http://localhost:8081`。

## Web UI

打开 [Flink Web UI](http://localhost:8081)，上传 JAR，设置：

- Entry Class：`com.tencent.bk.bkmonitor.rum.RumPrecomputeJob`
- Arguments：`--config /etc/rum-config/task.json`

`task.json` 必须提前放到 JobManager 可读取的位置，格式见 [配置文件](../docs/overview/source_compile.md#配置文件)。提交后检查作业状态和 JobManager 日志。

## REST API

上传 JAR：

```bash
curl -f -X POST \
  -F 'jarfile=@target/rum-compute-1.0.jar' \
  http://localhost:8081/jars/upload

curl -f http://localhost:8081/jars
```

从 JAR 列表取得 ID，将下面的 `<jar-id>` 替换为该值：

```bash
curl -f -X POST 'http://localhost:8081/jars/<jar-id>/run' \
  -H 'Content-Type: application/json' \
  -d '{
    "entryClass": "com.tencent.bk.bkmonitor.rum.RumPrecomputeJob",
    "programArgsList": ["--config", "/etc/rum-config/task.json"]
  }'
```

响应中的 `jobid` 用于查询和取消作业：

```bash
curl -f 'http://localhost:8081/jobs/<job-id>'
curl -f -X PATCH 'http://localhost:8081/jobs/<job-id>?mode=cancel'
```

## Savepoint

触发 Savepoint，目标目录必须可由 Flink 集群访问：

```bash
curl -f -X POST 'http://localhost:8081/jobs/<job-id>/savepoints' \
  -H 'Content-Type: application/json' \
  -d '{"target-directory":"file:///opt/flink/state/savepoints"}'
```

使用返回的 `request-id` 查询结果：

```bash
curl -f 'http://localhost:8081/jobs/<job-id>/savepoints/<request-id>'
```

操作完成且返回 `operation.location` 后，才可使用该路径恢复。对兼容状态，在 `/jars/<jar-id>/run` 请求中设置 `savepointPath`：

```json
{
  "entryClass": "com.tencent.bk.bkmonitor.rum.RumPrecomputeJob",
  "savepointPath": "file:///opt/flink/state/savepoints/<savepoint-dir>",
  "programArgsList": ["--config", "/etc/rum-config/task.json"]
}
```

`savepointPath` 是 REST 请求字段，不能通过应用参数 `--fromSavepoint` 传入。旧状态限制见 [恢复与升级](../docs/overview/architecture.md#恢复与升级)，接口定义见 [Flink 2.2 REST API](https://nightlies.apache.org/flink/flink-docs-release-2.2/docs/ops/rest_api/)。
