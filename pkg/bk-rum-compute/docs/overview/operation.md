# 部署

## 提交方式

| 场景 | 说明 |
| --- | --- |
| Flink CLI | 见 [构建与运行](source_compile.md) |
| Flink Web UI / REST API | 见 [作业提交](../../k8s-deploy/REST%20API%20Submit.md) |
| JobFlow 平台 | 见 [JobFlow 接入](../../k8s-deploy/K8sJobFlow%20Submit.md) |

作业入口为 `com.tencent.bk.bkmonitor.rum.RumPrecomputeJob`，产物为 `target/rum-compute-1.0.jar`。

## Kubernetes 配置

仓库提供 [flink-standalone.yaml](../../k8s-deploy/flink-standalone.yaml) 和 [flink-default.yaml](../../k8s-deploy/flink-default.yaml)，选择一份并按部署环境调整。两份配置均包含：

- 一个 JobManager 和两个 TaskManager。
- 挂载到 `/opt/flink/state` 的 `flink-state` PVC，申请 `ReadWriteMany` 和 `100Gi`，需要对应的存储支持。
- Checkpoint 和 Savepoint 文件目录；调度参数由作业的 `checkpoint.*` 控制。
- JobManager Service 和 Ingress。对外访问前需要配置访问控制。

配置中的 `high-availability.cluster-id` 不是完整的 JobManager HA 配置。Pod 重建后的作业恢复需要单独配置和验证。

## 配置与升级

`--config` 指向 JSON 文件。部署到 Kubernetes 时可通过 Secret 挂载，并让执行入口类的进程读取该文件。

集群作业通过 Flink 的管理接口停止。Web UI 提交后的入口进程不会持续等待作业，不能依赖其 JVM 关闭钩子管理作业。

升级前检查 [状态兼容要求](architecture.md#恢复与升级)。兼容版本可通过 Flink 的 Savepoint 恢复参数提交；旧租户 key 或缺少 `window_id` 的状态需冷启动并处理历史数据。
