# 在自建构建机发布镜像

使用 `scripts/ci-release.sh` 将镜像编译放在现有 Dockerfile 内执行。宿主机需要 Bash、Git、Docker Engine 及 Buildx 插件。Go 1.26.10 由构建镜像提供，不要求宿主机安装。DevOps Agent 所需的 JDK 由构建机自身管理。

amd64 和 arm64 各自在原生 Linux 构建机上执行，不需要 QEMU。每种架构只生成 `linkd` 镜像。脚本校验 Docker 服务端架构、成品镜像架构，以及镜像的 `version` 命令输出。

## 环境变量与步骤

以下示例中域名为占位符。镜像源、目标仓库和凭据由流水线环境变量传入，不写进 Dockerfile。`RELEASE_ID` 在同一架构的所有步骤保持一致，并须包含流水线和构建编号以避免并发冲突。使用小写字母、数字、点、下划线或短横线，最长 80 个字符。

```bash
export RELEASE_ID=linkd-pipeline-123
export TARGET_ARCH=amd64
export PACKAGE_VERSION=1.0.0
export IMAGE_REPOSITORY=registry.example.com/project/images
# 从 CI 凭据管理绑定 REGISTRY_USER、REGISTRY_PASSWORD。
export GO_IMAGE=golang:1.26.10
export RUNTIME_IMAGE=tencentos/tencentos4-minimal
export GOPROXY=https://proxy.golang.org,direct

bash pkg/linkd/scripts/ci-release.sh build
bash pkg/linkd/scripts/ci-release.sh push
```

ARM 构建步骤将 `TARGET_ARCH` 改为 `arm64`，并配置对应的 `IMAGE_REPOSITORY`。`PACKAGE_VERSION` 使用不带 `v` 前缀、不带 `+` 元数据的 SemVer，且作为 Linkd 镜像的 tag。请为每次发布使用新版本号，避免覆盖已有发布。

两个架构的 clone 应固定同一个 Git 提交。脚本输出使用蓝鲸 DevOps 的 `::set-output` 语法，其他 CI 可以从任务 `.release/<RELEASE_ID>-<TARGET_ARCH>/revision` 读取。

`fast_git_clone` 等插件可能只导出源码，目标目录不保留 `.git`。此时在调用发布脚本前，将 `GIT_DIR` 指向拉取插件的缓存仓库 `.git`，并核对其 HEAD 与本次 clone 输出的 `commitId` 完全一致；每个 build、push 步骤都需核对，避免共享缓存改变后写入错误的镜像版本信息。部分 DevOps 版本不会在自定义环境变量中展开 `${{ci.workspace}}` 和凭据引用；工作目录应在 Bash 内根据运行时 `$WORKSPACE` 计算，凭据引用由脚本插件解析，并关闭 `set -x`。

## 清理与网络

在保证失败或取消后也执行的清理步骤中调用：

```bash
bash pkg/linkd/scripts/ci-release.sh cleanup
```

蓝鲸 DevOps 可将清理放在每个架构的构建 Job 末尾，配置 `if: ALWAYS`。不要为未选择的架构创建需要复用构建节点的全局 Finally Job：该架构没有分配节点时，平台会在执行 Bash 条件判断前报错。

清理只删除本次成功生成并登记的镜像标签，以及本次任务目录（含临时 Docker 登录配置）。标签已指向其他镜像或存在运行中／已停止容器引用时保留。不会使用全局 prune、强制删除，也不清除共享 BuildKit 缓存。流水线可以在脚本退出后删除本次独立 clone 目录；不得删除共享 Git 缓存或其他构建的目录。Agent 被强制终止或主机宕机可能阻止 finally 执行，此时使用相同 `RELEASE_ID` 手工清理。

首次构建仍需访问配置的基础镜像仓库和 Go 模块源；本机有网不代表线上可达。完全断网时必须提前将基础镜像及包代理缓存同步到内网。Dockerfile 保留官方默认值，内部镜像源仅在流水线配置。

## 验证

```bash
bash -n pkg/linkd/scripts/ci-release.sh
python3 pkg/linkd/scripts/test_ci_release.py
```

上述脚本测试使用隔离的假 Docker，不拉镜像、不推送镜像。真实 amd64 / arm64 镜像构建、仓库认证和上传需要在目标构建池执行后才能确认。
