# OneModel 调试查询 API

控制面提供 OneModel 领域查询，复用正式 CMDB 丰富的类型化过滤、关联和租户校验。
Console 通过 `/local-api/onemodel/*` 代理以下接口，管理 token 只保留在服务端。
所有接口要求 `Authorization: Bearer <dispatch.api_token>`；worker token 无权访问。
响应使用 `Cache-Control: no-store`，不允许客户端指定资源连接、索引或原生 ES DSL。

## 实例分页

`POST /api/v1/onemodel/search`

```json
{
  "bk_tenant_id": "system",
  "model_id": "cw-Host",
  "where": {
    "field": "attributes.bk_host_innerip",
    "type": "keyword",
    "operator": "eq",
    "value": "10.0.0.1"
  },
  "limit": 50
}
```

`bk_tenant_id`、`model_id` 必填；`where` 省略或 `{}` 表示该租户模型的全部实例。
类型化条件支持 `all/any/not`，沿用 [CMDB 规则](../../design/custom-enrichment.md) 的 OneModel Filter，
但值为直接字面量，不使用丰富规则中的 JSONPath 或 literal 包装。
`limit` 默认 50，范围 1–200；返回 `items`、可选 `next_cursor` 和 `elapsed_milliseconds`。
实例文档包含 `bk_tenant_id/model_id/model_inst_id/entity_uid/attributes` 及可用的展示字段。

下一页保持租户、模型、条件和页大小不变，并传入上页 `next_cursor` 到 `cursor`。
排序为字符串实例 ID、物理索引升序；PIT 固定查询视图，采用 `search_after`，支持 ES 7.10 起的现有兼容范围。
签名游标绑定全部查询条件，不能修改或跨租户复用。快照每次访问保活一分钟；
控制面重启后原游标失效，需重新查询。没有 `next_cursor` 表示已结束，并已尝试释放快照。
单次 ES 响应最多 1 MiB；大属性对象导致响应超限时应减小页大小。

## 关联查询

`POST /api/v1/onemodel/related`

```json
{
  "bk_tenant_id": "system",
  "roots": [{"model_id": "cw-Host", "model_inst_id": "101"}],
  "relation": "belongs",
  "direction": "out",
  "query": {"model_id": "cw-Biz", "where": {}, "limit": 1024}
}
```

`relation` 为实际关联标识；`direction` 支持 `out/in/both`。
起点和目标统一绑定请求租户。目标 `query.limit` 默认及最大值为 1024；
SDK 每个查询方向最多读取 1024 条边，合并后的目标实例最多 1024 个。
超过任何上限都会失败，不返回截断的成功结果。响应字段与实例查询相同，但没有分页游标。
正式 CMDB 丰富继续使用有界完整查询，不使用调试分页接口。

## 释放快照

`POST /api/v1/onemodel/close` 接收 `{"bk_tenant_id":"system","cursor":"<next_cursor>"}`，返回 `{"closed":true}`。
页面改变条件、重新查询或离开时释放未读完的快照；结束、失败和取消也会尝试释放。
释放失败不覆盖原查询结果，ES 在一分钟保活期后回收遗留快照。

## 预算与错误

每个控制面进程最多同时执行 4 个调试请求，不排队；单请求超时 10 秒，清理额外使用至多 2 秒。
查询连接池独立于 Lifecycle，配置为每节点最多 4 个连接；只配置 OneModel 就可使用，不要求 MySQL 或 Redis 资源。

| 状态码 | 语义 |
| --- | --- |
| 400 | 参数、过滤或游标无效；请求包含未知字段 |
| 401 | 管理身份未通过校验 |
| 410 | PIT 或游标已过期，需要重新查询 |
| 422 | 关联结果或边超过上限 |
| 429 | 调试并发预算已满 |
| 503 | 未配置 `resources.onemodel` |
| 504 | 请求超时或取消 |
| 502 | 外部数据源失败、响应超限或身份校验失败 |

错误结构为 `{"error":{"message":"说明"}}`。HTTP 200 中的 ES `timed_out`、`_shards.failed`
同样视为失败；不会展示为无数据，也不会生成下一页游标。错误不包含后端凭据或原始响应。

## 可选真实 ES 验证

设置 `LINKD_TEST_ELASTICSEARCH_URL`、`LINKD_TEST_ONEMODEL_TENANT_ID` 和
`LINKD_TEST_ONEMODEL_MODEL_ID` 后执行：

```bash
go test -count=1 -run TestElasticsearchOneModelPagination ./internal/onemodel
```

可通过 `LINKD_TEST_ELASTICSEARCH_API_KEY` 提供认证。测试只读取现有实例，不创建或删除业务数据，
最多读取两页并关闭 PIT。未配置环境变量时跳过；只有一个实例的模型只能验证首个终止页。
