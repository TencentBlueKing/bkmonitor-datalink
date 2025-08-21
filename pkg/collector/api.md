# Receiver Api 接入

## 接入路由 (http)

### pushgateway (Prometheus Metrics)

- POST/PUT `/metrics/job@base64/{job}/{labels:.*}`
- POST/PUT `/metrics/job@base64/{job}`
- POST/PUT `/metrics/job/{job}/{labels:.*}`
- POST/PUT `/metrics/job/{job}`

### remotewrite (Prometheus Metrics)

- POST `/prometheus/write`

### otlp (OpenTelemetry Traces / Logs /Metrics)

- POST `/v1/traces`
- POST `/v1/metrics`
- POST `/v1/logs`

### jaeger (Traces)

- POST `/jaeger/v1/traces`

### skywalking (Traces)

- POST `/v3/segment`
- POST `/v3/segments`

### zipkin (Traces)

- POST `/api/v2/spans`

### pyroscope (Profiles)

- POST `/pyroscope/ingest`

### beat

- POST `/v1/beat`

### fta

- POST `/fta/v1/event`

### logpush

- POST `/v1/logpush`

## 鉴权

在上报数据到 bk-collector 时需要带上 token 进行鉴权，目前支持以下上报方式:

### 使用 Token Key

- http 请求在 url query 参数或请求头中携带 `X-BK-TOKEN`

```go
// 直接在 query param 中携带
fullUrl := fmt.Sprintf("%s?X-BK-TOKEN=%s", baseUrl, url.QueryEscape(token))

// 或在请求头中携带
req.Header.Set("X-BK-TOKEN", token)
```

- grpc 请求在 metadata 中携带 `X-BK-TOKEN`

```go
ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("X-BK-TOKEN", token))
response, err := client.YourMethod(ctx, &YourRequest{})
```

- tars 请求在 context 中携带 `X-BK-TOKEN`

```go
// 在 context 携带
app.ReportPropMsgWithContext(ctx, props, map[string]string{"X-BK-TOKEN": token})
```

### 使用 TenantId Key

tenant id key 支持 http/grpc 请求，具体使用方式同 Token Key

- http 请求在 url query 参数或请求头中携带 `X-Tps-TenantID`
- grpc 请求在 metadata 中携带 `X-Tps-TenantID`

### 使用 Basic Auth

- http 请求使用 basic auth 携带（这种方式当且仅当 username 为 bkmonitor 时有效⚠️）

```go
username := "bkmonitor"     // username 为 bkmonitor，password 填 token
req.SetBasicAuth(username, token)
```

### 使用 Bearer Auth

- http 请求在请求头中使用 bearer auth 形式携带 token

```go
req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
```

### 携带方式

| 请求类型 | URL query param | Header | bearer auth | basic auth | metadata | context |
|------|-----------------|--------|-------------|------------|----------|---------|
| http | ✅               | ✅      | ✅           | ✅          |          |         |
| grpc |                 |        |             |            | ✅        |         |
| tars |                 |        |             |            |          | ✅       |
