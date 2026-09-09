# 在自建构建机发布镜像与 Chart

使用 `scripts/ci-release.sh` 将镜像编译放在现有 Dockerfile 内执行。宿主机需要 Bash、Git、Docker Engine 及 Buildx 插件；打包 Chart 的机器还需要 Helm、curl 和 sha256sum。Go 1.26.7、Node 24、pnpm 11.24.0 由构建镜像提供，不要求宿主机安装。DevOps Agent 所需的 JDK 由构建机自身管理。

amd64 和 arm64 各自在原生 Linux 构建机上执行，不需要 QEMU。每种架构生成 `linkd` 和 `linkd-console` 两个镜像。脚本校验 Docker 服务端架构、成品镜像架构，以及两个镜像的 `version` 命令输出。

## 环境变量与步骤

以下示例中域名为占位符。镜像源、目标仓库和凭据由流水线环境变量传入，不写进 Dockerfile。`RELEASE_ID` 在同一架构的所有步骤保持一致，并须包含流水线和构建编号以避免并发冲突。使用小写字母、数字、点、下划线或短横线，最长 80 个字符。

```bash
export RELEASE_ID=linkd-pipeline-123
export TARGET_ARCH=amd64
export PACKAGE_VERSION=0.1.0-ci.123
export IMAGE_REPOSITORY=registry.example.com/project/images
# 从 CI 凭据管理绑定 REGISTRY_USER、REGISTRY_PASSWORD。
export GO_IMAGE=golang:1.26.7
export RUNTIME_IMAGE=tencentos/tencentos4-minimal
export NODE_IMAGE=node:24-bookworm-slim
export GOPROXY=https://proxy.golang.org,direct
export NPM_REGISTRY=https://registry.npmjs.org

bash pkg/linkd/scripts/ci-release.sh build
bash pkg/linkd/scripts/ci-release.sh push
```

ARM 构建步骤将 `TARGET_ARCH` 改为 `arm64`，并配置对应的 `IMAGE_REPOSITORY`。`PACKAGE_VERSION` 使用不带 `v` 前缀、不带 `+` 元数据的 SemVer，且作为两个镜像的 tag、Chart version 和 appVersion。请为每次发布使用新版本号，避免覆盖已有发布。

两个架构的 clone 应固定同一个 Git 提交。若流水线按分支拉取，Chart 步骤必须比对两个 push 步骤输出的 `revision`；存在差异时停止发布，固定提交后重新构建。脚本输出使用蓝鲸 DevOps 的 `::set-output` 语法，其他 CI 可以从任务 `.release/<RELEASE_ID>-<TARGET_ARCH>/revision` 读取。

## Chart 打包与上传

Chart 步骤复用已成功构建和推送镜像的机器与工作目录。两种架构都选中时，只在 amd64 机器打包；仅选 arm64 时，在 ARM 机器打包。

```bash
# 延续前面构建步骤的环境变量。
export HELM_UPLOAD_URL=https://repo.example.com/helm/api/project/repository/charts
# 从 CI 凭据管理绑定 HELM_USER、HELM_PASSWORD（个人访问 token）。
# 仅当同时构建另一架构时，设置这两个变量，并要求 PEER_REVISION 非空。
export PEER_REVISION='<另一架构 push 步骤输出的完整 Git SHA>'
export PEER_IMAGE_REPOSITORY=registry.example.com/project/images-arm
bash pkg/linkd/scripts/ci-release.sh chart
```

脚本复制 Chart 后，用本次 Console 镜像内的 Node 和 `yaml` 更新副本。默认镜像仓库与节点架构跟随打包机器，并将各架构的 `values-amd64.yaml` / `values-arm64.yaml` 装入 Chart 包。双架构发布使用 ARM 集群时，下载并解压 Chart，安装时添加 `-f linkd/values-arm64.yaml`。覆盖文件同时调整 Linkd、Console 的镜像仓库和 `nodeSelector.kubernetes.io/arch`。

Chart 执行严格 lint、示例配置渲染以及各架构覆盖配置渲染，再打包、输出 SHA256 并通过 `curl -F chart=@...` 上传。此步骤不访问 Kubernetes 集群，不部署服务，不执行数据库迁移。上传接口使用 HTTPS Basic Auth，token 从 stdin 传给 curl，不放入命令参数或磁盘配置文件。上传 POST 不自动重试；若上传超时，应先检查仓库中该版本是否已经存在。

## 清理与网络

在流水线 `finally` 中调用：

```bash
bash pkg/linkd/scripts/ci-release.sh cleanup
```

清理只删除本次成功生成并登记的镜像标签，以及本次任务目录（含临时 Docker 登录配置和 Chart 副本）。标签已指向其他镜像或存在运行中／已停止容器引用时保留。不会使用全局 prune、强制删除，也不清除共享 BuildKit 缓存。流水线可以在脚本退出后删除本次独立 clone 目录；不得删除共享 Git 缓存或其他构建的目录。Agent 被强制终止或主机宕机可能阻止 finally 执行，此时使用相同 `RELEASE_ID` 手工清理。

首次构建仍需访问配置的基础镜像仓库、Go 模块源和 npm registry；本机有网不代表线上可达。完全断网时必须提前将基础镜像及包代理缓存同步到内网。Dockerfile 保留官方默认值，内部镜像源仅在流水线配置。

## 验证

```bash
bash -n pkg/linkd/scripts/ci-release.sh
python3 pkg/linkd/scripts/test_ci_release.py
# 本机需已安装 Console 依赖及 Helm。
NODE_PATH="$PWD/pkg/linkd/console/node_modules" node --test pkg/linkd/scripts/test-prepare-release-chart.cjs
```

上述脚本测试使用隔离的假 Docker，不拉镜像、不推送镜像。真实 amd64 / arm64 镜像构建、仓库认证和上传需要在目标构建池执行后才能确认。
