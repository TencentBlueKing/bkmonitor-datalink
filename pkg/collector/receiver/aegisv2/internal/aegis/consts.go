// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package aegis

// 本文件集中 aegis 转换器使用的所有常量，按用途分组。命名约定：
//   - attr...：OTel attribute key，写入 Span/Resource/Event/Link 属性
//   - span...：固定 Span 名称
//   - event...：Span Event 名称
//   - view/session/ws...：阶段值
//   - resource/error/...type/...name：分类值或固定字符串

// -----------------------------------------------------------------------------
// Span 名称（写入 Span.Name）
// -----------------------------------------------------------------------------

const (
	spanViewStart  = "start"
	spanViewUpdate = "update"
	// spanNetProtocolWS 是 WebSocket 协议名，写入 network.protocol.name 属性。
	spanNetProtocolWS = "websocket"
)

// -----------------------------------------------------------------------------
// Span Event 名称（写入 Event.Name）
// -----------------------------------------------------------------------------

const eventNameException = "exception"

// -----------------------------------------------------------------------------
// OTel attribute keys：公共与 SDK
// -----------------------------------------------------------------------------

const (
	attrTelemetrySDKName    = "telemetry.sdk.name"
	attrTelemetrySDKLang    = "telemetry.sdk.language"
	attrTelemetrySDKVersion = "telemetry.sdk.version"
	attrDeploymentEnvName   = "deployment.environment.name"
	attrUserAgentOSName     = "user_agent.os.name"
)

// SDK 自身固定值。
const (
	sdkNameAegis = "aegis"
	sdkLangWebJS = "webjs"
	scopeName    = "aegisv2.collect"
)

// -----------------------------------------------------------------------------
// OTel attribute keys：URL 与服务端
// -----------------------------------------------------------------------------

const (
	attrURLFull       = "url.full"
	attrURLTemplate   = "url.template"
	attrURLScheme     = "url.scheme"
	attrServerAddress = "server.address"
	attrServerPort    = "server.port"
)

// URL 解析/模板化使用。
const (
	urlSchemeHTTP   = "http"
	urlTemplateID   = ":id"
	urlTemplateRoot = "/"
)

// -----------------------------------------------------------------------------
// OTel attribute keys：HTTP
// -----------------------------------------------------------------------------

const (
	attrHTTPRequestMethod      = "http.request.method"
	attrHTTPResponseStatusCode = "http.response.status_code"
)

// -----------------------------------------------------------------------------
// OTel attribute keys：资源
// -----------------------------------------------------------------------------

const (
	attrResourceProtocol        = "resource.protocol"
	attrResourceDeliveryType    = "resource.delivery_type"
	attrResourceRenderBlocking  = "resource.render_blocking_status"
	attrResourceCacheHit        = "resource.cache.hit"
	attrResourceTransferSize    = "resource.transfer_size"
	attrResourceEncodedBodySize = "resource.encoded_body_size"
	attrResourceDecodedBodySize = "resource.decoded_body_size"
	attrResourceSize            = "resource.size"
	attrResourceDNSDuration     = "resource.dns.duration"
	attrResourceConnectDuration = "resource.connect.duration"
	attrResourceFirstByte       = "resource.first_byte.duration"
	attrResourceDownload        = "resource.download.duration"
	attrResourceSSLDuration     = "resource.ssl.duration"
)

// 资源子类型输出值（写入 resource.type）。
const (
	resourceTypeLink   = "link"
	resourceTypeScript = "script"
	resourceTypeImage  = "img"
	resourceTypeXHR    = "xhr"
	resourceTypeFetch  = "fetch"
)

// -----------------------------------------------------------------------------
// OTel attribute keys：错误与异常
// -----------------------------------------------------------------------------

