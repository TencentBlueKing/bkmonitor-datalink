# 手动构建与发布镜像

仓库根目录的 [Linkd images workflow](../../../../.github/workflows/linkd-images.yml) 复用
各组件的 Dockerfile，默认构建 Linkd 的 `linux/amd64`、`linux/arm64` 镜像并推送到 GitHub Container Registry（GHCR）。
`linkd-eventgen` 是独立版本的可选镜像，只有显式选择该组件才构建，不包含在 `all` 中。
每个组件的两个架构共用同一个标签，拉取时按运行节点架构选择镜像，无需设置架构专用 tag。
Workflow 在 amd64 runner 上配置 QEMU，支持 ARM 构建及版本查询；双架构构建超时为 90 分钟。
它只声明 `workflow_dispatch`，提交代码、创建 PR、推送 Git tag 或发布 Release 都不会自动触发。

## 首次准备

1. 将 workflow 合入 GitHub 仓库的默认分支，启用仓库 Actions。
2. 确保组织或仓库策略允许通过 `GITHUB_TOKEN` 写入 Packages，以及使用 workflow 引用的 Actions。
   workflow 已声明 `contents: read` 和构建 job 的 `packages: write`，不需要额外保存 registry 密码。
3. 如果目标 GHCR package 已存在，需在 package 设置中为当前仓库授予 Actions 写入权限。
   新 package 的可见性由 GHCR 管理；需要匿名拉取时，在 package 设置中设为 public。
   私有镜像需为部署环境配置拉取凭据。

GitHub 要求手动 workflow 文件存在于默认分支，才会提供手动触发入口，见
[GitHub 手动运行说明](https://docs.github.com/actions/managing-workflow-runs/manually-running-a-workflow)。
GHCR 权限见 [GitHub 镜像发布说明](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)。

## 手动执行

打开 GitHub 仓库 **Actions → Linkd images → Run workflow**，选择包含源码 的分支（当前为 `feat/linkd-dev`），再填写：
默认分支只登记 Workflow 时不能直接构建；缺少 Dockerfile 会提前报错。

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `component` | `all` | `all` 只构建 Linkd；`linkd` / `linkd-eventgen` 只构建所选镜像 |
| `version` | `1.0.0` | 仅用于 Linkd 的 Docker tag，例如 `v0.1.1-rc.1`；留空只发布唯一构建标签 |
| `eventgen_version` | `0.1.1` | 仅用于 `linkd-eventgen` 的独立 Docker tag；留空只发布唯一构建标签，不继承 `version` |

两个版本字段都只允许合法 Docker tag：最长 128 字符，仅含字母、数字、下划线、点和连字符，首字符不能为点或连字符。

也可在仓库目录通过 GitHub CLI 手动选择分支或 Git tag：

```bash
gh workflow run linkd-images.yml --ref <branch-or-tag> \
  -f component=all -f version=v0.1.0-rc.1
```

镜像构建使用本次触发确定的同一个完整 Git SHA；分支后续推进不会改变本次构建的源码。
产物地址以当前执行 workflow 的仓库为准（owner 和 repository 均转为小写）：

```text
ghcr.io/<owner>/<repository>/linkd:<tag>
ghcr.io/<owner>/<repository>/linkd-eventgen:<eventgen-tag>
```

每个成功的构建都会在 Actions Summary 中输出镜像标签、多架构 index digest、平台和源码 SHA。
部署前应确认目标镜像 job 成功，并记录 digest。流程只构建和推送镜像，不执行 `make check`，发布前应在待发布提交上完成质量门禁。

独立构建模拟器的 `0.1.1` 版本：

```bash
gh workflow run linkd-images.yml --repo TencentBlueKing/bkmonitor-datalink \
  --ref feat/linkd-dev -f component=linkd-eventgen -f eventgen_version=0.1.1
```

模拟器使用 `Dockerfile.eventgen`，同样生成双架构镜像、记录 OCI 标签，并运行 `version` 校验完整 Git SHA。
用法见 [Standard Event 模拟器](event-generator.md)。

## 版本建议

Linkd 使用独立的镜像版本，当前为 `1.0.0`。

| 用途 | 标签示例 | 使用方式 |
| --- | --- | --- |
| 日常开发验证 | `sha-a1b2c3d4e5f6-123456789.1` | `version` 留空；自动包含 SHA 前 12 位、run ID 和 run attempt |
| 候选版本 | `v0.1.0-rc.1` | 手动填写 `version`，同时保留上述构建标签 |
| 正式交付 | `1.0.0` | 将 `version` 设为本次交付的版本号 |

不会自动生成 `latest`、分支名或主次版本浮动标签。显式版本标签仍可被后续手动运行覆盖；
不要复用已交付版本。构建标签区分重跑，但 registry 没有由此获得不可变策略，严格固定产物应使用 digest。
同一源码重建时基础镜像或外部依赖可能变化，不能把相同 Git SHA 当成镜像字节一致的保证。

Linkd 与 Eventgen 镜像均记录 OCI version、revision（完整 Git SHA）和 source 标签，Workflow 将同一个 SHA
通过 `GIT_COMMIT` 构建参数写入镜像内部。两个镜像的 `version` 命令同时显示版本号和 commit；
查询不需要配置文件或连接中间件。

```bash
docker run --rm ghcr.io/tencentblueking/bkmonitor-datalink/linkd:<tag> version
```

```text
version: <version-or-build-tag>
git_commit: <full-git-sha>
```

每次镜像构建结束后，Workflow
从产物 index digest 中解析各架构的子 manifest digest，分别以 `--platform linux/amd64` 和 `--platform linux/arm64` 拉起无网络版本查询，
校验两个架构的镜像内版本及 commit 均与本次构建一致。
未注入 Git 提交的本地构建显示 `git_commit: unknown`。

镜像发布后，在目标环境的部署配置中指定镜像标签。见[Kubernetes 部署](helm.md)。
