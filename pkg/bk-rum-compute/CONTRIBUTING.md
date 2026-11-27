# 贡献指南

提交代码前阅读 [代码风格](docs/specification/code-style.md) 和 [Review 清单](docs/specification/review.md)。构建环境见 [构建与运行](docs/overview/source_compile.md)。

## 修改要求

- 一次修改解决一个明确问题，避免顺带重构。
- 修复缺陷时补充回归测试，新增行为同步对应文档。
- 涉及状态、算子 UID、事件格式或索引变更时，说明兼容性及升级方式。
- 不提交密码、token、私钥或本地运行配置。

## 验证

```bash
mvn compile
mvn test
```

涉及故障恢复链路时，按 [集成测试](docs/overview/integration-tests.md) 验证。PR 说明修改目的、验证结果和已知限制；无法执行的检查应注明原因。

提交与合并方式见 [Git 工作流](docs/specification/git-workflow.md) 和 [提交信息规范](docs/specification/commit-spec.md)。用户可见的变更记录到 [CHANGELOG](CHANGELOG.md)。

本项目使用 [MIT License](LICENSE)，社区交流遵守 [社区公约](CODE_OF_CONDUCT.md)。
