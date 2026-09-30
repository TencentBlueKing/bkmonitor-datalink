# 索引命名

`rum-compute` 为 Session 和 View 分别生成预计算索引。名称由 `PrecalculateFamily` 和 `NamingPolicy` 生成，需与 BK-Monitor 的存储配置一致。

## 索引与分表

| 对象 | 逻辑表 | 索引集 | 写入目标 |
| --- | --- | --- | --- |
| View | `rum_global_view.precalculate_auto_{N}` | `rum_global_view_precalculate_auto_{N}` | `write_{yyyyMMdd}_rum_global_view_precalculate_auto_{N}` |
| Session | `rum_global_session.precalculate_auto_{N}` | `rum_global_session_precalculate_auto_{N}` | `write_{yyyyMMdd}_rum_global_session_precalculate_auto_{N}` |

- `N` 从 1 开始，分表数由 `precalculate.dispersed-count` 设置，默认 5。
- 日期使用 UTC，在窗口首次事件创建状态时确定，之后不随乱序事件变化。
- 逻辑表名中的 `.` 转为 `_` 后得到索引集名称。
- `NamingPolicy.searchPattern` 返回 `{indexSet}*`，用于存储侧读名称。直接查询作业写入的物理索引时，需使用包含 `write_` 日期前缀的模式。

分表选择使用 Rendezvous Hash，对各候选节点计算 SHA-1 权重并取最大值：

```text
nodeKey  = {clusterId}-{logicalTable}
routeKey = {bkBizId}:{appName}
weight   = SHA-1({nodeKey}:{routeKey})
```

`clusterId` 来自 `precalculate.es.cluster-id`。修改集群 ID 或分表数可能改变路由，需与已有索引和查询配置一起处理。

## 文档 ID

```text
entityKey = bkBizId + ":" + appName.length() + ":" + appName
          + entityId.length() + ":" + entityId
_id       = hex_lowercase(SHA-256(UTF-8(entityKey))) + ":" + window_id
```

`entityId` 为 Session ID 或 View ID。长度按 Java `String.length()` 计算，编码由 `RumEntityKey` 实现，身份字段不修剪或截断。

`window_id` 为窗口创建时生成的 UUID，随状态保存。同一窗口的快照复用 ID；重开窗口使用新 ID。该字段不参与聚合分组或分表选择。

## 查询与模板

查询物理索引时可使用以下模式，并按业务、应用、实体 ID 和窗口 ID 过滤：

```text
write_*_rum_global_session_precalculate_auto_*
write_*_rum_global_view_precalculate_auto_*
```

模板文件：

- [Session 模板](elasticsearch/rum-session-precalculate-auto-template.json)
- [View 模板](elasticsearch/rum-view-precalculate-auto-template.json)

模板设置 `dynamic: false`。新增输出字段时需要同步模板，并为已有索引补充映射。字段定义见 [预计算字段](Precalculate-index-fields.md)，旧数据处理见 [恢复与升级](overview/architecture.md#恢复与升级)。
