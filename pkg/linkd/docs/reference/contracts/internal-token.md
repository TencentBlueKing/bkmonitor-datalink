# 内部 HTTP JWT 认证

Linkd 管理 API `/api/v1/*` 使用与 Kingeye 兼容的共享密钥 JWT，权限等同原管理 API Token。`/internal/*` 是 Worker 调度协议，仍使用独立的 `Authorization: Bearer <worker_token>`，管理 JWT 无权调用。

## 请求与身份

```http
GET /api/v1/metrics/catalog HTTP/1.1
Internal-Token: Bearer <JWT>
```

签名算法固定为 `HS256`，payload 必须包含非空字符串 `username`。Linkd 不创建本地用户、不基于用户名划分角色；认证用户名进入请求上下文，但不会替换关闭告警等业务命令中的 `operator_id`。

认证头必须恰好出现一次，使用 `Bearer ` 前缀，整个头值最多 8 KiB。缺失、畸形、错误算法、无效签名、无效用户名或时间声明均返回 401；不回退到旧 `Authorization` 管理 Token。

Kingeye 当前生成器只签入 `username`，Linkd 接受此类无 `exp` 的 Token。若存在 `exp`、`nbf` 或 `iat`，必须是有效的数值时间声明，并校验到期或未来时间，不额外增加时钟宽限。Linkd 与 Console 每次请求重新签发 `username`、`iat`、`exp`，有效期固定为 300 秒。部署节点应保持时钟同步。

业务租户仍按各接口已有请求体或来源配置校验。调用 Kingeye 时按目标接口需要独立传递 `X-Bk-Tenant-Id`；该头不在当前 JWT 签名载荷中，也不会自动改变 Linkd 接口的租户作用域。共享密钥代表部署内的管理互信，不提供租户授权。

## 配置与升级

```yaml
dispatch:
  jwt:
    secret_key: "<与 Kingeye BKAPP_JWT_SECRET_KEY 相同的密钥>"
    username: admin
  worker_token: "<独立 Worker Token>"
```

- `LINKD_JWT_SECRET_KEY`、`LINKD_JWT_USERNAME` 分别覆盖 YAML 中的密钥、签发用户名，用户名默认为 `admin`。
- 控制面与迁移 Job 要求 JWT 密钥非空、且与 Worker Token 不同；纯 Worker 不需要 JWT 密钥。
- Console 只在服务端持有密钥和签发 Token；浏览器继续使用 Console 自己的访问认证。
- 旧 `dispatch.api_token`、`LINKD_API_TOKEN`、Helm `auth.apiTokenKey` 已移除。Helm 使用 `auth.jwtSecretKey` 指定 Secret key，默认 `jwt-secret-key`。
- 升级时同步替换控制面、Console、来源导入 CLI 和 Secret；不支持新旧管理认证混用。无需数据库迁移，配置变更需重启相关进程。

## Python 调用 Linkd

以下示例使用 PyJWT，密钥从服务端环境读取，Token 不应打印或写入日志：

```python
import os
import time
import urllib.request
import jwt

now = int(time.time())
token = jwt.encode(
    {"username": "admin", "iat": now, "exp": now + 300},
    os.environ["BKAPP_JWT_SECRET_KEY"],
    algorithm="HS256",
)
request = urllib.request.Request(
    "http://linkd-control-plane:8090/api/v1/metrics/catalog",
    headers={"Internal-Token": f"Bearer {token}"},
)

# Internal-Token 是自定义头，禁止自动重定向到其他目标。
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

with urllib.request.build_opener(NoRedirect()).open(request, timeout=10) as response:
    catalog = response.read()
```

Kingeye 现有 `JwtTokenToolkit.encode()` 返回值已带 `Bearer `，可直接填入 `Internal-Token`，不要重复添加前缀。

## Linkd 调用 Kingeye

Go 内部调用方复用 `linkd/internal/internaltoken`。以下片段在 Linkd 模块内部使用，`ctx`、目标地址、租户和操作用户名由实际调用用例提供：

```go
signer, err := internaltoken.New(secretKey, nil)
if err != nil {
    return err
}
value, err := signer.Sign(username)
if err != nil {
    return err
}
request, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
if err != nil {
    return err
}
request.Header.Set(internaltoken.HeaderName, value)
request.Header.Set("X-Bk-Tenant-Id", tenantID)
client := &http.Client{
    Timeout: 10 * time.Second,
    CheckRedirect: func(*http.Request, []*http.Request) error {
        return http.ErrUseLastResponse
    },
}
response, err := client.Do(request)
if err != nil {
    return err
}
defer response.Body.Close()
if response.StatusCode < 200 || response.StatusCode >= 300 {
    return fmt.Errorf("Kingeye HTTP %d", response.StatusCode)
}
```

本次提供协议工具及现有管理调用方改造，不新增 Kingeye 业务调用或远程签发接口。具体目标 API、任务触发、业务幂等及响应语义由后续用例确定。

## 验证与边界

Go 验证使用固定版本 `github.com/golang-jwt/jwt/v5`，通过窄封装限定 HS256 和声明校验；Node 仅用标准库签发。该库采用 MIT 许可证，由独立维护团队维护，标准库和既有依赖不提供完整 JWT 验证能力。升级需重跑协议和互操作回归测试。[项目说明](https://github.com/golang-jwt/jwt)

普通测试使用固定测试密钥与 PyJWT 2.13.0 样本，覆盖 Go/Node 签发一致性和 Go 验证；不依赖在线 Kingeye。真实 Kingeye 接口、用户权限和 Kubernetes 部署需要单独联调。

该协议没有 nonce 消费记录或一次性 Token。无 `exp` 的旧 Token 仍可重用；新 Token 在五分钟有效窗口内也可重放。JWT 不绑定方法、路径、请求体或租户头，业务重试继续使用原有幂等身份。