const (
	attrErrorSource         = "error.source"
	attrErrorMessage        = "error.message"
	attrCodeFilepath        = "code.filepath"
	attrCodeLineno          = "code.lineno"
	attrCodeColumn          = "code.column"
	attrExceptionType       = "exception.type"
	attrExceptionMessage    = "exception.message"
	attrExceptionStacktrace = "exception.stacktrace"
)

// -----------------------------------------------------------------------------
// OTel attribute keys：视图与会话
// -----------------------------------------------------------------------------

const (
	attrSessionHasReplay     = "session.has_replay"
	attrUserID               = "user.id"
	attrDeviceID             = "device.id"
	attrActionID             = "action.id"
	attrViewID               = "view.id"
	attrViewName             = "view.name"
	attrViewLoadingType      = "view.loading_type"
	attrViewURL              = "view.url"
	attrViewURLTemplate      = "view.url_template"
	attrViewPhase            = "view.phase"
	attrViewReferrer         = "view.referrer"
	attrViewPrevious         = "view.previous"
	attrViewPreviousURLTpl   = "view.previous_url_template"
	attrSessionPhase         = "session.phase"
	attrViewFirstByte        = "view.first_byte"
	attrViewDOMInteractive   = "view.dom_interactive"
	attrViewDOMContentLoaded = "view.dom_content_loaded"
	attrViewDOMComplete      = "view.dom_complete"
	attrViewLoadEvent        = "view.load_event"
)

// -----------------------------------------------------------------------------
// OTel attribute keys：网络与浏览器
// -----------------------------------------------------------------------------

const (
	attrNetworkProtocolName   = "network.protocol.name"
	attrNetworkConnectionType = "network.connection.type"
	attrNetworkEffectiveType  = "network.effective_type"
	attrNetworkStatus         = "network.status"
	attrBrowserViewportWidth  = "browser.viewport.width"
	attrBrowserViewportHeight = "browser.viewport.height"
	attrScreenWidth           = "browser.screen.width"
	attrScreenHeight          = "browser.screen.height"
)

// -----------------------------------------------------------------------------
// OTel attribute keys：WebSocket
// -----------------------------------------------------------------------------

const (
	attrWebsocketPhase  = "websocket.phase"
	attrWebsocketConnID = "websocket.connection.id"
)

// -----------------------------------------------------------------------------
// OTel attribute keys：Action 与 Custom
// -----------------------------------------------------------------------------

const (
	attrActionType       = "action.type"
	attrActionTargetName = "action.target.name"
)

// -----------------------------------------------------------------------------
// OTel attribute keys：Vital
// -----------------------------------------------------------------------------

const (
	attrVitalMetric = "vital.metric"
	attrVitalValue  = "vital.value"
	attrVitalID     = "vital.id"
)

// -----------------------------------------------------------------------------
// OTel attribute keys：Span Link
// -----------------------------------------------------------------------------

const (
	attrLinkFormat        = "format"
	attrLinkTraceparent   = "traceparent"
	linkFormatTraceparent = "traceparent"
)

// -----------------------------------------------------------------------------
// Outcome 值（写入 outcome.type）
// -----------------------------------------------------------------------------

const (
	outcomeSuccess = "success"
	outcomeError   = "error"
	outcomeTimeout = "timeout"
	outcomeAbort   = "abort"
)

// -----------------------------------------------------------------------------
// 事件类型（d2[].fields.type 与消息别名）
// -----------------------------------------------------------------------------

const (
	eventTypeWebVitals       = "web_vitals"
	eventTypeAssetsSpeed     = "assets_speed"
	eventTypeAPI             = "api"
	eventTypeCustomEvent     = "custom_event"
	eventTypePagePerformance = "page_performance"
	eventTypePV              = "pv"
	eventTypeWebsocket       = "websocket"
	eventTypeSession         = "session"
	eventTypeError           = "error"
	eventTypeNormal          = "normal"
	eventTypeSPA             = "spa"
	// assetSpeed 旧消息别名，规范化后映射到 assets_speed。
	eventTypeAssetSpeed = "asset_speed"
)

