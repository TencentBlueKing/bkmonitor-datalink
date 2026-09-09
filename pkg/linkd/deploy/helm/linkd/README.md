# Linkd Helm Chart

默认部署 Control Plane 和按 `clusters` 分组的 Cleaner/Lifecycle，不支持 all-in-one。
Console、Event Generator、Ingress 和 ServiceMonitor 可选，所有中间件外置。

外部 Elasticsearch 最低要求 **7.10**；Redis 建议使用 **7.2 系列**（单节点或 Sentinel）。
完整版本边界与实测范围见 [中间件版本要求](../../../docs/guides/configuration.md#中间件版本要求)。

Chart 版本为 `0.1.3`。Linkd 与 Console 的默认镜像分别为 `ghcr.io/tencentblueking/bkmonitor-datalink/linkd:0.1.3`
和 `ghcr.io/tencentblueking/bkmonitor-datalink/linkd-console:0.1.3`；可选模拟器使用独立的 `linkd-eventgen:0.1.1`。
镜像仓库与版本均可通过 values 覆盖。
手动打包与 GitHub Actions Artifacts 下载见[发布指南](../../../docs/guides/image-release.md#helm-chart-打包)。

完整操作说明见 [Helm 部署指南](../../../docs/guides/helm.md)。
参数说明见 [values.yaml](values.yaml)，示例见：

- [外部服务](examples/external-services.yaml)
- [default / alarmd worker 组](examples/clusters.yaml)
- [共享已有 Secret 的不同 key](examples/existing-secrets.yaml)
- [Console、Ingress、Basic Auth](examples/console-ingress.yaml)
- [Event Generator 直连 Kafka](examples/eventgen-direct.yaml)：直接配置 brokers/topic，无需配置文件。
- [多个可选 Event Generator](examples/eventgen.yaml)：默认关闭，支持持续 Deployment 和有限周期 Job；[独立来源配置](examples/eventgen-config.yaml) 用于创建配置 Secret。
- [异步 migrate Job](examples/migrate-async.yaml)：默认等待模式使用 pre-install/pre-upgrade Hook，watch=false 使用普通 Job。
- [ServiceMonitor 指标采集](examples/servicemonitor.yaml)：覆盖 Control Plane 和全部 worker 组的 `/metrics`。

在模块根目录执行 `make helm-check`。需要 Helm 3、Node.js、已安装的 Console 依赖和 Go。
测试只构建配置校验器、执行本地渲染和校验，不连接 Kubernetes 或中间件。
