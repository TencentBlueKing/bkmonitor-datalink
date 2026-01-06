# API 接口文档

本文档详细说明 Unify-Query 提供的所有 HTTP API 接口。

## 目录

1. [通用说明](#1-通用说明)
2. [查询接口](#2-查询接口)
3. [元数据查询接口](#3-元数据查询接口)
4. [转换接口](#4-转换接口)
5. [校验接口](#5-校验接口)
6. [关系查询接口](#6-关系查询接口)
7. [工具接口](#7-工具接口)

---

## 1. 通用说明

### 1.1 基础信息

- **Base URL**: `http://localhost:10205`
- **Content-Type**: `application/json`
- **字符编码**: UTF-8

### 1.2 通用请求头

| 请求头                  | 类型   | 必填 | 说明                                       |
| ----------------------- | ------ | ---- | ------------------------------------------ |
| `Content-Type`          | string | 是   | 固定值：`application/json`                 |
| `traceparent`           | string | 否   | 追踪 ID，用于分布式追踪                    |
| `Bk-Query-Source`       | string | 否   | 查询来源，格式：`username:goodman`         |
| `X-Bk-Scope-Space-Uid`  | string | 是   | 空间 UID，用于多租户隔离（大部分接口必填） |
| `X-Bk-Scope-Skip-Space` | string | 否   | 是否跳过空间验证                           |

### 1.3 通用响应格式

**注意**：不同接口的响应格式可能不同，具体格式请参考各接口的说明。

#### 查询接口响应格式

查询接口（如 `/query/ts`、`/query/promql`）返回 `PromData` 格式：

```json
{
  "series": [...],
  "status": {...},
  "trace_id": "...",
  "is_partial": false
}
```

#### 错误响应格式

所有接口的错误响应统一格式：

```json
{
  "trace_id": "...",
  "error": "错误信息"
}
```

### 1.4 HTTP 状态码

- `200 OK`: 请求成功
- `400 Bad Request`: 请求参数错误
- `404 Not Found`: 资源不存在
- `500 Internal Server Error`: 服务器内部错误
- `504 Gateway Timeout`: 查询超时

---

## 2. 查询接口

### 2.1 结构体查询

**接口**: `POST /query/ts`

**描述**: 使用结构化的查询 JSON 查询监控数据。

**注意**: `space_uid` 必须通过请求头 `X-Bk-Scope-Space-Uid` 传递，不要放在请求体中。

**请求头**:

| 请求头                 | 类型   | 必填 | 说明     |
| ---------------------- | ------ | ---- | -------- |
| `X-Bk-Scope-Space-Uid` | string | 是   | 空间 UID |

**请求体**:

```json
{
  "query_list": [
    {
      "table_id": "system.cpu_summary",
      "field_name": "usage",
      "function": [
        {
          "method": "avg",
          "without": false,
          "dimensions": []
        }
      ],
      "time_aggregation": {
        "function": "avg_over_time",
        "window": "60s"
      },
      "reference_name": "a",
      "dimensions": [],
      "conditions": {
        "field_list": [],
        "condition_list": []
      }
    }
  ],
  "metric_merge": "a",
  "start_time": "1629810830",
  "end_time": "1629811070",
  "step": "60s"
}
```

**请求参数说明**:

| 参数                | 类型   | 必填 | 说明                                                                                                                                                                                                                                  |
| ------------------- | ------ | ---- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `query_list`        | array  | 是   | 查询列表                                                                                                                                                                                                                              |
| `start_time`        | string | 是   | 开始时间（Unix 时间戳）                                                                                                                                                                                                               |
| `end_time`          | string | 是   | 结束时间（Unix 时间戳）                                                                                                                                                                                                               |
| `step`              | string | 否   | 查询步长，如 `60s`、`5m`。为空时自动补充 `1m`                                                                                                                                                                                         |
| `metric_merge`      | string | 否   | PromQL 语法表达式，用于合并多个查询结果。表达式中的指标引用 `query_list` 中的 `reference_name`，例如：`a + b`、`a / b * 100`。如果 `query_list` 中只有一个查询项且 `reference_name` 为 `a`，则 `metric_merge` 可以设置为 `a` 或不设置 |
| `limit`             | int    | 否   | 结果数量限制                                                                                                                                                                                                                          |
| `offset`            | string | 否   | 时间偏移，如 `5m`                                                                                                                                                                                                                     |
| `down_sample_range` | string | 否   | 降采样范围，大于 `step` 才能生效，如 `5m`                                                                                                                                                                                             |
| `timezone`          | string | 否   | 时区，如 `Asia/Shanghai`                                                                                                                                                                                                              |
| `look_back_delta`   | string | 否   | 偏移量                                                                                                                                                                                                                                |
| `instant`           | bool   | 否   | 是否为瞬时查询                                                                                                                                                                                                                        |
| `reference`         | bool   | 否   | 查询开始时间是否需要对齐                                                                                                                                                                                                              |
| `not_time_align`    | bool   | 否   | 查询开始时间和聚合是否需要对齐                                                                                                                                                                                                        |
| `from`              | int    | 否   | 翻页起始位置                                                                                                                                                                                                                          |
| `tsdb_map`          | object | 否   | 查询路由匹配中的 tsDB 列表，key 为 `reference_name`，用于直接指定存储信息（高级用法）                                                                                                                                                 |
| `order_by`          | array  | 否   | 排序字段列表，按顺序排序，负数代表倒序，如 `["_time", "-_value"]`                                                                                                                                                                     |
| `result_columns`    | array  | 否   | 指定保留返回字段值（内部使用）                                                                                                                                                                                                        |
| `scroll`            | string | 否   | 滚动查询窗口超时时间，如 `3m`（用于 Elasticsearch 滚动查询）                                                                                                                                                                          |
| `slice_max`         | int    | 否   | 最大切片数量（用于滚动查询）                                                                                                                                                                                                          |
| `is_multi_from`     | bool   | 否   | 是否启用 MultiFrom 查询（用于 Elasticsearch）                                                                                                                                                                                         |
| `is_search_after`   | bool   | 否   | 是否启用 SearchAfter 查询（用于 Elasticsearch）                                                                                                                                                                                       |
| `clear_cache`       | bool   | 否   | 是否强制清理已存在的缓存会话（用于滚动查询）                                                                                                                                                                                          |
| `highlight`         | object | 否   | 高亮配置（用于 Elasticsearch 查询）                                                                                                                                                                                                   |
| `dry_run`           | bool   | 否   | 是否启用 DryRun（仅验证查询，不执行）                                                                                                                                                                                                 |
| `is_merge_db`       | bool   | 否   | 是否启用合并 db 特性                                                                                                                                                                                                                  |

**查询项参数** (`query_list` 中的元素):

| 参数               | 类型   | 必填 | 说明                                            |
| ------------------ | ------ | ---- | ----------------------------------------------- |
| `table_id`         | string | 是   | 表 ID                                           |
| `field_name`       | string | 是   | 字段名，支持正则（需设置 `is_regexp: true`）    |
| `is_regexp`        | bool   | 否   | 是否使用正则表达式                              |
| `field_list`       | array  | 否   | 字段列表（仅供 exemplar 查询 trace 指标时使用） |
| `function`         | array  | 否   | 聚合函数列表                                    |
| `time_aggregation` | object | 否   | 时间聚合函数                                    |
| `reference_name`   | string | 是   | 查询引用名称，用于 `metric_merge` 表达式中引用  |
| `dimensions`       | array  | 否   | 维度列表                                        |
| `conditions`       | object | 否   | 过滤条件                                        |
| `limit`            | int    | 否   | 结果数量限制                                    |
| `slimit`           | int    | 否   | Series 数量限制                                 |
| `from`             | int    | 否   | 翻页起始位置                                    |
| `offset`           | string | 否   | 偏移量，如 `5m`                                 |
| `offset_forward`   | bool   | 否   | 偏移方向，默认 `false` 为向前偏移               |
| `query_string`     | string | 否   | 关键字查询（用于 Elasticsearch 等存储）         |
| `sql`              | string | 否   | Doris SQL 查询语句                              |
| `is_prefix`        | bool   | 否   | 是否启用前缀匹配（用于关键字查询）              |
| `collapse`         | object | 否   | 折叠配置（用于 Elasticsearch 查询）             |

**响应示例**:

```json
{
  "series": [
    {
      "name": "_result0",
      "metric_name": "",
      "columns": ["_time", "_value"],
      "types": ["float", "float"],
      "group_keys": [],
      "group_values": [],
      "values": [
        [1629810800000, 60.2],
        [1629810860000, 60.2]
      ]
    }
  ],
  "status": null,
  "trace_id": "...",
  "is_partial": false
}
```

### 2.2 PromQL 查询

**接口**: `POST /query/ts/promql`

**描述**: 通过 PromQL 语句查询监控数据。

**注意**: `space_uid` 必须通过请求头 `X-Bk-Scope-Space-Uid` 传递。

**请求体**:

```json
{
  "promql": "rate(cpu_usage[5m])",
  "start": "1629810830",
  "end": "1629811070",
  "step": "30s",
  "instant": false
}
```

**请求头**:

| 请求头                 | 类型   | 必填 | 说明     |
| ---------------------- | ------ | ---- | -------- |
| `X-Bk-Scope-Space-Uid` | string | 是   | 空间 UID |

**请求参数说明**:

| 参数                    | 类型   | 必填 | 说明                             |
| ----------------------- | ------ | ---- | -------------------------------- |
| `promql`                | string | 是   | PromQL 查询语句                  |
| `start`                 | string | 是   | 开始时间（Unix 时间戳）          |
| `end`                   | string | 是   | 结束时间（Unix 时间戳）          |
| `step`                  | string | 否   | 查询步长。为空时自动补充 `1m`    |
| `instant`               | bool   | 是   | 是否为即时查询                   |
| `down_sample_range`     | string | 否   | 降采样范围，大于 `step` 才能生效 |
| `timezone`              | string | 否   | 时区，如 `Asia/Shanghai`         |
| `look_back_delta`       | string | 否   | 偏移量                           |
| `reference`             | bool   | 否   | 查询开始时间是否需要对齐         |
| `not_time_align`        | bool   | 否   | 查询开始时间和聚合是否需要对齐   |
| `bk_biz_ids`            | array  | 否   | 业务 ID 列表（用于过滤）         |
| `max_source_resolution` | string | 否   | 最大源分辨率                     |
| `not_align_influxdb`    | bool   | 否   | 不与 InfluxDB 对齐               |
| `slimit`                | int    | 否   | Series 数量限制                  |
| `match`                 | string | 否   | 匹配条件                         |
| `is_verify_dimensions`  | bool   | 否   | 是否验证维度                     |

**响应格式**: 同结构体查询（返回 `PromData` 格式）

### 2.3 引用查询

**接口**: `POST /query/ts/reference`

**描述**: 使用查询引用进行查询。

**请求头**: 同结构体查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同结构体查询

**响应格式**: 同结构体查询（返回 `PromData` 格式）

### 2.4 原始查询

**接口**: `POST /query/ts/raw`

**描述**: 执行原始查询，返回原始数据列表（用于特殊场景，如 Elasticsearch 查询）。

**请求头**: 同结构体查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同结构体查询

**响应格式**: 返回 `ListData` 格式

**响应示例**:

```json
{
  "total": 100,
  "list": [
    {
      "field1": "value1",
      "field2": "value2"
    }
  ],
  "done": true,
  "trace_id": "...",
  "status": null,
  "result_table_options": null
}
```

### 2.5 原始查询（带滚动）

**接口**: `POST /query/ts/raw_with_scroll`

**描述**: 执行原始查询，支持滚动查询（用于大数据量分页查询，如 Elasticsearch）。

**请求头**: 同结构体查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同结构体查询，需要设置 `scroll` 参数（滚动窗口超时时间，如 `3m`）

**响应格式**: 返回 `ListData` 格式，包含 `done` 字段表示是否还有更多数据

**响应示例**:

```json
{
  "total": 10000,
  "list": [...],
  "done": false,
  "trace_id": "...",
  "status": null
}
```

### 2.6 集群指标查询

**接口**: `POST /query/ts/cluster_metrics`

**描述**: 查询集群指标数据。

**请求头**: 同结构体查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同结构体查询

**响应格式**: 同结构体查询（返回 `PromData` 格式）

### 2.7 Exemplar 查询

**接口**: `POST /query/ts/exemplar`

**描述**: 查询时序 exemplar 数据（用于追踪关联）。

**请求头**: 同结构体查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同结构体查询

**响应格式**: 同结构体查询（返回 `PromData` 格式）

---

## 3. 元数据查询接口

### 3.1 查询字段列表

**接口**: `POST /query/ts/info/field_keys`

**描述**: 查询指定表的字段（指标）列表。

**请求头**:

| 请求头                 | 类型   | 必填 | 说明     |
| ---------------------- | ------ | ---- | -------- |
| `X-Bk-Scope-Space-Uid` | string | 是   | 空间 UID |

**请求体**:

```json
{
  "table_id": "system.cpu_summary",
  "data_source": "",
  "start_time": "1629810830",
  "end_time": "1629811070"
}
```

**请求参数说明**:

| 参数          | 类型   | 必填 | 说明                         |
| ------------- | ------ | ---- | ---------------------------- |
| `table_id`    | string | 是   | 表 ID                        |
| `data_source` | string | 否   | 数据源                       |
| `start_time`  | string | 是   | 开始时间（Unix 时间戳）      |
| `end_time`    | string | 是   | 结束时间（Unix 时间戳）      |
| `metric_name` | string | 否   | 指标名称（支持正则）         |
| `is_regexp`   | bool   | 否   | 是否使用正则                 |
| `conditions`  | object | 否   | 过滤条件                     |
| `tsdb_map`    | object | 否   | 直接指定存储信息（高级用法） |
| `limit`       | int    | 否   | 结果数量限制                 |
| `slimit`      | int    | 否   | Series 数量限制              |
| `timezone`    | string | 否   | 时区，如 `Asia/Shanghai`     |

**响应示例**:

```json
["usage", "idle", "iowait"]
```

### 3.2 查询标签列表

**接口**: `POST /query/ts/info/tag_keys`

**描述**: 查询指定表的标签（维度）列表。

**请求头**: 同字段列表查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同字段列表查询

**响应格式**: 同字段列表查询（返回字符串数组）

### 3.3 查询标签值

**接口**: `POST /query/ts/info/tag_values`

**描述**: 查询指定标签的值列表。

**请求头**:

| 请求头                 | 类型   | 必填 | 说明     |
| ---------------------- | ------ | ---- | -------- |
| `X-Bk-Scope-Space-Uid` | string | 是   | 空间 UID |

**请求体**:

```json
{
  "table_id": "system.cpu_summary",
  "keys": ["host", "instance"],
  "start_time": "1629810830",
  "end_time": "1629811070",
  "conditions": {
    "field_list": [
      {
        "field_name": "host",
        "value": ["server1"],
        "op": "eq"
      }
    ]
  }
}
```

**请求参数说明**:

| 参数     | 类型  | 必填 | 说明               |
| -------- | ----- | ---- | ------------------ |
| `keys`   | array | 是   | 要查询的标签名列表 |
| 其他参数 | -     | -    | 同字段列表查询     |

**响应示例**:

```json
{
  "values": {
    "host": ["server1", "server2"],
    "instance": ["instance1", "instance2"]
  },
  "trace_id": "..."
}
```

### 3.4 查询 Series

**接口**: `POST /query/ts/info/series`

**描述**: 查询 Series 列表。

**请求头**: 同字段列表查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同字段列表查询

**响应示例**:

```json
{
  "measurement": "system.cpu_summary",
  "keys": ["host", "instance"],
  "series": [
    ["server1", "instance1"],
    ["server2", "instance2"]
  ],
  "trace_id": "..."
}
```

### 3.5 查询时间序列

**接口**: `POST /query/ts/info/time_series`

**描述**: 查询时间序列信息。

**请求头**: 同字段列表查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同字段列表查询

**响应格式**: 返回 `InfoData` 格式

**响应示例**:

```json
{
  "series": [
    {
      "name": "_result0",
      "metric_name": "",
      "columns": ["_time", "_value"],
      "types": ["float", "float"],
      "group_keys": [],
      "group_values": [],
      "values": []
    }
  ]
}
```

### 3.6 查询字段映射

**接口**: `POST /query/ts/info/field_map`

**描述**: 查询字段映射关系（主要用于 Elasticsearch 等存储的字段信息）。

**请求头**: 同字段列表查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同字段列表查询（需要指定 `data_source` 和 `table_id`）

**响应示例**:

```json
{
  "data": [
    {
      "alias_name": "",
      "field_name": "__ext.container_id",
      "field_type": "keyword",
      "origin_field": "__ext",
      "is_agg": true,
      "is_analyzed": false,
      "is_case_sensitive": false,
      "tokenize_on_chars": []
    }
  ],
  "trace_id": "..."
}
```

### 3.7 根据标签名查询标签值

**接口**: `GET /query/ts/label/{label_name}/values`

**描述**: 根据标签名查询标签值（Prometheus 兼容接口）。

**请求头**:

| 请求头                 | 类型   | 必填 | 说明     |
| ---------------------- | ------ | ---- | -------- |
| `X-Bk-Scope-Space-Uid` | string | 是   | 空间 UID |

**请求参数**:

| 参数         | 类型   | 位置  | 必填 | 说明                                   |
| ------------ | ------ | ----- | ---- | -------------------------------------- |
| `label_name` | string | path  | 是   | 标签名                                 |
| `start`      | string | query | 否   | 开始时间（Unix 时间戳）                |
| `end`        | string | query | 否   | 结束时间（Unix 时间戳）                |
| `match[]`    | string | query | 是   | 匹配条件（PromQL 语句），只支持 1 个值 |
| `limit`      | string | query | 否   | 结果数量限制                           |

**响应示例**:

```json
{
  "values": {
    "container": ["POD", "kube-proxy"]
  },
  "trace_id": "..."
}
```

---

## 4. 转换接口

### 4.1 结构体转 PromQL

**接口**: `POST /query/ts/struct_to_promql`

**描述**: 将查询结构体转换为 PromQL 语句。

**请求头**: 同结构体查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同结构体查询

**响应示例**:

```json
{
  "promql": "avg(avg_over_time(cpu_usage[1m]))",
  "start": "1629810830",
  "end": "1629811070",
  "step": "60s",
  "instant": false
}
```

### 4.2 PromQL 转结构体

**接口**: `POST /query/ts/promql_to_struct`

**描述**: 将 PromQL 语句转换为查询结构体。

**请求头**:

| 请求头                 | 类型   | 必填 | 说明     |
| ---------------------- | ------ | ---- | -------- |
| `X-Bk-Scope-Space-Uid` | string | 是   | 空间 UID |

**请求体**:

```json
{
  "promql": "avg(avg_over_time(cpu_usage[1m]))",
  "start": "1629810830",
  "end": "1629811070",
  "step": "60s"
}
```

**响应示例**:

```json
{
  "data": {
    "query_list": [
      {
        "table_id": "system.cpu_summary",
        "field_name": "usage",
        "function": [
          {
            "method": "avg"
          }
        ],
        "time_aggregation": {
          "function": "avg_over_time",
          "window": "60s"
        },
        "reference_name": "a"
      }
    ],
    "start_time": "1629810830",
    "end_time": "1629811070",
    "step": "60s"
  }
}
```

**注意**: 响应格式为 `{"data": {...}}`，其中 `data` 字段包含转换后的查询结构体。

---

## 5. 校验接口

### 5.1 校验结构体查询

**接口**: `POST /check/query/ts`

**描述**: 校验结构体查询，返回查询转换的各个步骤信息（用于调试）。

**请求头**: 同结构体查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同结构体查询

**响应格式**: 返回文本格式的调试信息，包含查询转换的各个步骤

**响应示例**:

```
step-name: query ts
data: {"query_list":[...],...}
-------------------------------------------------
step-name: metadata user
data: {"key":"username:goodman","space_uid":"bkcc__2"}
-------------------------------------------------
step-name: query-reference
data: {...}
-------------------------------------------------
step-name: query promQL
data: "avg(avg_over_time(cpu_usage[1m]))"
-------------------------------------------------
```

**注意**: 如果某个步骤出错，会显示 `error: ...` 而不是 `data: ...`

### 5.2 校验 PromQL 查询

**接口**: `POST /check/query/ts/promql`

**描述**: 校验 PromQL 查询，返回查询转换的各个步骤信息。

**请求头**: 同 PromQL 查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**: 同 PromQL 查询

**响应格式**: 同结构体查询校验（返回文本格式）

---

## 6. 关系查询接口

### 6.1 多资源关系查询

**接口**: `POST /api/v1/relation/multi_resource`

**描述**: 通过关键维度，查询对应目标资源的关联维度信息（批量）。

**请求头**:

| 请求头                 | 类型   | 必填 | 说明     |
| ---------------------- | ------ | ---- | -------- |
| `X-Bk-Scope-Space-Uid` | string | 是   | 空间 UID |

**请求体**:

```json
{
  "query_list": [
    {
      "timestamp": 1693217460,
      "target_type": "system",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000",
        "namespace": "blueking",
        "pod": "bk-applog-bkapp-filebeat-stdout-gnknx"
      }
    },
    {
      "timestamp": 1693217460,
      "target_type": "system",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000",
        "node": "node-127-0-0-1"
      }
    }
  ]
}
```

**请求参数说明**:

| 参数                              | 类型   | 必填 | 说明                                                                                                    |
| --------------------------------- | ------ | ---- | ------------------------------------------------------------------------------------------------------- |
| `query_list`                      | array  | 是   | 查询列表                                                                                                |
| `query_list[].timestamp`          | int    | 是   | 时间戳（Unix 时间戳，秒级）                                                                             |
| `query_list[].source_type`        | string | 否   | 源资源类型，如 `pod`、`node`                                                                            |
| `query_list[].source_info`        | object | 是   | 源资源信息（维度映射），如 `{"bcs_cluster_id": "BCS-K8S-00000", "namespace": "blueking", "pod": "xxx"}` |
| `query_list[].source_expand_info` | object | 否   | 源资源扩展信息（维度映射）                                                                              |
| `query_list[].target_type`        | string | 是   | 目标资源类型，如 `system`                                                                               |
| `query_list[].target_info_show`   | bool   | 否   | 是否显示目标资源信息                                                                                    |
| `query_list[].path_resource`      | array  | 否   | 关联路径资源类型列表                                                                                    |
| `query_list[].look_back_delta`    | string | 否   | 偏移量，如 `5m`                                                                                         |

**响应示例**:

```json
{
  "trace_id": "...",
  "data": [
    {
      "code": 200,
      "source_type": "pod",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000",
        "namespace": "blueking",
        "pod": "bk-applog-bkapp-filebeat-stdout-gnknx"
      },
      "target_type": "system",
      "target_list": [
        {
          "bk_target_ip": "127.0.0.1"
        }
      ]
    },
    {
      "code": 200,
      "source_type": "node",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000",
        "node": "node-127-0-0-1"
      },
      "target_type": "system",
      "target_list": [
        {
          "bk_target_ip": "127.0.0.1"
        }
      ]
    },
    {
      "code": 400,
      "message": "错误信息"
    }
  ]
}
```

### 6.2 多资源关系范围查询

**接口**: `POST /api/v1/relation/multi_resource_range`

**描述**: 多资源关系查询的范围查询版本，支持时间范围查询。

**请求头**: 同多资源关系查询（需要 `X-Bk-Scope-Space-Uid`）

**请求体**:

```json
{
  "query_list": [
    {
      "start_time": 1693217460,
      "end_time": 1693218060,
      "step": "60s",
      "target_type": "system",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000",
        "namespace": "blueking",
        "pod": "bk-applog-bkapp-filebeat-stdout-gnknx"
      }
    }
  ]
}
```

**请求参数说明**:

| 参数                              | 类型   | 必填 | 说明                          |
| --------------------------------- | ------ | ---- | ----------------------------- |
| `query_list`                      | array  | 是   | 查询列表                      |
| `query_list[].start_time`         | int    | 是   | 开始时间（Unix 时间戳，秒级） |
| `query_list[].end_time`           | int    | 是   | 结束时间（Unix 时间戳，秒级） |
| `query_list[].step`               | string | 是   | 查询步长，如 `60s`            |
| `query_list[].source_type`        | string | 否   | 源资源类型                    |
| `query_list[].source_info`        | object | 是   | 源资源信息（维度映射）        |
| `query_list[].source_expand_info` | object | 否   | 源资源扩展信息（维度映射）    |
| `query_list[].target_type`        | string | 是   | 目标资源类型                  |
| `query_list[].target_info_show`   | bool   | 否   | 是否显示目标资源信息          |
| `query_list[].path_resource`      | array  | 否   | 关联路径资源类型列表          |
| `query_list[].look_back_delta`    | string | 否   | 偏移量，如 `5m`               |

**响应示例**:

```json
{
  "trace_id": "...",
  "data": [
    {
      "code": 200,
      "source_type": "pod",
      "source_info": {
        "bcs_cluster_id": "BCS-K8S-00000",
        "namespace": "blueking",
        "pod": "bk-applog-bkapp-filebeat-stdout-gnknx"
      },
      "target_type": "system",
      "target_list": [
        {
          "timestamp": 1693217460,
          "items": [
            {
              "bk_target_ip": "127.0.0.1"
            }
          ]
        },
        {
          "timestamp": 1693217520,
          "items": [
            {
              "bk_target_ip": "127.0.0.1"
            }
          ]
        }
      ],
      "path": []
    }
  ]
}
```

---

## 7. 工具接口

### 7.1 健康检查

**接口**: `HEAD /`

**描述**: 健康检查接口。

**响应**: HTTP 200 表示服务正常

### 7.2 打印路由信息

**接口**: `GET /print`

**描述**: 打印当前的路由信息（用于调试）。

**响应**: 文本格式的路由信息

### 7.3 打印 InfluxDB 信息

**接口**: `GET /influxdb_print`

**描述**: 打印 InfluxDB 相关信息（用于调试）。

**响应**: 文本格式的 InfluxDB 信息

### 7.4 打印空间信息

**接口**: `GET /space_print`

**描述**: 打印空间路由信息。

**查询参数**:

| 参数       | 类型   | 必填 | 说明         |
| ---------- | ------ | ---- | ------------ |
| `type_key` | string | 否   | 类型键       |
| `refresh`  | bool   | 否   | 是否刷新     |
| `content`  | bool   | 否   | 是否显示内容 |

**响应**: 文本格式的空间路由信息

### 7.5 打印空间键信息

**接口**: `GET /space_key_print`

**描述**: 打印指定空间键的详细信息。

**查询参数**:

| 参数       | 类型   | 必填 | 说明         |
| ---------- | ------ | ---- | ------------ |
| `type_key` | string | 否   | 类型键       |
| `hash_key` | string | 否   | 哈希键       |
| `cached`   | bool   | 否   | 是否使用缓存 |
| `refresh`  | bool   | 否   | 是否刷新     |
| `content`  | bool   | 否   | 是否显示内容 |

**响应**: 文本格式的空间键信息

### 7.6 打印 TSDB 信息

**接口**: `GET /tsdb_print`

**描述**: 打印 TSDB 相关信息。

**查询参数**:

| 参数         | 类型   | 必填 | 说明    |
| ------------ | ------ | ---- | ------- |
| `space_id`   | string | 否   | 空间 ID |
| `table_id`   | string | 否   | 表 ID   |
| `field_name` | string | 否   | 字段名  |

**响应**: 文本格式的 TSDB 信息

### 7.7 特性开关

**接口**: `GET /ff`

**描述**: 查看或刷新特性开关配置信息（用于调试）。

**查询参数**:

| 参数 | 类型   | 必填 | 说明                             |
| ---- | ------ | ---- | -------------------------------- |
| `r`  | string | 否   | 不为空时强制刷新特性开关配置     |
| `c`  | string | 否   | 特性开关键名                     |
| `t`  | string | 否   | 特性开关类型，默认为 `string`    |
| `k`  | string | 否   | 用户属性键名（用于测试特性开关） |
| `v`  | string | 否   | 用户属性值（用于测试特性开关）   |

**响应**: 文本格式的特性开关信息

### 7.8 代理接口

**接口**: `POST /proxy`

**描述**: 代理接口，用于 API 网关统一调用其他接口。

**请求体**:

```json
{
  "path": "/query/ts",
  "data": {
    "query_list": [...],
    "start_time": "1629810830",
    "end_time": "1629811070",
    "step": "60s"
  }
}
```

**请求参数说明**:

| 参数   | 类型   | 必填 | 说明                     |
| ------ | ------ | ---- | ------------------------ |
| `path` | string | 是   | 要代理的接口路径         |
| `data` | object | 是   | 要代理的接口的请求体数据 |

**响应格式**: 统一格式

```json
{
  "result": true,
  "data": {...},
  "message": "success"
}
```

**错误响应**:

```json
{
  "result": false,
  "data": null,
  "message": "错误信息"
}
```

### 7.9 Prometheus 指标

**接口**: `GET /metrics`

**描述**: 暴露 Prometheus 格式的指标（如果启用）。

**响应**: Prometheus 格式的指标数据

---

## 8. 数据格式说明

### 8.1 时间格式

- **Unix 时间戳**: 秒级时间戳，如 `1629810830`
- **时间字符串**: 支持 `2006-01-02T15:04:05Z` 格式
- **时间偏移**: 支持 `5m`、`1h`、`1d` 等格式

### 8.2 步长格式

支持 Prometheus 时间格式：
- `s`: 秒
- `m`: 分钟
- `h`: 小时
- `d`: 天
- `w`: 周

示例: `60s`、`5m`、`1h`

### 8.3 聚合函数

支持的聚合函数：
- `sum`: 求和
- `avg`: 平均值（PromQL 标准语法）
- `min`: 最小值
- `max`: 最大值
- `count`: 计数
- `stddev`: 标准差
- `quantile`: 分位数（需要 `vargs_list` 参数）
- `topk`: 前 k 个最大值（需要 `vargs_list` 参数）
- `bottomk`: 前 k 个最小值（需要 `vargs_list` 参数）
- `group`: 分组
- `stdvar`: 标准方差
- `count_values`: 计数相同值（需要 `vargs_list` 参数）

**注意**：虽然代码中也支持 `mean`（兼容性），但建议使用 PromQL 标准语法 `avg`。

**聚合函数参数** (`function` 数组中的元素):

| 参数           | 类型   | 必填 | 说明                                                                |
| -------------- | ------ | ---- | ------------------------------------------------------------------- |
| `method`       | string | 是   | 聚合方法名称                                                        |
| `field`        | string | 否   | 聚合字段，默认为指标字段，指定则会进行覆盖                          |
| `without`      | bool   | 否   | 是否使用 `without` 语法（与 `dimensions` 互斥）                     |
| `dimensions`   | array  | 否   | 聚合维度列表，如 `["bk_target_ip", "bk_target_cloud_id"]`           |
| `window`       | string | 否   | 聚合周期（用于时间窗口聚合），如 `60s`                              |
| `is_sub_query` | bool   | 否   | 判断是否为子查询                                                    |
| `step`         | string | 否   | 子查询区间 step（用于子查询）                                       |
| `vargs_list`   | array  | 否   | 函数参数列表，用于 `topk`、`bottomk`、`quantile`、`count_values` 等 |
| `position`     | int    | 否   | 函数参数位置，结合 `vargs_list` 一起使用（内部使用）                |

### 8.4 时间聚合函数

支持的时间聚合函数：
- `avg_over_time`: 时间窗口平均值
- `min_over_time`: 时间窗口最小值
- `max_over_time`: 时间窗口最大值
- `sum_over_time`: 时间窗口求和
- `count_over_time`: 时间窗口计数
- `rate`: 增长率
- `irate`: 瞬时增长率
- `increase`: 增长量
- `delta`: 差值
- `idelta`: 瞬时差值
- `deriv`: 导数
- `predict_linear`: 线性预测
- `holt_winters`: 霍尔特-温特斯预测

**时间聚合函数参数** (`time_aggregation` 对象):

| 参数           | 类型   | 必填 | 说明                                                 |
| -------------- | ------ | ---- | ---------------------------------------------------- |
| `function`     | string | 是   | 时间聚合方法名称                                     |
| `window`       | string | 是   | 聚合周期，如 `60s`、`5m`                             |
| `node_index`   | int    | 否   | 聚合函数的位置，用于还原 promql 的定位（内部使用）   |
| `is_sub_query` | bool   | 否   | 判断是否为子查询                                     |
| `step`         | string | 否   | 子查询区间 step（用于子查询）                        |
| `vargs_list`   | array  | 否   | 函数参数列表，用于需要额外参数的函数                 |
| `position`     | int    | 否   | 函数参数位置，结合 `vargs_list` 一起使用（内部使用） |

### 8.5 过滤条件

过滤条件格式：

```json
{
  "field_list": [
    {
      "field_name": "host",
      "value": ["server1", "server2"],
      "op": "eq"
    },
    {
      "field_name": "cpu",
      "value": ["80"],
      "op": "gt"
    }
  ],
  "condition_list": ["and"]
}
```

**重要说明**：
- `condition_list` 的长度必须等于 `len(field_list) - 1`
- `condition_list[i]` 表示 `field_list[i]` 和 `field_list[i+1]` 之间的逻辑关系
- 例如：如果有 2 个字段，需要 1 个条件（"and" 或 "or"）；如果有 3 个字段，需要 2 个条件

**示例**：
- 1 个字段：`field_list` 长度为 1，`condition_list` 为空数组 `[]`
- 2 个字段：`field_list` 长度为 2，`condition_list` 为 `["and"]` 或 `["or"]`
- 3 个字段：`field_list` 长度为 3，`condition_list` 为 `["and", "or"]`（表示：field1 AND field2 OR field3）

**操作符**:
- `eq`: 等于
- `ne`: 不等于
- `gt`: 大于
- `gte`: 大于等于
- `lt`: 小于
- `lte`: 小于等于
- `req`: 正则匹配
- `nreq`: 非正则匹配
- `contains`: 包含（多值匹配，转换为正则表达式 `^(value1|value2|...)$`）
- `ncontains`: 不包含（多值匹配，转换为非正则表达式）
- `existed`: 存在（字段存在）
- `nexisted`: 不存在（字段不存在）
- `exact`: 精确匹配（等同于 `eq`）

---

## 9. 错误码说明

### 9.1 业务错误码

| 错误码 | 说明           |
| ------ | -------------- |
| `200`  | 成功           |
| `400`  | 请求参数错误   |
| `404`  | 资源不存在     |
| `500`  | 服务器内部错误 |
| `504`  | 查询超时       |

### 9.2 错误信息格式

错误信息包含：
- 错误码
- 错误消息
- 错误详情（可选）

---

## 10. 使用示例

### 10.1 查询 CPU 使用率

```bash
curl -X POST http://localhost:10205/query/ts \
  -H "Content-Type: application/json" \
  -H "X-Bk-Scope-Space-Uid: bkcc__2" \
  -d '{
    "query_list": [
      {
        "table_id": "system.cpu_summary",
        "field_name": "usage",
        "reference_name": "cpu_usage",
        "time_aggregation": {
          "function": "avg_over_time",
          "window": "60s"
        }
      }
    ],
    "start_time": "1629810830",
    "end_time": "1629811070",
    "step": "60s"
  }'
```

### 10.2 使用 PromQL 查询

```bash
curl -X POST http://localhost:10205/query/ts/promql \
  -H "Content-Type: application/json" \
  -H "X-Bk-Scope-Space-Uid: bkcc__2" \
  -d '{
    "promql": "rate(cpu_usage[5m])",
    "start": "1629810830",
    "end": "1629811070",
    "step": "30s",
    "instant": false
  }'
```

### 10.3 查询字段列表

```bash
curl -X POST http://localhost:10205/query/ts/info/field_keys \
  -H "Content-Type: application/json" \
  -H "X-Bk-Scope-Space-Uid: bkcc__2" \
  -d '{
    "table_id": "system.cpu_summary",
    "start_time": "1629810830",
    "end_time": "1629811070"
  }'
```

### 10.4 使用 metric_merge 合并多个查询

```bash
curl -X POST http://localhost:10205/query/ts \
  -H "Content-Type: application/json" \
  -H "X-Bk-Scope-Space-Uid: bkcc__2" \
  -d '{
    "query_list": [
      {
        "table_id": "system.cpu_summary",
        "field_name": "usage",
        "reference_name": "a",
        "time_aggregation": {
          "function": "avg_over_time",
          "window": "60s"
        }
      },
      {
        "table_id": "system.mem_summary",
        "field_name": "usage",
        "reference_name": "b",
        "time_aggregation": {
          "function": "avg_over_time",
          "window": "60s"
        }
      }
    ],
    "metric_merge": "a + b",
    "start_time": "1629810830",
    "end_time": "1629811070",
    "step": "60s"
  }'
```

**说明**：
- `reference_name` 为 `a` 和 `b` 分别对应两个查询结果
- `metric_merge` 为 `a + b` 表示将两个查询结果相加
- 支持所有 PromQL 语法，如 `a * 100`、`(a - b) / b * 100` 等

---

## 附录

### A. 相关文档

- 关系查询接口详见本文档第 6 节
- [PromQL 文档](../promql/promql.md)
- [Swagger 文档](../swagger.yaml)

### B. 更多示例

更多使用示例请参考项目根目录的 `README.md`。

