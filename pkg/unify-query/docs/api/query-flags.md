# 请求级 Query Flags

所有公共查询、检查和 API 网关代理入口统一支持以下 Header，无需修改原有请求体：

```http
X-Bk-Query-Flags: {"doris_direct_query":true}
```

`doris_direct_query` 为可选布尔值。未传 Header、空 Header、空对象或未传该字段时使用服务默认配置；显式 `true/false` 覆盖默认值。未知字段忽略，非法 JSON、非对象或已知字段类型错误返回 HTTP 400。字段值为 `null` 与未传相同。

```yaml
query:
  flags:
    doris_direct_query: false
```

默认值为 `false`，保持现有 BKBase 查询行为。有效值在公共 middleware 中固定，派生 context 和子查询继承，不受请求中途配置变化影响。内部调用可以使用 `metadata.WithQueryFlags` 固定有效值。

## 后端选择

| 目标配置 | true | false |
|---|---|---|
| 物理表与 BKBase table ID 都存在 | Doris | BKBase |
| 只有物理表 | Doris | Doris |
| 只有 BKBase table ID | BKBase | BKBase |
| 都不存在 | 配置缺失错误 | 配置缺失错误 |

`metadata.SelectDorisQueryBackend` 实现上述规则。该 flag 只影响 Doris 查询偏好，不影响 ES、VM、InfluxDB 或 BKBase 的其他存储。执行失败不能作为缺少配置自动换后端。

## 物理表路由

在 Redis 的 result_table_detail、storage_cluster_records 或请求 tsdb_map 的目标上配置：

```json
{
  "storage_id": "101",
  "storage_type": "bk_sql",
  "cluster_name": "doris-a",
  "db": "1001_logs",
  "measurement": "doris",
  "physical_tables": {
    "main": {
      "database": "logs",
      "table": "app_log_v2",
      "analyzed_fields": ["log"]
    }
  }
}
```

Redis 原始路由的 storage_id 保持原来的整数类型；上面的示例是查询侧 tsdb_map 格式。db 继续保存 BKBase table ID，不能改成物理表名。历史记录各自携带当时的 physical_tables；没有历史物理配置时不会借用当前表，继续走该记录的 BKBase ID。同集群换表通过真实库表区分，不再只按 BKBase RT 去重。

`analyzed_fields` 由路由提供，用于恢复 BKBase 返回的 Analyzed 信息；不根据 TEXT 类型推定分词。应与目标表真实倒排索引和 BKBase storage_config 保持相同口径。未列出的字段按非分词字段处理。

storage_id 对应现有存储配置中的 address、username、password。address 为 Doris FE 的 MySQL 地址 `host:9030`（不是 HTTP 查询地址），账号需要查询及字段描述权限。客户端按集群复用连接池；配置变化时替换旧连接池，凭据不进入路由或 trace。会话时区按现有平台默认口径使用 `+08:00`。

## 实现与兼容范围

公共查询计划根据 flag 和目标配置记录 QueryBackend，实例工厂仅创建已选定的执行器。未传 Header 时默认 false，两种配置都存在则仍走 BKBase；只有物理表配置时按配置回退规则走 Doris。执行失败不会自动重试 BKBase。

Doris 执行器通过 MySQL 协议执行，复用 bksql.Instance 的过滤、聚合、SQL parser、UNION ALL、字段别名、明细和时序结果格式化。原生路径使用分别引用的 database/table 和同连接的 `SET describe_extend_variant_column = true; DESC ...`，并适配结果类型、列顺序和 schema。minuteN 虚拟字段转换为 BKBase 同口径的 FROM_UNIXTIME 表达式；物理表缺少平台日期辅助列时不追加这些条件。JSON/VARIANT 保持字符串中间表示，NULL 保持 nil，BIGINT 不先转换成 float64。

现有日期、分段边界和跨集群执行逻辑保持不变；本次没有实现新的跨集群 DISTINCT、分位数归并，也没有把写入切换时间重新定义为事件时间覆盖范围。

## 分页与检查

SearchAfter 返回的 ResultTableOption 记录所选后端，后续页沿用该后端；物理库表仍从授权路由读取，不接受客户端游标携带任意物理目标。直查的 TableUUID 包含物理库表，隔离同集群历史表游标。继续分页应完整回传 result_table_options。 跨 RT 合并查询在分组前恢复主表游标对应的成员后端，因此切换 Header 不会拆分原合并查询；没有后端标记的旧游标按 BKBase 处理。

滚动下载的会话标识在有效 flag 为 true 时加入标记，默认 false 保留旧标识；切换 Header 会启动另一会话，不混用两种后端的已有偏移。连续下载需保持 Header 和目标路由不变。路由变化期间的跨请求快照一致性不在本阶段保证范围内。

检查接口返回直查的 query_backend 与物理目标；已有非 VM 请求体预览能力仍按原实现返回，不新增一次真实数据执行。字段和标签接口通过公共计划复用相同选路规则。

## 验证

单元与集成测试覆盖默认 BKBase、Header 开启、物理配置缺失回退、同集群历史表区分与 UNION、历史段不继承当前物理表、SearchAfter 后端延续、msgpack 路由往返、原生 SQL 生成、类型/schema 转换及失败传播。数据库执行使用 sqlmock 验证；尚需在部署环境使用真实 Doris 完成连接、版本与字段展开能力的验收。
