# 使用与部署

本目录面向 Linkd 的使用者、部署者和运维人员，内容应只依赖公开配置和稳定接口，不要求读者理解内部实现。

项目当前处于早期开发阶段，尚未形成可用于生产环境的完整部署方案，也不承诺未发布内部配置和存储结构的向后兼容。现阶段已经实现的本地进程配置、校验和启动方式见：

- [部署模式与进程拓扑](../design/deployment.md)（已确认方向与当前实现边界）；
- [项目 README 的容器镜像](../../README.md#容器镜像)：在 `pkg/linkd` 独立构建 `tencentos/tencentos4-minimal` 运行时镜像；
- [手动构建与发布镜像](image-release.md)：GitHub Actions 手动构建 Linkd / Console、GHCR 权限与版本标签；
- [配置与启动](configuration.md)；
- [Helm 部署与 worker 分组](helm.md)：三角色部署、独立配置 Secret、可选 Console 与认证入口；
- [Standard Event 模拟器](event-generator.md)；
- [使用 PM2 托管本地 Linkd 拓扑](pm2.md)；
- [Linkd Console 运维调试工具](console.md)；
- [主机推送告警 Enrich 示例](host-alert-enrich-example.md)；
- [核心任务调度验证流程](task-scheduling-validation.md)。

后续按实际交付能力补充以下文档，而不提前记录尚未实现的操作步骤：

- 生产部署、容量规划和高可用；
- 升级、回滚和兼容性说明；
- 生产可观测性、日常运维和故障排查。

面向内部开发者的模块实现细节应放在 [功能模块](../modules/README.md)，跨模块方案应放在 [设计文档](../design/README.md)。
