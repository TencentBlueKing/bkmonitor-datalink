# JobFlow 接入

本项目提供携带作业 JAR 的 Dockerfile。JobFlow CLI、提交镜像和集群初始化方式由部署平台提供，本仓库没有对应实现。

## 构建应用镜像

在项目根目录执行，替换示例镜像仓库地址：

```bash
mvn clean package
cp target/rum-compute-1.0.jar k8s-deploy/docker/rum-compute-1.0.jar

docker build \
  -t registry.example.com/your-team/rum-compute:1.0 \
  k8s-deploy/docker

docker push registry.example.com/your-team/rum-compute:1.0
```

[k8s-deploy/docker/Dockerfile](docker/Dockerfile) 将 JAR 放在 `/opt/app/app.jar`。镜像基于 Alpine，不包含 Java 或 Flink 运行时。

同目录的 `task.example.json` 是脱敏后的样例配置（值全部置空），可作为模板参考，**不进镜像**；真实配置由平台通过 Secret 挂载到执行入口类进程可读取的位置，路径见下表"应用参数"。同目录的 `task.json` 是真实运行配置模板，被 `.gitignore` 忽略，禁止提交到 git。

修改 Java 后必须重新执行 `mvn package` → `cp` → `docker build` → `docker push` 四步，JAR 在 git 忽略之列，旧文件会被新构建覆盖。

## 平台需要的参数

| 项目 | 值或要求 |
| --- | --- |
| 应用镜像 | 上一步推送的镜像 |
| 镜像内 JAR | `/opt/app/app.jar` |
| 入口类 | `com.tencent.bk.bkmonitor.rum.RumPrecomputeJob` |
| 应用参数 | `--config /etc/rum-config/task.json` |
| Flink 版本 | `2.2.1`，由平台准备运行镜像 |
| 配置文件 | 扁平 JSON，挂载到执行入口类的进程可读取的位置 |

配置格式见 [构建与运行](../docs/overview/source_compile.md#配置文件)。含凭据的配置可通过 Kubernetes Secret 挂载。

## 文件传递

如果平台通过 init container 提取 JAR：

1. 从应用镜像复制 `/opt/app/app.jar` 到共享目录。
2. 提交容器读取该目录中的 JAR。
3. 由平台将 JAR 和配置提供给实际运行作业的 Flink 进程。

`emptyDir` 只在同一 Pod 内共享，提交容器中的文件不会自动出现在其他 JobManager/TaskManager Pod。使用 Application 模式时，需要按平台约定分发文件，或将 JAR 放入 Flink 运行镜像。

平台若提供 `--local` 参数，路径应指向提交容器内的 JAR；具体语法以该平台为准。

提交后的状态查看、停止和升级使用平台提供的管理入口，状态兼容限制见 [恢复与升级](../docs/overview/architecture.md#恢复与升级)。
