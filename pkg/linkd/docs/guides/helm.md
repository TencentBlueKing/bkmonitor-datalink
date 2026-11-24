# Kubernetes 部署

在现有 Helm release 的 values 中填写已发布的 Linkd 镜像仓库和标签。使用多个 worker 组时，确保 Control Plane、Cleaner、Lifecycle 及初始化 Job 使用同一版本。不同环境的 values 层级和升级命令以实际使用的 Chart 为准。

发布前可用镜像的 `version` 命令核对版本与源码提交。构建与查询命令见[镜像发布指南](image-release.md)，各角色的启动参数和配置见[配置指南](configuration.md)。
