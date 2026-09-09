# Linkd Helm Chart

仅部署 Control Plane 和按 `clusters` 分组的 Cleaner/Lifecycle，不支持 all-in-one。
Console、Ingress 和 ServiceMonitor 可选，所有中间件外置。

完整操作说明见 [Helm 部署指南](../../../docs/guides/helm.md)。
参数说明见 [values.yaml](values.yaml)，示例见：

- [外部服务](examples/external-services.yaml)
- [default / alarmd worker 组](examples/clusters.yaml)
- [共享已有 Secret 的不同 key](examples/existing-secrets.yaml)
- [Console、Ingress、Basic Auth](examples/console-ingress.yaml)
- [异步 migrate Job](examples/migrate-async.yaml)：默认等待模式使用 pre-install/pre-upgrade Hook，watch=false 使用普通 Job。
- [ServiceMonitor 指标采集](examples/servicemonitor.yaml)：覆盖 Control Plane 和全部 worker 组的 `/metrics`。

在模块根目录执行 `make helm-check`。需要 Helm 3、Node.js、已安装的 Console 依赖和 Go。
测试只构建配置校验器、执行本地渲染和校验，不连接 Kubernetes 或中间件。
