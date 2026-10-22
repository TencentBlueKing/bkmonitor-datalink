# RUM 字段协议

上游 SDK 字段参考 [Telemetry 文档](https://bk-rum-wiki.vercel.app/reference/telemetry)。网关消息格式见 [数据流](overview/architecture.md#输入)，本作业的 Elasticsearch 输出见 [预计算字段](Precalculate-index-fields.md)。

## 1. OTLP Span Envelope 字段

这些字段位于 Span 顶层，不属于 `attributes`。

| 字段 | 类型 | 条件 | 说明 |
|---|---|---|---|
| `traceId` | string | 必有 | 32 位十六进制字符串，16 字节 Trace ID |
| `spanId` | string | 必有 | 16 位十六进制字符串，当前 Span ID |
| `parentSpanId` | string | 存在活动父 Span 时 | 父 Span ID；根 Span 通常没有 |
| `links` | link[] | 注入或探测到 Trace Header 时 | 关联其他 Trace 的 Link |
| `name` | string | 必有 | Span 名，例如 `browser.view`、`browser.resource` |
| `kind` | enum number/string | 必有 | OpenTelemetry Span Kind，SDK 内置为 `INTERNAL` 或 `CLIENT` |
| `startTimeUnixNano` | string | 必有 | Span 开始时间，epoch 纳秒 |
| `endTimeUnixNano` | string | 必有 | Span 结束时间，epoch 纳秒 |
| `attributes` | key-value[] / object | 可选 | RUM 字段和第三方 Instrumentation 字段 |
| `events` | event[] | 可选 | `exception`、`long_task.script`、`long_task.attribution` 等事件 |
| `status` | object | 可选 | OpenTelemetry 状态，部分错误会设置为 `ERROR` |

---

## 2. Resource Attribute 字段

Resource 字段描述 SDK、应用和运行环境，同一 SDK 实例的 Span 通常共享这些字段。

| 字段 | 类型 | 条件 |
|---|---|---|
| `service.name` | string | 必有 |
| `service.version` | string | 配置 `application.version` 时 |
| `deployment.environment.name` | string | 必有，未配置时为 `production` |
| `session.sample_rate` | number | 必有，范围 `0..1` |
| `telemetry.sdk.name` | string | 必有，固定为 `@blueking/open-telemetry` |
| `telemetry.sdk.language` | string | 必有，固定为 `webjs` |
| `telemetry.sdk.version` | string | 必有 |
| `user_agent.name` | string | `tracking.device !== false` 且存在浏览器环境 |
| `user_agent.version` | string | 能识别浏览器版本时 |
| `user_agent.os.name` | string | `tracking.device !== false` 且存在浏览器环境 |
| `device.type` | string | `tracking.device !== false` 且存在浏览器环境 |
| `device.memory_gib` | number | 浏览器支持 `navigator.deviceMemory` 时 |

### 典型取值

- `user_agent.name`：`Edge`、`Chrome`、`Firefox`、`Safari`、`Other`
- `user_agent.os.name`：`iOS`、`Android`、`macOS`、`Windows`、`Linux`、`Other`
- `device.type`：`mobile`、`desktop`
- `device.memory_gib`：浏览器提供的近似内存容量，单位 GiB

---

## 3. 运行时公共字段

这些字段会被普通 Span 继承，通常位于 Span `attributes` 中。

### 用户、Session、View、Action

| 字段 | 类型 | 条件 |
|---|---|---|
| `user.id` | string | 调用 `setUser({ id })` 后 |
| `session.id` | string | Session 有效 |
| `session.start_time` | number | Session 有效，epoch ms |
| `session.type` | string | Session 有效，当前固定为 `user` |
| `session.has_replay` | boolean | Session 有效，当前固定为 `false` |
| `view.id` | string | View 有效 |
| `view.name` | string | 显式命名后 |
| `view.url` | string | View 有效 |
| `view.url_template` | string | View 有效 |
| `view.loading_type` | string | View 有效 |
| `action.id` | string[] | 自动 Action 活动中 |
| `device.id` | string | Device 插件开启 |

### View 相关取值

`view.loading_type` 可能为：

- `initial_load`
- `route_change`
- `bf_cache`
- `session_renewal`

### 浏览器视口与屏幕

| 字段 | 类型 | 条件 |
|---|---|---|
| `browser.viewport.width` | number | 浏览器环境 |
| `browser.viewport.height` | number | 浏览器环境 |
| `browser.screen.width` | number | 浏览器提供 `window.screen` 时 |
| `browser.screen.height` | number | 浏览器提供 `window.screen` 时 |

单位均为 CSS px。

### 网络状态

| 字段 | 类型 | 条件 |
|---|---|---|
| `network.status` | string | 浏览器环境 |
| `network.effective_type` | string | 支持 Network Information API |
| `network.connection.type` | string | 浏览器提供 `connection.type` 时 |

典型取值：

- `network.status`：`connected`、`not_connected`
- `network.effective_type`：`slow-2g`、`2g`、`3g`、`4g`
- `network.connection.type`：`wifi`、`cellular`、`ethernet`

---

## 4. RUM 公共分类字段

| 字段 | 类型 | 条件 | 典型取值 |
|---|---|---|---|
| `span_type` | string | 所有 SDK 原生 RUM Span | `session`、`view`、`resource`、`error`、`vital`、`long_task`、`action`、`websocket`、`custom` |
| `outcome.type` | string | 所有 SDK 原生 RUM Span | `success`、`warning`、`error`、`timeout`、`abort` |
| `outcome.reason` | string | 需要解释非正常结果时 | `network`、`network_timeout`、`http_4xx`、`http_5xx`、`blank_screen`、`csp` |

`outcome.type` 和 OTLP 顶层的 `status` 是两套不同概念：

- `outcome.type`：RUM 业务结果分类
- `status`：OpenTelemetry Span 状态

---

## 5. 内置 Span 清单

见 [Span 清单](RUM-Span.md)。

## 6. Session 字段

适用于 `browser.session`。

| 字段 | 类型 | 取值/条件 |
|---|---|---|
| `session.phase` | string | `start`、`rotate`、`end` |
| `session.id` | string | 所有阶段 |
| `session.start_time` | number | 所有阶段，epoch ms |
| `session.type` | string | 所有阶段，固定为 `user` |
| `session.has_replay` | boolean | 所有阶段，固定为 `false` |
| `session.lifecycle.reason` | string | 所有阶段 |
| `session.previous_id` | string | `rotate` 且可以识别旧 Session 时 |

`session.lifecycle.reason` 可能为：

- `init`
- `inactivity`
- `maxLifetime`
- `external`

Session Span 可以没有 `view.id`。

---

## 7. View 字段

适用于 `browser.view`。

| 字段 | 类型 | 条件/单位 |
|---|---|---|
| `view.phase` | string | `start`、`update`、`end` |
| `view.id` | string | 所有阶段 |
| `view.version` | number | 从 1 递增 |
| `view.started_at` | number | epoch ms |
| `view.name` | string | 显式命名后 |
| `view.url` | string | 所有阶段 |
| `view.url_template` | string | 所有阶段 |
| `view.previous_url_template` | string | 所有阶段 |
| `view.loading_type` | string | 所有阶段 |
| `view.loading_time` | number | 计算完成后，单位 ms |
| `view.loading_time_source` | string | 有 `view.loading_time` 时 |
| `view.end_reason` | string | 仅 `end` 阶段 |
| `url.previous` | string | 所有阶段 |
| `document.referrer` | string | 仅 `initial_load` |
| `view.first_byte` | number | 初始 View，Navigation Timing 可用时 |
| `view.dom_interactive` | number | 初始 View，Navigation Timing 可用时 |
| `view.dom_content_loaded` | number | 初始 View，Navigation Timing 可用时 |
| `view.dom_complete` | number | 初始 View，Navigation Timing 可用时 |
| `view.load_event` | number | 初始 View，Navigation Timing 可用且值有效时 |

### `view.loading_time_source`

- `auto`
- `manual`

### `view.end_reason`

可能为：

- 路由来源值
- `manual`
- `bfcache`
- `pagehide`
- `session_expired`
- `shutdown`

---

## 8. 请求与静态资源字段

适用于 `browser.resource`。

### 基础请求字段

| 字段 | 类型 | 条件 |
|---|---|---|
| `url.full` | string | 存在有效请求/资源 URL |
| `url.template` | string | Fetch/XHR |
| `server.address` | string | URL 可解析 |
| `server.port` | number | URL 显式包含端口 |
| `resource.type` | string | 必有 |
| `http.request.method` | string | Fetch/XHR |
| `http.response.status_code` | number | 获得真实 HTTP Response |
| `resource.size` | number | 有 Resource Timing 详情 |
| `resource.protocol` | string | 浏览器提供 `nextHopProtocol` |
| `resource.cache.hit` | boolean | SDK 能推断缓存命中时 |
| `resource.delivery_type` | string | 浏览器支持 `deliveryType` |
| `resource.render_blocking_status` | string | 浏览器支持 `renderBlockingStatus` |
| `resource.encoded_body_size` | number | 有 Resource Timing |
| `resource.decoded_body_size` | number | 有 Resource Timing |
| `resource.transfer_size` | number | 有 Resource Timing |

### `resource.type` 典型取值

- Fetch：`fetch`
- XHR：`xhr`
- 静态资源：`script`、`link`、`img`、`css`
- 无法识别：`other`

### 网络阶段字段

以下字段均为 number，单位 ms。

| 字段 | 条件 |
|---|---|
| `resource.redirect.start` | 存在合法重定向时间 |
| `resource.redirect.duration` | 存在合法重定向时间 |
| `resource.worker.start` | Service Worker 在 fetch 前介入 |
| `resource.worker.duration` | Service Worker 在 fetch 前介入 |
| `resource.dns.start` | DNS 阶段发生或可见 |
| `resource.dns.duration` | DNS 阶段发生或可见 |
| `resource.connect.start` | 连接阶段发生或可见 |
| `resource.connect.duration` | 连接阶段发生或可见 |
| `resource.ssl.start` | 存在 `secureConnectionStart` |
| `resource.ssl.duration` | 存在 `secureConnectionStart` |
| `resource.first_byte.start` | 网络时间点完整且有序 |
| `resource.first_byte.duration` | 网络时间点完整且有序 |
| `resource.download.start` | 网络时间点完整且有序 |
| `resource.download.duration` | 网络时间点完整且有序 |

### 请求结果映射

| 场景 | `outcome.type` | `outcome.reason` |
|---|---|---|
| HTTP 408/504 | `timeout` | `network_timeout` |
| 其他 4xx | `error` | `http_4xx` |
| 其他 5xx | `error` | `http_5xx` |
| Fetch `AbortError` / XHR abort | `abort` | 不写 |
| Fetch `TimeoutError` / XHR timeout | `timeout` | `network_timeout` |
| 其他网络失败 | `error` | `network` |
| 其他成功请求 | `success` | 不写 |

---

## 9. Trace Link 字段

命中 `allowedTracingUrls` 的请求会通过 Span Link 关联后端 Trace。

Link 本身包含 Trace ID、Span ID、Flags，以及以下 Link Attributes：

| 字段 | 类型 | 条件 |
|---|---|---|
| `format` | string | 恒有 |
| `injected` | boolean | 恒有 |
| 原始 Header 名 | string | 对应 Header 存在时 |

### `format` 可能取值

- `traceparent`
- `sentry-trace`
- `b3`
- `b3-multi`
- `datadog`
- `sw8`

例如原始 Header 字段可能为：

- `traceparent`
- `b3`
- `x-datadog-trace-id`
- `sw8`

此外，Link 中还存在标准关联字段：

- `trace_id`
- `span_id`
- `trace_flags`

注意：

- Link Attributes 不经过 `privacy.redactAttributes`
- `tracestate` 不会被记录
- 无法映射为合法 OTel ID 的格式可能使用随机占位 ID

---

## 10. 错误字段

适用于：

- `browser.error`
- `browser.unhandledrejection`
- `browser.resource_error`
- `csp.violation` 中的部分公共错误字段

| 字段 | 类型 | 适用范围 |
|---|---|---|
| `error.message` | string | 三类自动错误 |
| `error.handled` | boolean | 三类自动错误 |
| `error.source` | string | 自动错误/CSP |
| `code.filepath` | string | `browser.error` |
| `code.lineno` | number | `browser.error` |
| `code.column` | number | `browser.error` |
| `error.cross_origin` | boolean | 跨域 `Script error.` |
| `url.full` | string | `browser.resource_error` |
| `html.tag` | string | `browser.resource_error` |

### `error.source` 取值

- `window.error`
- `unhandledrejection`
- `resource`
- `csp`

### 错误公共结果

三类自动浏览器错误通常：

```text
outcome.type = error
status.code = ERROR
error.handled = false
```

---

## 11. Exception Span Event 字段

当 Span 内有 `exception` Event 时，包含：

| 字段 | 类型 | 条件 |
|---|---|---|
| `exception.type` | string | 能识别异常类型时 |
| `exception.message` | string | 能提取异常消息时 |
| `exception.stacktrace` | string | Error 存在 stack 时 |

可能产生 Exception Event 的场景：

- `browser.error`
- `browser.unhandledrejection`
- `browser.resource_error`
- Fetch timeout
- Fetch network reject
- Fetch/XHR 同步抛出 Error
- 带 `error` 的 `custom.<name>`

主动 abort 不记录 `exception` Event。

---

## 12. Web Vitals 字段

适用于 `browser.web_vital`。

SDK 当前支持：

- CLS
- FCP
- INP
- LCP
- TTFB

### 公共 Vital 字段

| 字段 | 类型 | 条件 |
|---|---|---|
| `vital.metric` | string | 必有 |
| `vital.value` | number | 必有 |
| `vital.rating` | string | 必有 |
| `vital.id` | string | 必有 |

### `vital.metric` 取值

- `cls`
- `fcp`
- `inp`
- `lcp`
- `ttfb`

### `vital.rating` 取值及阈值

| 指标 | good | needs-improvement | poor |
|---|---:|---:|---:|
| CLS | ≤ 0.1 | ≤ 0.25 | > 0.25 |
| INP | ≤ 200 ms | ≤ 500 ms | > 500 ms |
| FCP | ≤ 1800 ms | ≤ 3000 ms | > 3000 ms |
| LCP | ≤ 2500 ms | ≤ 4000 ms | > 4000 ms |
| TTFB | ≤ 800 ms | ≤ 1800 ms | > 1800 ms |

### 结果映射

- `vital.rating=good` → `outcome.type=success`
- 其他评级 → `outcome.type=warning`

---

### 12.1 LCP Attribution 字段

| 字段 | 类型 | 条件 |
|---|---|---|
| `vital.lcp.url` | string | LCP 是图片等 URL 资源时 |
| `vital.lcp.target` | string | 能定位 LCP 元素时 |
| `vital.lcp.element_render_delay` | number | 单位 ms |
| `vital.lcp.resource_load_duration` | number | 单位 ms |
| `vital.lcp.time_to_first_byte` | number | 单位 ms |

---

### 12.2 CLS Attribution 字段

| 字段 | 类型 | 条件 |
|---|---|---|
| `vital.cls.largest_shift_target` | string | 能定位元素时 |
| `vital.cls.largest_shift_value` | number | 无量纲 |
| `vital.cls.load_state` | string | 当前不会产生 |

注意：`vital.cls.load_state` 代码中虽然预留，但当前 SDK 不会写入。

---

### 12.3 INP Attribution 字段

| 字段 | 类型 | 条件 |
|---|---|---|
| `vital.inp.interaction_target` | string | 能定位交互元素时 |
| `vital.inp.interaction_type` | string | 必有 |
| `vital.inp.input_delay` | number | 单位 ms |
| `vital.inp.processing_duration` | number | 单位 ms |
| `vital.inp.presentation_delay` | number | 单位 ms |

通常满足：

```text
input_delay + processing_duration + presentation_delay ≈ vital.value
```

---

### 12.4 FCP Attribution 字段

| 字段 | 类型 | 条件 |
|---|---|---|
| `vital.fcp.time_to_first_byte` | number | 单位 ms |
| `vital.fcp.load_state` | string | 必有 |

`vital.fcp.load_state` 可能为：

- `loading`
- `dom-interactive`
- `dom-content-loaded`
- `complete`

---

### 12.5 TTFB Attribution 字段

| 字段 | 类型 | 单位 |
|---|---|---|
| `vital.ttfb.waiting_duration` | number | ms |
| `vital.ttfb.dns_duration` | number | ms |
| `vital.ttfb.connection_duration` | number | ms |
| `vital.ttfb.request_duration` | number | ms |

---

## 13. Action 字段

适用于：

- `action.click`
- `action.custom`

| 字段 | 类型 | 自动 Click | 手动 Action |
|---|---|---:|---:|
| `action.id` | string | 必有 | 必有 |
| `action.type` | string | 固定 `click` | 固定 `custom` |
| `action.name` | string | `Click on <target>` | payload 中的 `name` |
| `action.target.name` | string | 必有 | 不写 |
| `action.target.tag` | string | 必有 | 不写 |
| `action.loading_time` | number | 有可观察页面活动时 | 不写 |
| `action.frustration.type` | string[] | 检出挫败信号时 | 不写 |

### `action.frustration.type` 取值

- `rage_click`
- `dead_click`
- `error_click`

### 相关规则

- `rage_click`：1 秒内、100px 范围内至少连续三次点击
- `dead_click`：点击后没有页面活动、输入、滚动或选择反馈
- `error_click`：Action 生命周期内出现错误
- 存在错误或挫败信号时，`outcome.type=warning`

---

## 14. 白屏字段

适用于 `browser.blank_screen`。

| 字段 | 类型 | 条件 |
|---|---|---|
| `blank_screen.reason` | string | 必有 |
| `blank_screen.empty_ratio` | number | `0..1` |
| `blank_screen.sample_count` | number | 必有，当前固定为 9 |
| `blank_screen.valid_sample_count` | number | 必有 |
| `blank_screen.empty_sample_count` | number | 必有 |
| `blank_screen.ignored_sample_count` | number | 必有 |
| `blank_screen.root_selector` | string | 必有 |
| `blank_screen.root_found` | boolean | 必有 |
| `blank_screen.center_element` | string | 必有，可能为空 |

### `blank_screen.reason` 取值

- `missing_root`
- `empty_viewport`

白屏 Span 固定为：

```text
span_type = error
outcome.type = error
outcome.reason = blank_screen
```

---

## 15. Long Task 字段

适用于 `browser.long_task`。

优先使用 Long Animation Frame API，不支持时回退到 Long Tasks API。

### Span 顶层 Long Task 字段

| 字段 | 类型 | 条件/单位 |
|---|---|---|
| `long_task.id` | string | 必有 |
| `long_task.entry_type` | string | 必有 |
| `long_task.duration` | number | 必有，ms |
| `long_task.name` | string | 必有 |
| `long_task.start_time` | number | 仅 LoAF，相对 timeOrigin ms |
| `long_task.blocking_duration` | number | 仅 LoAF，ms |
| `long_task.first_ui_event_timestamp` | number | LoAF 提供时，相对 timeOrigin ms |
| `long_task.render_start` | number | LoAF 提供时，相对 timeOrigin ms |
| `long_task.style_and_layout_start` | number | LoAF 提供时，相对 timeOrigin ms |

`long_task.entry_type`：

- `long-animation-frame`
- `long-task`

---

### 15.1 `long_task.script` Span Event 字段

每个 LoAF `scripts` 条目生成一个 `long_task.script` Event。

| 字段 | 类型 | 单位/条件 |
|---|---|---|
| `long_task.script.duration` | number | ms |
| `long_task.script.execution_start` | number | 相对 timeOrigin ms |
| `long_task.script.forced_style_and_layout_duration` | number | ms |
| `long_task.script.invoker` | string | 浏览器提供 |
| `long_task.script.invoker_type` | string | 浏览器提供 |
| `long_task.script.pause_duration` | number | ms |
| `long_task.script.source_char_position` | number | 字符偏移 |
| `long_task.script.source_function_name` | string | 浏览器提供 |
| `long_task.script.source_url` | string | URL，经脱敏 |
| `long_task.script.start_time` | number | 相对 timeOrigin ms |
| `long_task.script.window_attribution` | string | 浏览器提供 |

---

### 15.2 `long_task.attribution` Span Event 字段

Legacy Long Task 的每个 attribution 条目生成一个 Event。

| 字段 | 类型 | 条件 |
|---|---|---|
| `long_task.attribution.index` | number | 必有，0-based |
| `long_task.attribution.container_id` | string | 浏览器提供时 |
| `long_task.attribution.container_name` | string | 浏览器提供时 |
| `long_task.attribution.container_src` | string | 浏览器提供时，经脱敏 |
| `long_task.attribution.container_type` | string | 浏览器提供时 |
| `long_task.attribution.name` | string | 浏览器提供时 |

---

## 16. CSP 字段

适用于 `csp.violation`。

| 字段 | 类型 | 条件 |
|---|---|---|
| `csp.blocked_uri` | string | 浏览器提供时 |
| `csp.violated_directive` | string | 必有 |
| `csp.effective_directive` | string | 必有 |
| `csp.disposition` | string | 必有 |
| `csp.source_file` | string | 能定位触发代码时 |
| `csp.line_number` | number | 浏览器提供时 |
| `csp.column_number` | number | 浏览器提供时 |
| `csp.status_code` | number | 必有 |
| `csp.original_policy` | string | 必有 |
| `error.source` | string | 必有，固定为 `csp` |

CSP Span 固定为：

```text
span_type = error
outcome.type = error
outcome.reason = csp
error.source = csp
```

---

## 17. WebSocket 字段

适用于 `browser.websocket`。

每个 WebSocket 连接会产生四类瞬时 Span：

- `connect`
- `open`
- `error`
- `close`

| 字段 | 类型 | 条件 |
|---|---|---|
| `websocket.phase` | string | 必有 |
| `websocket.connection.id` | string | 必有 |
| `network.protocol.name` | string | 必有，固定为 `websocket` |
| `url.full` | string | 必有 |
| `url.scheme` | string | URL 可解析时 |
| `server.address` | string | URL 可解析时 |
| `server.port` | number | URL 显式包含端口时 |
| `websocket.error.phase` | string | 仅 `error` |
| `websocket.close.code` | number | 仅 `close` |
| `websocket.close.reason` | string | 仅 `close` |
| `websocket.close.was_clean` | boolean | 仅 `close` |

### `websocket.phase`

- `connect`
- `open`
- `error`
- `close`

### `websocket.error.phase`

- `connect`：连接建立前发生错误
- `runtime`：连接已经成功打开后发生错误

### WebSocket 结果

- `error` 阶段：`outcome.type=error`、`outcome.reason=network`
- 其他阶段：`outcome.type=success`

SDK 当前不采集：

- WebSocket 消息内容
- 消息数量
- 字节数

---

## 18. Custom Event 字段

`reportCustomEvent({ name, attributes, error })` 会创建：

```text
custom.<name>
```

固定字段如下：

| 字段 | 类型 | 条件 |
|---|---|---|
| `span_type` | string | 必有，固定为 `custom` |
| `outcome.type` | string | 必有 |
| `name` | Span Name | 来自调用方的 `name`，拼接为 `custom.<name>` |
| 自定义 attributes | OTel Attribute | 由调用方传入 |

### `outcome.type`

- 没有 `error`：`success`
- 传入 Error：`error`

Custom Event 还可能合并：

- `context.attributes.page()`
- `context.attributes.custom()`
- payload attributes
- 全局运行时上下文

---

## 19. 业务扩展字段

以下字段无法由固定字典穷举，实际名称由接入方定义。

| 来源 | 进入范围 |
|---|---|
| `context.attributes.page()` | 多数 SDK Span |
| `context.attributes.error()` | 三类自动错误 Span |
| `context.attributes.custom()` | `custom.<name>` |
| `startView({ attributes })` | View 生命周期 Span 和运行时上下文 |
| `reportAction` payload attributes | `action.custom` |
| `reportCustomEvent` payload attributes | `custom.<name>` |
| `useInstrumentation()` | 第三方 OpenTelemetry Instrumentation Span |

自定义字段必须符合 OpenTelemetry Attribute 类型：

- string
- number
- boolean
- 对应数组类型

不建议传入：

- 对象
- 函数
- 嵌套 JSON

---

## 20. 当前 SDK 不会产生的字段和信号

以下内容不属于当前 SDK 原生产出：

| 不会产生的内容 |
|---|
| `/v1/metrics` 请求 |
| `/v1/logs` 请求 |
| Counter |
| Histogram |
| Gauge |
| LogRecord |
| `session.duration` |
| `view.duration` |
| `vital.cls.load_state` |
| Action count |
| Resource count |
| Error count |
| Long Task count |
| 错误 fingerprint/group |
| HTTP Request Body |
| HTTP Response Body |
| HTTP Header |
| WebSocket message 内容 |
| WebSocket 消息数量 |
| WebSocket 字节数 |
| Session Replay 数据 |

其中：

- `session.duration`、`view.duration` 需要由后端根据生命周期事件派生
- 各类 count 需要从原始 Span 聚合
- 错误分组需要由后端根据 `exception`、`message`、`stacktrace` 等字段计算
- Trace Context 注入不代表 SDK 会采集 HTTP Header 或 Body
