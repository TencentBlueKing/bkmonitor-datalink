# 更新日志规范

用户可见的变化记录到 [CHANGELOG](../../CHANGELOG.md) 的 `Unreleased` 部分。

- 按 `Added`、`Changed`、`Deprecated`、`Removed`、`Fixed`、`Security` 分类，只保留有内容的分类。
- 每条说明具体变化，有关联 issue 或 PR 时附上编号。
- 配置默认值、字段和状态兼容性变化应写明影响。
- 发版时将待发布条目移到对应版本下，填写日期；最新版本在前。
- 已发布记录需要更正时标明更正内容。

```markdown
## [Unreleased]

### Changed

- 将项目命名统一为 rum-compute。
```
