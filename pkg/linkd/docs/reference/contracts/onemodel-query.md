# OneModel 调试查询 API

控制面提供 OneModel 领域查询，复用正式 CMDB 丰富的类型化过滤、关联和租户校验。
`long` 过滤接受范围内的整数值，包括丰富转换产生的整数浮点值；不因其默认文本采用科学计数法而拒绝，仍拒绝小数、非有限值和超出 int64 范围的值。
Console 通过 `/local-api/onemodel/*` 代理以下接口，管理 JWT 只保留在服务端。
所有接口要求 `Internal-Token: Bearer <JWT>`；worker token 无权访问。
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
ES 排序为字符串实例 ID、物理索引升序，PIT 固定查询视图并采用 search_after。
Doris 按 model_inst_id 字节序进行当前态 keyset 分页，沿用 KAC，不提供跨页 PIT 一致快照。
签名游标绑定后端、租户和全部查询条件，不能修改或跨租户复用。快照每次访问保活一分钟；
控制面重启后原游标失效，需重新查询。没有 `next_cursor` 表示已结束，并已尝试释放快照。
单次后端响应最多 1 MiB；大属性对象导致响应超限时应减小页大小。

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
关联读取的每次后端响应最多 1 MiB；缺失/null `hits.hits`、尾随第二个 JSON 值、边两端身份不一致
或实例不属于已读取的边集合均作为错误，不当作有效空结果。完整校验后才返回目标集合。
正式 CMDB 丰富继续使用有界完整查询，不使用调试分页接口。

## 释放快照

`POST /api/v1/onemodel/close` 接收 `{"bk_tenant_id":"system","cursor":"<next_cursor>"}`，返回 `{"closed":true}`。
页面改变条件、重新查询或离开时释放未读完的快照；结束、失败和取消也会尝试释放。
释放失败不覆盖原查询结果，ES 在一分钟保活期后回收遗留快照；Doris close 只校验游标，无服务端 PIT 可释放。

## 预算与错误

每个控制面进程最多同时执行 4 个调试请求，不排队；单请求超时 10 秒，清理额外使用至多 2 秒。
查询连接池独立于 Lifecycle，配置为每节点最多 4 个连接；只配置 OneModel 就可使用，不要求 MySQL 或 Redis 资源。

| 状态码 | 语义 |
| --- | --- |
| 400 | 参数、过滤或游标无效；请求包含未知字段 |
| 401 | 管理身份未通过校验 |
| 410 | PIT 或游标已过期，需要重新查询 |
| 422 | 关联结果、边或关联读取响应字节数超过上限 |
| 429 | 调试并发预算已满 |
| 503 | 未配置 `resources.onemodel` |
| 504 | 请求超时或取消 |
| 502 | 外部数据源失败、实例分页响应超限、响应损坏或身份校验失败 |

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

## Doris 读取范围

Doris 连接配置见[配置指南](../../guides/configuration.md#onemodel-doris-读取)。请求字段、实例身份、租户、
关系方向及结果上限不变。对齐 KAC 的类型化 JSON_EXTRACT_STRING/BIGINT/DOUBLE/BOOL、ARRAY_CONTAINS、
LIKE/REGEXP 与缺失值否定语义，不增加类型或条件。大整数和 false/0 保留原值；SQL 参数、表名和响应
分别受校验与预算限制。驱动错误、部分读取和格式错误不转成未找到，错误正文不泄漏连接或参数。

KAC 源码基线为本地 Kingeye develop/5.3.0（06f8da31a3）的 base/infras/instance_storage/{doris.py,doris_schema.py,runtime.py}；
此适配仅覆盖其已有通用实例和投影边，主线拓扑边界保持不变。

真实 Doris 只读验证使用 LINKD_TEST_DORIS_DSN、LINKD_TEST_ONEMODEL_TENANT_ID、
LINKD_TEST_ONEMODEL_MODEL_ID、LINKD_TEST_ONEMODEL_INSTANCE_ID，可选
LINKD_TEST_DORIS_INSTANCE_TABLE/LINKD_TEST_DORIS_EDGE_TABLE 后执行：

```bash
go test -count=1 -run TestDorisOneModelContract ./internal/onemodel
```

没有显式环境时跳过。该用例不创建或修改业务表，只验证身份、租户隔离和当前态分页。
