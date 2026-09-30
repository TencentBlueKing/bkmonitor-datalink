# Git 工作流

## 分支

| 分支 | 用途 |
| --- | --- |
| `master` | 主分支 |
| `feature/*` | 功能修改 |
| `hotfix/*` | 紧急修复 |
| `release/vX.Y.Z` | 发布准备 |

## 提交与合并

1. 从 `master` 创建工作分支，一次提交对应一个变更。
2. 提交前完成相关检查并更新文档，格式见 [提交信息](commit-spec.md)。
3. 创建 PR，说明修改内容和验证结果。
4. CI 通过并经至少一名 reviewer 批准后，以 squash 方式合并。
5. 删除已合并的工作分支；紧急修复同步到仍在维护的发布分支。

同步主分支时优先 rebase，逐项解决冲突，不用整批 `ours` / `theirs` 覆盖。不要 force push 已合并的分支。

发布分支合并后使用 `vX.Y.Z` 标签，并更新 [CHANGELOG](../../CHANGELOG.md)。
