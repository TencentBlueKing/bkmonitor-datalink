// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
// package aegis 将 Aegis V2 报文转换为 BK-RUM Traces。
//
// 唯一转换入口是 ConvertJSON。输入结构与解析在 payload.go，批次状态、
// Action 去重和唯一事件分发点在 convert.go，来源公共上下文在 context.go。
// 各事件的来源解释与映射放在 view.go、resource.go、action.go、session.go、
// error.go、websocket.go、vital.go 和 custom.go；包内类型不暴露给接入层。
//
// 映射完成后统一调用 bkrum 的缺省与结构校验。该包只依赖目标协议和 pdata，
// 不引用 HTTP、receiver、pipeline 或 define，不发布 Record，也不解码 OTLP。
// ErrNotAegis 只表示输入不属于 Aegis；报文损坏不会触发其他协议回退。
//
// 新增来源事件：在 appendMessage 选择映射函数，将来源字段解释放进对应事件
// 文件，并补充同名测试。新增目标事件时，再扩充 bkrum 的类型、名称和 Kind
// 约束。测试既覆盖嵌套 JSON 输入，也断言最终 OTLP 属性类型、状态和时间。
//
// 协议依据（2026-10-15 核对）：
//   - https://bk-rum-wiki.vercel.app/reference/span-protocol
//   - https://bk-rum-wiki.vercel.app/reference/telemetry
//   - https://bk-rum-wiki.vercel.app/features/views-routing
//
// 空消息（null、空串、内层 null 或空对象）直接跳过，不生成消息 Span。
// 明确的已支持类型优先于消息正文；仅空类型或 normal 在未命中插件和错误
// 判定时使用消息别名兼容，避免错误文本被误识别为 PV 或 Vital。
// Action 仅在单请求内去重，跨批次统计需由下游按应用、Session、Action ID 去重。
//
// # 类型映射
//
// Aegis 类型 -> Span 名称 / span_type / Kind：
//   - api、assets_speed -> browser.resource / resource / CLIENT。
//   - pv（含 spa）-> browser.view / view / INTERNAL，view.phase=start。
//   - page_performance -> browser.view / view / INTERNAL，view.phase=update。
//   - session -> browser.session / session / INTERNAL；Session 插件的
//     session_type=view/action 记录分别分流到 View 与 Action 映射。
//   - normal+error -> browser.error / error / INTERNAL。
//   - promise_error -> browser.unhandledrejection / error / INTERNAL。
//   - resource_error、asset_error -> browser.resource_error / error / INTERNAL。
//   - web_vitals -> browser.web_vital / vital / INTERNAL，每项一个 Span。
//   - fields.action -> action.<action_type> / action / INTERNAL；手动 Action
//     使用 action.<action_name>。同一请求内按非空 action.id 去重。
//   - websocket -> browser.websocket / websocket / CLIENT。
//   - custom_event -> custom.<name> / custom / INTERNAL。
//   - 未识别类型 -> custom.<type>，仅保留公共上下文和显式映射字段。
//
// Resource 使用 timestamp + duration（ms）表达请求耗时；Action 用 Span 起止
// 时间表达耗时；其他当前支持的事件均为瞬时 Span。OTLP 时间戳保持纳秒，后端
// 再转换为 start_time、end_time、elapsed_time（微秒）。app_name、bk_biz_id 沿用接收流水线处理，
// 不复制成 Span Attribute，也不把 topic 当作 service.name。
//
// # 公共字段
//
// bean.version -> Resource telemetry.sdk.version（不是 service.version）；
// bean.env -> deployment.environment.name；SDK name=aegis、language=webjs。
// bean.platform 的已知操作系统值 -> Resource user_agent.os.name。
// service.name 沿用 unknown_service 缺省值；缺失的应用版本、采样率和浏览器
// 名称不推测。session.id 不放在 Resource，防止同批不同会话串用首条 ID。
//
// bean.uid/aid -> user.id/device.id；fields.session.id -> session.id，
// session.type=user；has_replay 仅在源字段存在时写入。
// fields.view -> view.id/name/url/loading_type；view_url 缺失时回退 fields.from。
// fields.action.id 同时放在其关联的各条 Span 上。
// bean.vp/sr -> browser.viewport/screen 的 width、height（整数）。
// netType: wired -> ethernet，移动网络代际 -> cellular，wifi 保持 wifi；
// 仅 slow-2g、2g、3g、4g 同时写入 network.effective_type。
// none -> network.status=not_connected；其他连接类型不推断在线状态。
//
// # 领域字段
//
// Resource：requestType -> resource.type（xmlhttprequest -> xhr）；静态资源
// 优先使用 initiatorType，assetType=css/js/image 分别回退 link/script/img。
// method/status -> http.request.method/http.response.status_code。
// URL 拆为 url.full/scheme/template、server.address/port；端口仅在显式提供时写入。
// View 和请求模板将数字、UUID、至少 16 位十六进制路径段替换为 :id，去掉查询参数。
// transferSize/encodedBodySize/decodedBodySize -> resource.*_size；
// resource.size=decodedBodySize。仅 deliveryType=cache 或 transferSize=0 且
// decodedBodySize>0 时写 resource.cache.hit=true，避免把 TAO 限制误判为缓存。
// nextHopProtocol -> resource.protocol；domainLookup/connectTime/tlsTime/
// requestTime/responseTime -> dns/connect/ssl/first_byte/download.duration。
// 明确为 HTTP 或大于建连耗时的 TLS 值不写入 resource.ssl.duration。
// 缺失的阶段起点不通过累加耗时伪造；不再生成旧的阶段 Span Event。
//
// HTTP 408/504、TimeoutError -> outcome.type=timeout，AbortError -> abort，
// 其余请求失败 -> error，正常 -> success。HTTP 状态错误本身不创建异常 Event。
// JS/Promise/资源错误写 error.source/handled/message 和 code.*，异常详情写入
// 名为 exception 的 Event（exception.type/message/stacktrace），设置 Status ERROR。
// SDK aegis.error() 上报 level=custom_error，按 window.error 处理。
// 不再把 exception.* 放在 Span 顶层 Attributes。
//
// View：首次加载的 referrer -> view.referrer；路由切换的 referrer ->
// view.previous/previous_url_template。初始 View 的 previous/template 为空。
// PV/SPA 为 start，page_performance 为 update；仅 initial_load 的导航时间点
// responseStart/domInteractive/domContentLoadedEventEnd/domComplete/loadEventEnd
// 分别写 view.first_byte/dom_interactive/dom_content_loaded/dom_complete/load_event。
// 值相对于 performance.timeOrigin，以毫秒保留，必须有限且严格大于零。
//
// Session：aegis-web-sdk-v2 2.5.38/2.6.12 起 Session 插件统一上报
// fields.type=session，按 message.session_type 分流。view -> browser.view，
// is_active=true/false 分别映射 view.phase=start/end，缺失时不推断阶段；
// action 不另生成 Span，结束记录（is_active=false）用同批开始与结束消息的
// 时间差补写该 Action Span 的结束时间表达耗时；message.timestamp 经服务端
// 校时，与客户端时钟的 fields.action.timestamp 不可直接相减，缺少开始消息时
// 保持瞬时 Span。
// session 及旧版记录仍生成 browser.session：显式 session_phase/session_type=
// start、rotate、end 原样映射，其次 is_active=true/false 映射 start/end，
// session_from=local_generate 回退 start；无法判定生命周期阶段时省略。
// Action：action_target_name -> action.target.name，移除旧 action.name/target_name。
// WebSocket：successFlag -> websocket.phase=open/error，显式 phase 支持四个阶段；
// connectionId -> websocket.connection.id。生命周期 Span 瞬时，错误仅写 RUM Outcome。
// 有合法 message.traceparent 的 Fetch/XHR 才创建后端 Trace Link，referrer 不再建 Link。
//
// Vital：标准 FCP/LCP/INP/TTFB/CLS -> vital.metric（小写）、value、id。
// FID 不属于新协议，负数和非有限数不输出，零值保留；CLS 无量纲，其他为 ms。
// 良好值 -> outcome.type=success，其余 -> warning，不设置 Status ERROR，
// 不再写 vital.rating。优先用 aegisv2_goto+指标名作为 ID，否则生成唯一 ID。
//
// # 兼容边界
//
// Aegis 未提供的 view.version/started_at/end、Session 轮换原因、WebSocket
// 连接 ID、Vital 归因，以及 Long Task/白屏/CSP 的专用采集数据不能凭空补齐。
// firstScreenTiming 是首屏时间，ttfb 是请求阶段耗时，均不等同于 BK-RUM 的
// view.loading_time 或 view.first_byte；当前丢弃这些无对应 View 字段的阶段值。
// 不输出 aegisv2.ext.*，也不为已映射字段保留原始副本。
// 旧 event.*、span_subtype、result/error_type、target_*、rum.page.* 和阶段 Event
// 不再作为语义字段输出。接收器不补建缺失的生命周期，也不跨请求维护 View 状态。
package aegis
