# 模块基础信息 API

`GET /api/v1/module-info` 供 Admin 服务端发现当前 Linkd 控制面生效的部署、Repository 查询目标和连接信息。
无需在 Admin 重复配置 repository、deployment、ES/MySQL/Redis 地址或实体查询 alias。

## 鉴权和敏感字段

使用[管理 JWT](internal-token.md)：`Internal-Token: Bearer <JWT>`，签名密钥与 Kingeye 公共内部 JWT 相同，
与 Admin 自己的 CLI Token 签名密钥不同。Worker Token 不得访问。

- 默认及 `include_secrets=false`：省略连接密码、API Key 和 Basic Auth 用户名密码。
- `include_secrets=true`：返回下面白名单连接所需的认证材料，仅供 Admin 服务端内存使用；不得转发到浏览器、日志、遥测或错误。
- 两种响应均为 `Cache-Control: no-store`，不返回管理 JWT、JWT 签名密钥、Worker Token、原始配置或任意文件内容。
- 只接受最多一次 `include_secrets=true|false`；未知、重复和非法查询参数返回 400。不能指定配置文件、目标地址或资源名。

## JSON 结构

以下为 ES Repository、Redis standalone、Basic Auth 的敏感响应示例；示例凭据不是实际凭据。
`schema_version=1` 固定表示本文响应结构，未列出的可选字段见后文。
为便于阅读，示例省略 `runtime_settings` 的内容和 `capabilities` 的其余项目。

```json
{
  "schema_version": 1,
  "module": "linkd",
  "deployment": "prod",
  "version": "dev",
  "secrets_included": true,
  "storage": {
    "status": "configured",
    "repository": "elasticsearch",
    "tenant_field": "bk_tenant_id",
    "elasticsearch": {
      "status": "configured",
      "addresses": ["http://es:9200"],
      "auth": {"type": "basic", "username": "example-user", "password": "example-password"},
      "index_prefix": "linkd-prod",
      "event_targets": ["linkd-prod-events"],
      "alert_targets": ["linkd-prod-alerts"],
      "alert_log_targets": ["linkd-prod-alert-logs"],
      "active_alert_targets": ["linkd-prod-alerts-active"],
      "alert_history_targets": ["linkd-prod-alert-history"],
      "time_partition": {
        "timezone": "UTC",
        "epoch": "1970-01-05T00:00:00Z",
        "event_bucket_days": 7,
        "alert_history_bucket_days": 7,
        "alert_log_bucket_days": 7
      },
      "refresh_interval_seconds": 5,
      "active_alert_refresh_interval_seconds": 5
    },
    "mysql": {"status": "unavailable", "reason": "repository_not_mysql"},
    "redis": {
      "status": "configured",
      "connection": {"mode": "standalone", "address": "redis:6379", "password": "example-password", "database": 0}
    }
  },
  "metrics": {"status": "available", "listen_address": "0.0.0.0:9464", "path": "/metrics"},
  "kafka": {"status": "source_scoped", "sources_endpoint": "/api/v1/event-sources?published=true"},
  "kac": {"status": "unavailable", "reason": "plugin_disabled"},
  "runtime_settings": {},
  "capabilities": {
    "event_sources": {"status": "available", "endpoint": "/api/v1/event-sources"},
    "policies": {"status": "available", "endpoint": "/api/v1/policies"},
    "projection_tasks": {"status": "available", "endpoint": "/api/v1/projection-tasks"}
  }
}
```

字段使用 snake_case。`version` 是当前进程启动时传入的构建版本，未持有版本信息时省略；不推断 Git commit。
`deployment` 来自当前控制面的有效 `dispatch.deployment`。没有任何按 tenant 聚合或来源推断的部署身份。
配置修改需重启控制面；信息来自进程有效配置快照，不重新读取配置文件或探测远端。

## 状态和读取边界

- `configured`：已配置并通过结构校验，**不承诺**远端可连接、索引/表已存在、Admin 网络可达或凭据有只读权限。
- `available`：当前进程已注入对应 API 能力或已绑定 metrics listener，具体依赖健康见各自运行接口。
- `unavailable`：没有配置或没有注入能力；`reason` 为固定原因码，不携带原始错误。
- `source_scoped`：Kafka 不存在部署级唯一连接；通过正式 EventSource 列表及其发布版本发现。
- 缺少存储等某一能力时仍返回 200 和对应 `unavailable`，不伪造连接或目标；基础信息服务本身未装配时返回 503。

### Elasticsearch

`storage.elasticsearch` 仅在主 Repository 为 ES 时返回连接与目标；其他情况为 `unavailable/repository_not_elasticsearch`。
`addresses` 为全部生效的 HTTP(S) origin，禁止包含 URL userinfo、query、fragment 或额外路径。
`auth.type` 为 `none`、`api_key` 或 `basic`；敏感模式下分别无需凭据、返回 `api_key`、返回 `username/password`。
默认模式只返回 `auth.type`，不是把脱敏占位符当作可用凭据。

查询目标由生产 BucketRouter 提供，均为真实 alias，无 `*_read` 猜测别名，也无需 Admin 拼接通配符。
`alert_targets` 同时覆盖 Active 和 History。归档复制与删除不是原子操作，可能短暂出现同一租户/Alert 的两份文档；
消费者按 `bk_tenant_id/alert_id` 及业务 `revision` 合并，不能把 ES hit 总数直接当作唯一 Alert 数。
Active 索引在归档前也可能保留终态文档，活动查询还需 `status=active`。

