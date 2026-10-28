# p130_15710 K8s 回放样本

样本来自 2026-10-28 经 `kingeye-admin` 授权只读取得的 test-bkee5 / system 数据：

- KAC 告警：`linkd-3487ca7d-81fe-50f4-b788-c4b6d194b425`
- Linkd Alert：`alert-20261027073759-34ehhne7pqlem`
- 恢复 Event：`event-20261027153602-lq6rnt7fcf4lo`
- Namespace / Cluster：按该 Event 的自然维度读取的当前 OneModel 查询结果；当前 Container 查询无结果。

`k8s_p130_15710.json` 保留 Alert 的 opening 冻结丰富、恢复 Event 的定位输入与冻结丰富。
去除 Event 原始来源消息和内部处理状态，以及 OneModel 无关属性（如 IP、版本和集群状态）。
实例快照只保留租户、模型、实例身份与定位/业务/名称字段。固定预期独立写在测试代码中，
不从当前实现生成；普通测试不访问网络、共享数据库或仓库文档。

`TestK8sFrozenAlertCompatibilityProjection` 重放冻结 Alert 到 KAC 的实际转换入口；
`TestK8sRecoveryReplayWithMissingContainer` 通过生产 K8s Reader 与本地 OneModel 快照，
重放恢复 Event 的 K8s 步骤，并验证有效结果到 KAC 的转换。恢复步骤重放保留
resource 先前产生的残缺模型身份，不重跑策略、指标和完整 Lifecycle。

当前 Namespace / Cluster 快照用于证明容器失败时可以独立获得上下文，不能证明恢复发生时
依赖的历史状态。所有结果均为本地重放，不写远端，不改 opening Alert，也不回填已有 KAC 文档。
