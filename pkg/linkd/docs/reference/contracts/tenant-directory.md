# 租户目录查询 API

控制面按 `blueking.enable_multi_tenant_mode` 提供租户目录。目录项不表示租户已接入 Linkd、已初始化或已获业务访问授权。

## 请求与鉴权

```http
GET /api/v1/tenants
Internal-Token: <management JWT>
```

沿用[内部 HTTP JWT 认证](internal-token.md)。这是管理级全局目录接口，不需要 `bk_tenant_id`，
不接受查询参数；Worker Token 不能访问。用户管理应用凭据只由控制面部署配置提供。

## 成功响应

关闭多租户模式（缺省为 false）时，无需蓝鲸连接或应用凭据，固定返回以下目录，不请求用户管理：

```json
[{ "id": "system", "name": "Blueking", "status": "enabled" }]
```

开启多租户模式时，HTTP 200 返回 JSON 数组，保留上游顺序和状态，包括禁用租户。空目录返回 `[]`。

```json
[
  { "id": "system", "name": "蓝鲸运营", "status": "enabled" },
  { "id": "test", "name": "测试租户", "status": "disabled" }
]
```

只返回 `id/name/status`，不转发上游额外字段。`id` 使用 Linkd 的稳定租户标识校验，最多 64 字节；
`name` 最多 256 字节，`status` 最多 64 字节，均为非空、无首尾空白和控制字符的 UTF-8 文本。
租户身份重复或任一目录项非法时整个查询失败，不返回部分目录。

## 用户管理调用

多租户模式使用[蓝鲸全局配置](../../guides/configuration.md#蓝鲸全局配置与-cmdb-实时目标读取)中的
`blueking.api_url/app_code/app_secret`，无需配置 `resources.cmdb`。

上游固定为 `GET <blueking.api_url>/api/bk-user/prod/api/v3/open/tenants/`，无查询参数。
`X-Bk-Tenant-Id` 固定为 `system`，应用鉴权中的 `bk_username` 固定为 `admin`；
这只是全局目录的引导调用身份，不改变任何业务数据的租户作用域，也不解析租户 `bk_admin`。
本接口不按来源配置或 Kingeye 初始化名单筛选；多租户查询失败时不回退单租户默认目录。

上游成功信封为 `{"code":0,"data":[...]}`；如果包含 `result`，必须为 `true`。
缺少或为 null 的 `data` 不作为空目录处理。每次实时读取、不缓存、不自动重试、不跟随重定向。
单次排队和 HTTP 调用总预算 3 秒，最多四个目录调用，超额立即失败，不积累无界等待者；
共享 HTTP 配额等待和请求响应取消、超时和关闭。
完整上游响应最多 1 MiB、最多 4096 个租户；超过预算明确失败，不静默截断。不提供分页。
响应使用 `Cache-Control: no-store`。

## 错误

| HTTP 状态 | 含义 |
| --- | --- |
| 400 | 请求包含查询参数 |
| 401 | 缺少或无效的管理 JWT，或使用 Worker Token |
| 502 | 用户管理 HTTP/业务失败、超时、响应非法或超过预算 |
| 503 | 多租户模式未配置蓝鲸应用连接，或租户目录服务未装配 |

错误返回固定文本，不回显上游 URL、凭据或响应体。未配置蓝鲸连接不影响单租户目录或其他控制面能力启动。

## 协议依据与验证边界

2026-10-13 核对本机 Kingeye `base/infras/third_party_api/user/api.py` 的 `list_tenant`，
以及 bk-monitor `api/bk_login/default.py` 的 `ListTenantResource`：均使用用户管理 v3
`open/tenants/`，目录字段为 `id/name/status`，全局查询使用 `system/admin`。
Linkd 的 HTTP 测试使用本地测试服务验证该协议和单租户模式不访问上游；真实 APIGW 权限、部署版本的响应和网络连通性需要部署联调验证。
