# 手动构建与发布镜像

仓库根目录的 [Linkd images workflow](../../../../.github/workflows/linkd-images.yml) 复用
Linkd 和 Console 的 Dockerfile，构建 `linux/amd64` 镜像并推送到 GitHub Container Registry（GHCR）。
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

打开 GitHub 仓库 **Actions → Linkd images → Run workflow**，选择待构建的分支，再填写：

| 参数 | 默认值 | 含义 |
| --- | --- | --- |
| `component` | `all` | 同时构建两个镜像；也可选 `linkd` 或 `linkd-console` |
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

Linkd 二进制的 `linkd version` 使用 `version`，留空时使用构建标签；两个镜像均记录 OCI version、
revision 和 source 标签。Console 的 `package.json` 版本不参与镜像版本推导。

Helm 的 `image.tag` / `console.image.tag` 使用同一交付标签；需要精确回滚时分别填写
`image.digest` / `console.image.digest`。镜像仓库与拉取配置见 [Helm 部署](helm.md)。