// -----------------------------------------------------------------------------
// 插件标识（d2[].fields.plugin）
// -----------------------------------------------------------------------------

const (
	pluginSPA       = "spa"
	pluginWebsocket = "websocket"
	pluginError     = "error"
)

// -----------------------------------------------------------------------------
// 错误来源（fields.level 与推导的 error.source）
// -----------------------------------------------------------------------------

const (
	sourceUnhandledRejection = "unhandledrejection"
	sourceResource           = "resource"
	sourceWindowError        = "window.error"

	levelError              = "error"
	levelFatal              = "fatal"
	levelCritical           = "critical"
	levelPanic              = "panic"
	levelException          = "exception"
	levelPromiseError       = "promise_error"
	levelUnhandledRejection = "unhandledrejection"
	levelResourceError      = "resource_error"
	levelAssetError         = "asset_error"
)

// 决定 Resource outcome 的错误名。
const (
	errorNameAbort   = "AbortError"
	errorNameTimeout = "TimeoutError"
)

// exception.type 缺省值。
const (
	exceptionTypeUnhandledRejection = "UnhandledRejection"
	exceptionTypeResource           = "ResourceError"
	exceptionTypeDefault            = "Error"
)

// -----------------------------------------------------------------------------
// 视图阶段与加载类型
// -----------------------------------------------------------------------------

const (
	viewLoadingTypeInitial = "initial_load"
)

// -----------------------------------------------------------------------------
// 会话阶段与会话来源
// -----------------------------------------------------------------------------

const (
	sessionPhaseStart        = "start"
	sessionPhaseRotate       = "rotate"
	sessionPhaseEnd          = "end"
	sessionFromLocalGenerate = "local_generate"
)

// -----------------------------------------------------------------------------
// WebSocket 阶段
// -----------------------------------------------------------------------------

const (
	wsPhaseConnect = "connect"
	wsPhaseOpen    = "open"
	wsPhaseError   = "error"
	wsPhaseClose   = "close"
)

// -----------------------------------------------------------------------------
// Action 与 Custom 命名
// -----------------------------------------------------------------------------

const (
	actionTypeCustom  = "custom"
	actionNameDefault = "action.custom"
	actionNamePrefix  = "action."

	customNameDefault = "event"
	customNamePrefix  = "custom."
)

// -----------------------------------------------------------------------------
// Span Link：W3C traceparent 格式
// -----------------------------------------------------------------------------

const (
	traceparentVersion    = "00"
	traceparentTraceIDLen = 32
	traceparentSpanIDLen  = 16
	traceparentFlagsLen   = 2
)

// -----------------------------------------------------------------------------
// HTTP 状态码与判定阈值
// -----------------------------------------------------------------------------

const (
	statusRequestTimeout = 408
	statusGatewayTimeout = 504
	statusErrorThreshold = 400
)

// -----------------------------------------------------------------------------
// 网络类型（bean.netType 归一化后值）
// -----------------------------------------------------------------------------

const (
	netWired        = "wired"
	netBluetooth    = "bluetooth"
	netCellular     = "cellular"
	netEthernet     = "ethernet"
	netMixed        = "mixed"
	netNone         = "none"
	netOther        = "other"
	netUnknown      = "unknown"
	netWifi         = "wifi"
	netWimax        = "wimax"
	netNotConnected = "not_connected"
)

// 蜂窝代次。
const (
	netGen2G     = "2g"
	netGen3G     = "3g"
	netGen4G     = "4g"
	netGen5G     = "5g"
	netGen6G     = "6g"
	netGenSlow2G = "slow-2g"
)

// 平台标识（bean.platform 可接受值）。
const (
	platformMacOS   = "macOS"
	platformWindows = "Windows"
	platformLinux   = "Linux"
	platformAndroid = "Android"
	platformIOS     = "iOS"
)
