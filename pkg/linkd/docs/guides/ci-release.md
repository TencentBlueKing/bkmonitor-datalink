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

ARM 构建步骤将 `TARGET_ARCH` 改为 `arm64`，并配置对应的 `IMAGE_REPOSITORY`。`PACKAGE_VERSION` 使用不带 `v` 前缀、不带 `+` 元数据的 SemVer，仅作为两个镜像的 tag。请为每次镜像发布使用新版本号，避免覆盖已有镜像；Chart 版本独立维护。

需要发布同一源码的双架构镜像时，两个架构的 clone 应固定同一个 Git 提交。镜像步骤输出 `revision` 供调用方核对；Chart 不再承担镜像版本一致性校验。脚本输出使用蓝鲸 DevOps 的 `::set-output` 语法，其他 CI 可以从任务 `.release/<RELEASE_ID>-<TARGET_ARCH>/revision` 读取。

`fast_git_clone` 等插件可能只导出源码，目标目录不保留 `.git`。此时在调用发布脚本前，将 `GIT_DIR` 指向拉取插件的缓存仓库 `.git`，并核对其 HEAD 与本次 clone 输出的 `commitId` 完全一致；每个 build、push 步骤都需核对，避免共享缓存改变后写入错误的镜像版本信息。部分 DevOps 版本不会在自定义环境变量中展开 `${{ci.workspace}}` 和凭据引用；工作目录应在 Bash 内根据运行时 `$WORKSPACE` 计算，凭据引用由脚本插件解析，并关闭 `set -x`。

## Chart 打包与上传

Chart 按 `deploy/helm/linkd/Chart.yaml` 中的 `version` 打包，并保留仓库定义的 `appVersion`、`values.yaml` 和模板。它作为可重复使用的部署脚本，与本次镜像 tag 解耦；不要求本次镜像已构建或推送，也不需要 Docker、Node 或镜像仓库凭据。`TARGET_ARCH` 在此仅用于任务目录隔离，不会影响 Chart 内容。

```bash
export RELEASE_ID=linkd-pipeline-123
export TARGET_ARCH=amd64
export HELM_UPLOAD_URL=https://repo.example.com/helm/api/project/repository/charts
# 从 CI 凭据管理绑定 HELM_USER、HELM_PASSWORD（个人访问 token）。
bash pkg/linkd/scripts/ci-release.sh chart
```

脚本执行严格 lint 和示例配置渲染，然后原样打包、输出 SHA256 并通过 `curl -F chart=@...` 上传。包名由仓库的 Chart 名称和版本决定，例如 `linkd-0.1.1.tgz`；即使镜像 tag 是 `0.1.0-ci.123`，也不会改写 Chart 版本或镜像默认值。修改 Chart 后需在仓库中维护 `version`；仅重建镜像不需要修改 Chart 版本。同版本重复上传由仓库规则决定，脚本不会自动递增版本或重试上传。

部署时选择 Chart 版本，并用自己的 values 文件指定镜像、认证、存储、集群等参数：

```yaml
# images.yaml：镜像 tag 可以与 Chart 版本不同，两个组件也可分别指定。
global:
  imageRegistry: registry.example.com
image:
  repository: project/images/linkd
  tag: "0.1.0-ci.123"
console:
  image:
    repository: project/images/linkd-console
    tag: "0.1.0-ci.124"
nodeSelector:
  kubernetes.io/arch: amd64
```

```bash
# environment.yaml 按 Chart README 配置环境所需的认证、外部存储等 values。
helm upgrade --install linkd ./linkd-0.1.1.tgz \
  -n linkd --create-namespace -f environment.yaml -f images.yaml
```

ARM 部署时将两个镜像 repository 改为对应的 ARM 仓库，并将节点架构设为 `arm64`。流水线不再生成带有本次镜像 tag 的架构覆盖文件。其他可覆盖的配置见 [Chart README](../../deploy/helm/linkd/README.md)。

打包上传不会访问 Kubernetes 集群、部署服务或执行数据库迁移。上传接口使用 HTTPS Basic Auth，token 从 stdin 传给 curl，不放入命令参数或磁盘配置文件。上传 POST 不自动重试；若上传超时，应先检查仓库中该版本是否已经存在。

## 清理与网络

在保证失败或取消后也执行的清理步骤中调用：

```bash
bash pkg/linkd/scripts/ci-release.sh cleanup
```

流水线按“镜像构建 → Chart 构建 → Finally 清理”组织。Chart 只需打包一次，可在有 Helm 的机器上运行。不要让未选择的架构在 Finally 中复用一个没有启动的构建 Job：平台会在执行 Bash 条件判断前因缺少节点而报错。当前各架构构建池只有一台机器时，清理可直接使用对应池，并在脚本内跳过未选架构；构建池扩容后必须明确定位本次构建所在节点，避免清理落到其他机器。

清理只删除本次成功生成并登记的镜像标签，以及本次任务目录（含临时 Docker 登录配置和 Chart 副本）。标签已指向其他镜像或存在运行中／已停止容器引用时保留。不会使用全局 prune、强制删除，也不清除共享 BuildKit 缓存。流水线可以在脚本退出后删除本次独立 clone 目录；不得删除共享 Git 缓存或其他构建的目录。Agent 被强制终止或主机宕机可能阻止 finally 执行，此时使用相同 `RELEASE_ID` 手工清理。

首次构建仍需访问配置的基础镜像仓库、Go 模块源和 npm registry；本机有网不代表线上可达。完全断网时必须提前将基础镜像及包代理缓存同步到内网。Dockerfile 保留官方默认值，内部镜像源仅在流水线配置。

## 验证

```bash
bash -n pkg/linkd/scripts/ci-release.sh
python3 pkg/linkd/scripts/test_ci_release.py
# 本机需已安装 Console 依赖及 Helm。
NODE_PATH="$PWD/pkg/linkd/console/node_modules" node --test pkg/linkd/scripts/test-release-chart.cjs
```

镜像脚本测试使用隔离的假 Docker，不拉镜像、不推送镜像。Chart 测试使用真实 Helm 验证源码版本、原样打包和安装时覆盖镜像，上传由假 curl 拦截，不访问仓库。真实 amd64 / arm64 镜像构建、仓库认证和上传需要在目标构建池执行后才能确认。
