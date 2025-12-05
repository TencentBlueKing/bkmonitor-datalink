# Unify-Query 文档中心

欢迎来到 Unify-Query 文档中心！这里包含了项目的完整文档，帮助你更好地理解和使用 Unify-Query。

## 📚 文档目录

### 1. [架构设计文档](./architecture.md)
**适合人群**：架构师、技术负责人、新加入的开发者

**内容概览**：
- 项目概述和核心功能
- 系统整体架构
- 查询流程详解
- 元数据管理
- 存储引擎集成
- 性能优化策略
- 可观测性设计

**快速链接**：[查看架构文档](./architecture.md)

---

### 2. [核心模块文档](./modules.md)
**适合人群**：开发者、代码维护者

**内容概览**：
- HTTP 服务模块详解
- 查询处理模块说明
- 元数据模块功能
- 存储抽象层设计
- 配置管理机制
- 缓存策略
- 追踪实现

**快速链接**：[查看模块文档](./modules.md)

---

### 3. [存储引擎集成文档](./storage-integration.md)
**适合人群**：需要集成新存储引擎的开发者

**内容概览**：
- 存储接口定义
- 集成新存储引擎的步骤
- 现有存储引擎说明（InfluxDB、VictoriaMetrics、Prometheus 等）
- 配置管理
- 最佳实践

**快速链接**：[查看存储集成文档](./storage-integration.md)

---

### 4. [开发指南](./development-guide.md)
**适合人群**：所有开发者

**内容概览**：
- 开发环境搭建
- 项目结构说明
- 代码规范
- 开发流程
- 测试指南
- 调试技巧
- 常见问题

**快速链接**：[查看开发指南](./development-guide.md)

---

### 5. [故障排查指南](./troubleshooting.md)
**适合人群**：运维人员、开发者

**内容概览**：
- 查询问题排查
- 配置问题解决
- 连接问题诊断
- 性能问题分析
- 数据问题处理
- 日志分析
- 监控指标

**快速链接**：[查看故障排查指南](./troubleshooting.md)

---

### 6. [API 文档](./api/relation.md)
**适合人群**：API 使用者

**内容概览**：
- 关系查询 API
- 多资源查询 API
- 请求和响应格式

**快速链接**：[查看 API 文档](./api/relation.md)

---

### 7. [PromQL 文档](./promql/promql.md)
**适合人群**：查询使用者

**内容概览**：
- PromQL 语法说明
- 数据类型
- 操作符
- 内置函数
- 在 Unify-Query 中使用 PromQL

**快速链接**：[查看 PromQL 文档](./promql/promql.md)

---

### 8. [LTTB 降采样文档](./lttb/lttb.md)
**适合人群**：需要了解降采样功能的开发者

**内容概览**：
- LTTB 算法说明
- 降采样使用
- 性能优化

**快速链接**：[查看 LTTB 文档](./lttb/lttb.md)

---

## 🚀 快速开始

### 新用户

如果你是第一次接触 Unify-Query，建议按以下顺序阅读：

1. [架构设计文档](./architecture.md) - 了解整体架构
2. [开发指南](./development-guide.md) - 搭建开发环境
3. [核心模块文档](./modules.md) - 理解核心模块
4. [PromQL 文档](./promql/promql.md) - 学习查询语法

### 开发者

如果你要开发新功能：

1. [开发指南](./development-guide.md) - 开发环境搭建
2. [核心模块文档](./modules.md) - 理解相关模块
3. [存储引擎集成文档](./storage-integration.md) - 如需集成新存储
4. [故障排查指南](./troubleshooting.md) - 遇到问题时参考

### 运维人员

如果你要部署和维护：

1. [架构设计文档](./architecture.md) - 了解系统架构
2. [故障排查指南](./troubleshooting.md) - 问题排查方法
3. [API 文档](./api/relation.md) - API 使用说明

---

## 📖 文档更新

文档会随着项目的发展持续更新。如果你发现文档有错误或需要补充，欢迎：

1. 提交 Issue
2. 提交 Pull Request
3. 联系文档维护者

---

## 🔗 相关资源

- **项目仓库**：https://github.com/TencentBlueKing/bkmonitor-datalink
- **Swagger API**：`docs/swagger.yaml`
- **架构图**：`docs/common/unify-query-arch.png`

---

## 💡 文档使用建议

1. **按需阅读**：根据你的角色和需求，选择相应的文档阅读
2. **结合代码**：阅读文档时，建议结合源代码一起理解
3. **实践验证**：通过实际操作验证文档中的内容
4. **反馈改进**：发现文档问题及时反馈

---

## 📝 文档维护

文档由项目团队共同维护。如果你有好的建议或发现文档问题，欢迎贡献！

---

**最后更新**：2024年

**维护者**：Unify-Query 团队

