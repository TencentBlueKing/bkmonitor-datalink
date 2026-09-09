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
| `chart_version` | 空 | 仅 `helm` 使用；覆盖 Chart 包版本，必须是 SemVer；留空使用 Chart.yaml 的 version |

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

Linkd 二进制的 `linkd version` 使用 `version`，留空时使用构建标签；两个镜像均记录 OCI version、
revision 和 source 标签。Console 的 `package.json` 版本不参与镜像版本推导。

Helm 的 `image.tag` / `console.image.tag` 使用同一交付标签；需要精确回滚时分别填写
`image.digest` / `console.image.digest`。镜像仓库与拉取配置见 [Helm 部署](helm.md)。

## Helm Chart 打包

选择 `component=helm`，本次运行只执行 Chart 校验、渲染、打包和 Artifact 上传，镜像构建 job 跳过。
反过来，选择 `all`、`linkd` 或 `linkd-console` 时，Chart job 跳过。

```bash
gh workflow run linkd-images.yml --ref feat/linkd-dev \
  -f component=helm -f chart_version=0.1.0
```

流程固定使用 Helm 3.17.3，先用仓库的外部服务、worker 分组、Console 和 ServiceMonitor 示例执行
`helm lint --strict` 和 `helm template`，随后打包 Chart 源目录。示例仅用于校验，不写入包内默认 values，
也不会把渲染出来的 Secret 上传到 Artifacts。验证不连接 Kubernetes 或真实中间件。

Actions Summary 提供下载链接，Artifact 名称为 `linkd-chart-<run-id>.<attempt>`，保留 30 天，包含：

```text
linkd-0.1.0.tgz
SHA256SUMS
```

Chart 包默认沿用仓库的 `version: 0.1.0`、`appVersion: "0.1.0"` 和两个 GHCR `0.1.0` 镜像。
`chart_version` 只改变包版本，`version` 只用于镜像构建；两者都不会自动修改 Chart 的默认镜像。
发布下一版镜像后，需要显式更新 `values.yaml` 和 `Chart.yaml`，再单独触发 Helm 打包。

下载后仍需提供环境配置与已有认证 Secret：

```bash
sha256sum -c SHA256SUMS
helm upgrade --install linkd ./linkd-0.1.0.tgz \
  --namespace linkd --create-namespace -f /secure/linkd-values.yaml
```

Artifact 保留期受仓库或组织策略限制，见 [GitHub Artifacts 文档](https://docs.github.com/en/actions/tutorials/store-and-share-data)。
