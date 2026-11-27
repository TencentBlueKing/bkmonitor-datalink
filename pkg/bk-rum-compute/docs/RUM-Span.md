# Span 清单

| Span 名 | Kind | span_type | 领域分类字段 | 默认状态/入口 | 表达的事件 |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `browser.session` | INTERNAL | session | session.phase | 始终随 SDK 启动 | Session 创建、轮换和结束生命周期。 |
| `browser.view` | INTERNAL | view | view.phase | 自动；可切为手动 View | 初始页面或 SPA View 的 start/update/end 快照。 |
| `browser.resource` | CLIENT | resource | resource.type | 请求、静态资源默认开启 | Fetch、XHR 或静态资源加载。 |
| `browser.error` | INTERNAL | error | error.source=window.error | 错误采集默认开启 | window.error 捕获的 JavaScript 运行时错误。 |
| `browser.unhandledrejection` | INTERNAL | error | error.source=unhandledrejection | 错误采集默认开启 | 未处理的 Promise rejection。 |
| `browser.resource_error` | INTERNAL | error | error.source=resource | 错误采集默认开启 | script、img、link、media 等元素资源加载失败。 |
| `browser.web_vital` | INTERNAL | vital | vital.metric | 默认开启 | 单项用户体验指标及归因。 |
| `action.click` | INTERNAL | action | action.type=click | 自动 Action 默认开启 | 自动点击 Action 及其页面活动、错误和挫败归因。 |
| `action.custom` | INTERNAL | action | action.type=custom | reportAction | 主动上报的业务 Action。 |
| `browser.blank_screen` | INTERNAL | error | blank_screen.reason | 默认开启 | 视口九宫格连续检测确认的白屏。 |
| `browser.long_task` | INTERNAL | long_task | long_task.entry_type | tracking.longTask，默认关闭 | Long Animation Frame，或不支持时回退的 Long Task。 |
| `csp.violation` | INTERNAL | error | error.source=csp | tracking.cspViolation，默认关闭 | 浏览器 securitypolicyviolation 事件。 |
| `browser.websocket` | CLIENT | websocket | websocket.phase | tracking.websocket，默认关闭 | 同一 WebSocket 连接的四类生命周期事件。 |
| `custom.<name>` | INTERNAL | custom | Span 名 | — | 自定义业务 Span。 |

各类 Span 的字段和取值见 [SDK 字段](RUM-SDK-Fields.md)，本作业的输出见 [预计算字段](Precalculate-index-fields.md)。