Event 按 EventID 中的 `received_at` 分桶；Alert History 按 AlertID 中的 `create_at` 分桶；
AlertLog 也按所属 AlertID 的 `create_at` 分桶，**不是**按日志的 `created_time` 分桶。
均为从 epoch 对齐的 UTC `[start,end)` 区间，物理桶日期格式 `YYYYMMDD`；优先查询全量 alias，避免按过滤时间猜测桶。
业务时间过滤字段分别为 `received_at`、`update_at`、`created_time`，ES 类型为 `date_nanos`；保留纳秒精度。
刷新间隔是最终可见性预算的配置，不承诺实时快照。

### MySQL

主 Repository 为 MySQL 时，`storage.mysql` 返回：

```json
{
  "status": "configured",
  "address": "mysql:3306",
  "database": "linkd",
  "username": "example-user",
  "password": "example-password",
  "event_table": "linkd_events",
  "alert_table": "linkd_alerts",
  "alert_log_table": "linkd_alert_logs"
}
```

默认模式省略 `username/password`。实体表名固定且无 ES alias/时间桶，查询须限定 `bk_tenant_id`。
Event 时间列 `received_at_ns`、AlertLog 时间列 `created_time_ns` 为 Unix 纳秒，不能转成 JS number 后分页；
Alert 查询时间使用 JSON `payload.update_at`。Event 业务字段位于 `payload`，处理状态位于独立 `processing`。
非 MySQL Repository 返回 `unavailable/repository_not_mysql`，不把辅助 MySQL 连接冒充实体查询库。

### Redis、Kafka、KAC 与运行设置

Redis 的 `connection` 使用有效 `storage.redis` 的结构化字段，支持 `standalone` 和 `sentinel`。
Sentinel 返回 `sentinel.master_name/addresses`，敏感模式还包括数据节点与 Sentinel 各自的 `username/password`。
默认模式省略两处认证字段。没有 Redis 时为 `unavailable/not_configured`，不假定 DB 0 或本地地址。
部署/来源级 stream、mailbox、lock 等实际命名继续由 `/api/v1/runtime` 的 `routes` 和各领域 API 提供，不能仅根据部署拼接。

Kafka 的 `sources_endpoint` 返回发布配置及发布版本；需要 SASL/内联 TLS 材料时由 Admin 服务端追加 `include_secrets=true`。
列表需分页，未发布或 deleted 的来源不能当作活动订阅。TLS 文件路径仅对 Linkd 运行环境有意义，
模块接口不读取或导出文件；Admin 无法使用的文件型 TLS 连接应标为 unavailable，不能要求复制来源配置或猜测文件路径。
Kafka 健康与实际订阅仍需针对已发布来源查询，不用静态 YAML 冒充运行态。

启用 KAC 全局插件时，`kac` 返回 `configured`、独立的 `addresses/auth` 与真实 `alert_targets`；
该集群可能与 Repository 不同，不能复用 Repository 地址或前缀。不会返回 KAC JWT 密钥或动作凭据。

`runtime_settings` 只包含显式白名单的无凭据运行参数：`cleaner`，以及已配置的 `lifecycle`、
`elasticsearch_manager`、`redis_stream_manager`，字段与其有效 YAML 节点相同，默认值由 Linkd 收敛。
不会包含 EventSource payload、resources、蓝鲸应用密钥、JWT、配置文件路径或完整原始配置。

### 指标与能力目录

`metrics` 仅描述当前进程实际绑定的 exporter：`listen_address`、`path=/metrics`；关闭时为 `unavailable/not_enabled`。
监听地址可能是 wildcard、loopback 或容器内地址，不构造外部可访问 URL，不返回其他 Worker 的监听地址。
Prometheus **查询服务**是 Admin 公共配置，不属于 Linkd 模块配置；不要把 exporter 当作 `/api/v1/query` 服务。

`capabilities` 返回已装配的管理 API 入口与 available/unavailable 状态：
`event_sources`、`tenants`、`policies`、`policy_statistics`、`policy_preview`、`policy_simulation`、
`merge_runtime`、`shield_runtime`、`suppression_runtime`、`projection_tasks`、`action_deliveries`、
`onemodel`、`enrich_preview`、`ingest_preview`、`runtime`、`control_plane_tasks`、`metric_catalog`。
不可用的入口带 `reason=not_configured`。目录只反映服务装配状态，不代表远端依赖健康或新授权。

## Admin 接入

Admin 只需 Linkd 控制面地址与公共 Kingeye 内部 JWT 配置；Prometheus 查询服务使用 Admin 顶层公共配置。
服务端请求 `module-info?include_secrets=true`，按 `storage.repository` 选择后端，直接使用服务端返回的目标与连接。
禁止将整个响应序列化进前端配置、持久缓存、错误信息或调试日志。默认脱敏接口可用于配置展示。
收到 401/503 或 schema 不支持时明确标为不可用，不回退手工拼接 alias 或重复的旧 values。
本接口返回的是 Linkd 当前使用的连接身份，不会自动创建只读账号或收窄原账号权限；Admin 必须限制自己为只读查询。
