# 通用告警 DSL 检索

状态：2026-10-26。控制面提供 `POST /api/v1/alerts/search`，使用现有管理 JWT：
`Internal-Token: Bearer <JWT>`。Worker Token 无权调用；租户授权由受信管理调用方完成，
`bk_tenant_id` 强制限定本次检索范围。

接口直接查询 Linkd 原生 Alert，覆盖当前部署的 Active 与 History 查询目标，不依赖 KAC 投影。
当前仅 Elasticsearch Repository 支持该接口；MySQL 模式返回 `501`。
按来源轮询继续使用原有 `GET /api/v1/alerts`。

## 请求

```json
{
  "bk_tenant_id": "tenant-a",
  "query": {
    "bool": {
      "filter": [
        {"term": {"event_source_id": "built_in_bk"}},
        {"terms": {"status": ["active", "recovered"]}},
        {"term": {"labels.strategy_id": "123"}},
        {"range": {"create_at": {"gte": "2026-10-25T00:00:00+08:00", "lt": "2026-10-26T00:00:00+08:00"}}}
      ]
    }
  },
  "limit": 100
}
```

| 字段 | 含义 |
| --- | --- |
| `bk_tenant_id` | 必填，最长 64 字节，限定结果所属租户 |
| `query` | 可选的 Elasticsearch JSON Query DSL 子句；省略等同于 `match_all`。必须为对象，不能为字符串、数组或 `null` |
| `limit` | 省略为 100，允许 1–500 |
| `after` | 上一页响应的 `next`，第一页省略，最长 160 字节 |

`query` 是 `_search` 的查询子句，不是完整搜索请求。支持 `bool`、`term`、`terms`、`range`、
`exists`、`query_string` 等当前 Elasticsearch 版本支持的查询。查询值直接保留 JSON 类型与精度。
不接受客户端指定索引、连接、`size`、`from`、`sort`、`aggs`、`_source` 或 PIT。
具体 DSL 语法错误由 Elasticsearch 判定并返回 `400`，接口不另外维护一套语法解析器。

服务端将原始 `query` 作为完整子句嵌入强制租户过滤中，不改写用户 `bool.should` 的默认匹配语义。
在 DSL 中指定其他租户也不能扩大结果范围。
布尔查询语义见 [Elasticsearch 官方说明](https://www.elastic.co/guide/en/elasticsearch/reference/7.17/query-dsl-bool-query.html)。

## 可查询字段

查询能力由当前 Alert mapping 决定，本次接口不改变索引结构。

| 字段 | 当前索引行为 |
| --- | --- |
| `alert_id`、`bk_tenant_id`、`event_source_id`、`fingerprint`、`status`、`severity` | keyword，适合精确条件与组合条件 |
| `subject_system`、`subject_type`、`subject_id`、`source_event_id`、`source_alert_id`、`enrich_status` | keyword |
| `create_at`、`begin_at`、`last_occurred_at`、`update_at`、`end_at` | date_nanos，支持时间范围 |
| `labels.<key>`、`dimensions.<key>` | flattened，值按关键词处理；数字标签建议使用字符串条件 |
| `shield.active`、`merge.role` 等策略字段 | 按已有策略 mapping 查询 |
| `title`、`content`、`subject_name`、`end_reason` | 只保存，不建立可检索索引 |
| `extra_data`、`enrich` | 不建立内部字段索引，不能直接检索其中任意 JSON 路径 |

因此，接口支持原生 DSL，但当前不支持对标题、正文做全文检索。

## 响应与分页

返回 `200`：

```json
{
  "items": [],
  "next": ""
}
```

`items` 保存完整原生 Alert，不包含物理索引、ES 文档 ID 或 CAS token。结果固定按 `alert_id` 升序。
`next` 非空时，将其作为下一次请求的 `after`，并保持原租户和查询条件；为空表示本次读取无后续页。

归档时 Active 与 History 可能同时保存同一个终态 Alert，查询按 `alert_id` 折叠。
本接口不返回告警总数：ES 的 `hits.total` 是折叠前的文档数，不能当成去重后的告警数量。
依据见 [Elasticsearch collapse 说明](https://www.elastic.co/guide/en/elasticsearch/reference/7.17/collapse-search-results.html)。
分页不持有事务快照，期间发生的状态更新、归档和新告警可能改变后续匹配范围。
接口不创建索引、不主动 refresh；搜索可见性受现有 ES refresh 周期影响。

## 预算与错误

响应使用 `Cache-Control: no-store`。通过 `dispatch.alert_search` 配置每进程并发、查询超时、请求与响应大小，
默认分别为 2、5 秒、1 MiB 和 8 MiB。`timeout_seconds` 为 1–10，受管理接口十秒总预算约束。
后端同时沿用 Repository 的请求和响应字节预算。配置示例见[配置指南](../../guides/configuration.md#动态-eventsource-与调度)。

| 状态码 | 含义 |
| --- | --- |
| 400 | 租户、分页、JSON 或 DSL 无效，或存在未知顶层字段 |
| 401 | 管理 JWT 校验失败 |
| 413 | 请求正文超过预算 |
| 429 | 检索并发已满，不排队等待 |
| 501 | 当前 Repository 不支持 Elasticsearch DSL |
| 502 | Elasticsearch 查询失败、返回部分分片结果、返回超时结果、解码失败或结果页超过字节预算 |
| 503 | 查询服务未装配 |
| 504 | 查询 Context 超时 |

ES 的 HTTP 200 若包含超时或分片失败，不会被转换为成功空列表。
客户端错误不回显 ES 原始 reason、查询值或连接信息。

按检索条件批量关闭见[主动关闭 API](alert-close.md#按-filter-批量关闭)。
