# 手动构建与发布镜像和 Chart

仓库根目录的 [Linkd images workflow](../../../../.github/workflows/linkd-images.yml) 复用
Linkd 和 Console 的 Dockerfile，构建 `linux/amd64` 镜像并推送到 GitHub Container Registry（GHCR）。
它只声明 `workflow_dispatch`，提交代码、创建 PR、推送 Git tag 或发布 Release 都不会自动触发。
镜像构建与 Helm 打包通过 `component` 互斥选择，不会在同一次运行中同时执行。

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

打开 GitHub 仓库 **Actions → Linkd images → Run workflow**，选择包含源码或 Chart 的分支（当前为 `feat/linkd-dev`），再填写：
默认分支只登记 Workflow 时不能直接构建；缺少 Dockerfile 或 Chart.yaml 会提前报错。

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `component` | `all` | `all` 只构建两个镜像；`linkd` / `linkd-console` 只构建对应镜像；`helm` 只打包 Chart |
| `version` | 空 | 可选 Docker tag，例如 `v0.1.0-rc.1`；最长 128 字符，只允许字母、数字、下划线、点和连字符，首字符不能为点或连字符 |

也可在仓库目录通过 GitHub CLI 手动选择分支或 Git tag：

```bash
gh workflow run linkd-images.yml --ref <branch-or-tag> \
  -f component=all -f version=v0.1.0-rc.1
```

两个构建使用本次触发确定的同一个完整 Git SHA；分支后续推进不会改变本次构建的源码。
产物地址以当前执行 workflow 的仓库为准（owner 和 repository 均转为小写）：

```text
ghcr.io/<owner>/<repository>/linkd:<tag>
ghcr.io/<owner>/<repository>/linkd-console:<tag>
```

每个成功的构建都会在 Actions Summary 中输出镜像标签、digest 和源码 SHA。
选择 `all` 时两个组件独立执行；一个失败不会撤销另一个已推送的镜像。成套部署前应确认两个 job 均成功，
并记录各自 digest。流程只构建和推送镜像，不执行 `make check`，发布前应在待发布提交上完成质量门禁。

## 版本建议

Linkd 当前处于早期开发阶段，建议 Linkd 与 Console 使用同一套交付版本，便于成套部署和回滚：

| 用途 | 标签示例 | 使用方式 |
| --- | --- | --- |
| 日常开发验证 | `sha-a1b2c3d4e5f6-123456789.1` | `version` 留空；自动包含 SHA 前 12 位、run ID 和 run attempt |
| 候选版本 | `v0.1.0-rc.1` | 手动填写 `version`，同时保留上述构建标签 |
| 正式交付 | `v0.1.0` | 手动填写 `version`；早期阶段继续使用 `0.x` |

不会自动生成 `latest`、分支名或主次版本浮动标签。显式版本标签仍可被后续手动运行覆盖；
不要复用已交付版本。构建标签区分重跑，但 registry 没有由此获得不可变策略，严格固定产物应使用 digest。
同一源码重建时基础镜像或外部依赖可能变化，不能把相同 Git SHA 当成镜像字节一致的保证。

两个镜像均记录 OCI version、revision（完整 Git SHA）和 source 标签，Workflow 将同一个 SHA
通过 `GIT_COMMIT` 构建参数写入镜像内部。两个镜像的 `version` 命令同时显示版本号和 commit；
查询不需要配置文件或连接中间件。Console 的 package.json 版本不参与镜像版本推导，构建信息保存在
镜像内 `build-info.json`，不使用运行时环境变量覆盖。

```bash
docker run --rm ghcr.io/tencentblueking/bkmonitor-datalink/linkd:<tag> version
docker run --rm ghcr.io/tencentblueking/bkmonitor-datalink/linkd-console:<tag> version
```

```text
version: <version-or-build-tag>
git_commit: <full-git-sha>
```

Console 提供受 Basic Auth 保护的 `GET /local-api/version`，返回 `version` 和 `git_commit`。
这与 `/local-api/capabilities` 中的接口 schema version 是不同字段。每次镜像构建结束后，Workflow
使用产物 digest 拉起无网络版本查询，校验镜像内版本及 commit 与本次构建一致。
未注入构建信息的开发产物显示 `dev` / `unknown`；已发布镜像不会因 Workflow 修改而变化。

