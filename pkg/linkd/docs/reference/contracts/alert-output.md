# 已移除的 Kafka Alert V1 Hook

状态：2026-10-29。通用 `EventSource.hooks[].type: kafka` 已移除，不再发送完整 Alert V1 快照。
旧 Record、Release、YAML 和管理 API 中残留的声明会被忽略，不校验其参数、不创建客户端、不执行 Hook，
也不生成 push 流水。废弃参数和多余字段不会阻断加载或发布；执行名额和实例名检查也不统计这些声明。

当前 KAC 关闭与投影使用[全局 KAC 插件](kac-alert-projection-v1.md)及
[轻量动作协议](kac-action-delivery-v3.md)。旧 `type: kac` Kafka Hook 仍遵循自己的配置和运行时边界。
完整来源配置见[配置指南](../../guides/configuration.md#eventsource-hooks)。
