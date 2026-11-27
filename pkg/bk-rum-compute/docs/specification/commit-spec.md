# 提交信息

```text
<tag>(<scope>): <摘要> (#issue)
```

- `tag` 必填，可选 `feat`、`fix`、`docs`、`style`、`refactor`、`perf`、`test`、`chore`、`merge`。
- `scope` 可选，使用受影响的包或模块名称。
- 摘要不超过 50 字，说明具体改动。
- 有关联 issue 时附上编号。
- 每次提交围绕一个变更。

例如：

```text
fix(serde): 跳过格式错误的 span item (#123)
docs: 简化项目说明和部署文档
```