Helm 的 `image.tag` / `console.image.tag` 使用同一交付标签；需要精确回滚时分别填写
`image.digest` / `console.image.digest`。镜像仓库与拉取配置见 [Helm 部署](helm.md)。

## Helm Chart 打包

选择 `component=helm`，本次运行只执行 Chart 校验、渲染、打包和发布，镜像构建 job 跳过。
反过来，选择 `all`、`linkd` 或 `linkd-console` 时，Chart job 跳过。

```bash
gh workflow run linkd-images.yml --ref feat/linkd-dev \
  -f component=helm
```

流程固定使用 Helm 3.17.3，先用仓库的外部服务、worker 分组、Console 和 ServiceMonitor 示例执行
`helm lint --strict` 和 `helm template`，随后打包 Chart 源目录。示例仅用于校验，不写入包内默认 values，
也不会把渲染出来的 Secret 上传到 Artifacts。验证不连接 Kubernetes 或真实中间件。

Actions Summary 提供 Release 和 Artifact 两个下载入口。Release 的 tag 为 `helm/linkd/v<chart-version>`，
例如 `helm/linkd/v0.1.0`；版本直接读取 `Chart.yaml`，没有单独版本输入。
首次创建的 Git tag 指向首次发布提交，标题为 `Linkd Helm Chart 0.1.0`。
不存在时先创建草稿，上传后回下载校验，确认内容一致才发布；已存在时自动更新两个同名附件与构建说明。
已有 Git tag 不移动，Release 说明中的源码提交和构建链接记录本次包的实际来源。
更新已发布附件不是原子操作，失败时可能已部分更新，可重新触发相同版本修复；首次失败保留草稿供排查。
不会将 Chart 设为整个仓库的 Latest Release，包含预发布标识的 Chart 版本会标记为 prerelease。

正式包通过 Release 附件长期下载，不受 Actions Artifact 保留期限制；删除 Release 或附件仍会使入口失效。
Artifact 名称为 `linkd-chart-<run-id>.<attempt>`，作为保留 30 天的备份。两种入口均包含：

```text
linkd-0.1.0.tgz
SHA256SUMS
```

Chart 包默认沿用仓库的 `version: 0.1.0`、`appVersion: "0.1.0"` 和两个 GHCR `0.1.0` 镜像。
`version` 只用于镜像构建，不会自动修改 Chart 的版本或默认镜像。
发布下一版镜像后，需要显式更新 `values.yaml` 和 `Chart.yaml`，再单独触发 Helm 打包。

下载后仍需提供环境配置与已有认证 Secret：

```bash
sha256sum -c SHA256SUMS
helm upgrade --install linkd ./linkd-0.1.0.tgz \
  --namespace linkd --create-namespace -f /secure/linkd-values.yaml
```

Artifact 保留期受仓库或组织策略限制，见 [GitHub Artifacts 文档](https://docs.github.com/en/actions/tutorials/store-and-share-data)。

在组织仓库运行时，Chart 上传后还会通过 Artifact Metadata API 登记到组织的
`https://github.com/orgs/<owner>/artifacts` 页面，名称为 `linkd-chart`。登记包含 Chart 版本、
`.tgz` 文件本身的 SHA256、所属仓库与本次 Release 链接，并回读确认记录存在。
登记前通过 GitHub 官方 `actions/attest` 生成构建来源证明，关联同一个 `.tgz`、源码提交与 Workflow。
Helm job 单独申请发布 Release 所需的 `contents: write`，以及 `artifact-metadata: write`、
`id-token: write` 和 `attestations: write` 权限；
个人 fork 跳过来源证明和组织级登记。API 回读成功只确认存储记录存在，页面展示仍需单独验证。

Linked artifacts 只存储关联信息，当前其组织列表展示仍未确认，因此登记失败不阻塞 Release 发布，
正式下载入口以 Release 为准。本流程不会在文件删除后自动更新记录状态。接口说明见
[GitHub Linked artifacts 文档](https://docs.github.com/en/code-security/how-tos/secure-your-supply-chain/establish-provenance-and-integrity/upload-linked-artifacts)。
