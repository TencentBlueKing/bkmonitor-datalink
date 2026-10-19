// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegis

import (
	"encoding/hex"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/rum"
)

// appendResource 定义 Resource 的名称、Kind、耗时、属性、结果和 Trace Link。
func (c *spanConverter) appendResource(e aegisEvent) {
	span := c.newEventSpan(e, rum.Resource, "")
	span.SetEndTimestamp(resourceEndTimestamp(span.StartTimestamp(), e.msg))
	attrs, raw := span.Attributes(), e.msg.raw
	typ := assetSubtype(firstNonEmptyString(raw, "initiatorType"), firstNonEmptyString(raw, "assetType"))
	if e.record.Fields.Type == eventTypeAPI {
		typ = strings.ToLower(firstNonEmptyString(raw, "requestType"))
		if typ == "xmlhttprequest" {
			typ = resourceTypeXHR
		}
	}
	attrs.PutString(rum.ResourceTypeKey, typ)
	putTargetURLAttrs(attrs, firstNonEmptyString(raw, "url"))
	attrs.PutString(attrHTTPRequestMethod, strings.ToUpper(firstNonEmptyString(raw, "method")))
	status, hasStatus := extractInt64(raw, "status")
	if hasStatus && status >= 0 {
		attrs.PutInt(attrHTTPResponseStatusCode, status)
	}
	for _, field := range []struct{ source, target string }{
		{"transferSize", attrResourceTransferSize},
		{"encodedBodySize", attrResourceEncodedBodySize},
		{"decodedBodySize", attrResourceDecodedBodySize},
	} {
		if value, ok := extractInt64(raw, field.source); ok && value >= 0 {
			attrs.PutInt(field.target, value)
		}
	}
	decoded, hasDecoded := extractInt64(raw, "decodedBodySize")
	if hasDecoded && decoded >= 0 {
		attrs.PutInt(attrResourceSize, decoded)
	}
	transfer, hasTransfer := extractInt64(raw, "transferSize")
	delivery := firstNonEmptyString(raw, "deliveryType")
	if delivery == "cache" || (hasTransfer && transfer == 0 && hasDecoded && decoded > 0) {
		attrs.PutBool(attrResourceCacheHit, true)
	}
	attrs.PutString(attrResourceProtocol, firstNonEmptyString(raw, "nextHopProtocol"))
	attrs.PutString(attrResourceDeliveryType, delivery)
	attrs.PutString(attrResourceRenderBlocking, firstNonEmptyString(raw, "renderBlockingStatus"))
	// Aegis 只提供这些阶段耗时，无法可靠还原 W3C 各阶段的相对起点。
	for _, field := range []struct{ source, target string }{
		{"domainLookup", attrResourceDNSDuration},
		{"connectTime", attrResourceConnectDuration},
		{"requestTime", attrResourceFirstByte},
		{"responseTime", attrResourceDownload},
	} {
		putNonNegativeNumber(attrs, field.target, raw, field.source)
	}
	// Aegis 在连接复用或 HTTP 场景可能产生无效 tlsTime；TLS 必须包含在建连耗时内。
	tls, hasTLS := extractFloat64(raw, "tlsTime")
	connect, hasConnect := extractFloat64(raw, "connectTime")
	isHTTPS, hasHTTPS := extractBool(raw, "isHttps")
	scheme, _ := attrs.Get(attrURLScheme)
	if hasTLS && tls >= 0 && hasConnect && connect >= tls && scheme.Str() != urlSchemeHTTP && (!hasHTTPS || isHTTPS) {
		attrs.PutDouble(attrResourceSSLDuration, tls)
	}

	outcome := outcomeSuccess
	switch name := firstNonEmptyString(raw, "errorName", "errorType"); {
	case name == errorNameAbort:
		outcome = outcomeAbort
	case name == errorNameTimeout || status == statusRequestTimeout || status == statusGatewayTimeout:
		outcome = outcomeTimeout
	case e.isError() || status >= statusErrorThreshold:
		outcome = outcomeError
	}
	if outcome == outcomeError || outcome == outcomeTimeout {
		span.Status().SetCode(ptrace.StatusCodeError)
		// HTTP 状态失败本身不是异常；有实际错误消息时才记录 Event。
		if firstNonEmptyString(raw, "errorMsg", "errorName", "errorType", "stack", "stacktrace") != "" || (e.isError() && status < statusErrorThreshold) {
			putSpanException(span, e)
		}
	}
	attrs.PutString(rum.OutcomeTypeKey, outcome)
	if e.record.Fields.Type == eventTypeAPI && (typ == resourceTypeFetch || typ == resourceTypeXHR) {
		appendRequestTraceLink(span, raw)
	}
}

func resourceEndTimestamp(start pcommon.Timestamp, msg d2Message) pcommon.Timestamp {
	// 防止异常的毫秒值在转换为纳秒时溢出。
	maxDuration := float64(math.MaxInt64-int64(start)) / float64(time.Millisecond)
	if duration, ok := extractFloat64(msg.raw, "duration"); ok && duration > 0 && duration < maxDuration {
		return pcommon.NewTimestampFromTime(start.AsTime().Add(time.Duration(duration * float64(time.Millisecond))))
	}
	return start
}

func assetSubtype(initiatorType, assetType string) string {
	if initiatorType != "" {
		return strings.ToLower(initiatorType)
	}
	switch strings.ToLower(assetType) {
	case "css":
		return resourceTypeLink
	case "js", "script":
		return resourceTypeScript
	case "img", "image":
		return resourceTypeImage
	default:
		return strings.ToLower(assetType)
	}
}

func putTargetURLAttrs(attrs pcommon.Map, rawURL string) {
	attrs.PutString(attrURLFull, rawURL)
	attrs.PutString(attrURLTemplate, urlTemplate(rawURL))
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return
	}
	attrs.PutString(attrURLScheme, parsed.Scheme)
	attrs.PutString(attrServerAddress, parsed.Hostname())
	if port, err := strconv.ParseInt(parsed.Port(), 10, 64); err == nil {
		attrs.PutInt(attrServerPort, port)
	}
}

// 只在存在合法 W3C traceparent 时建立后端关联；referrer 不是 Trace Link。
func appendRequestTraceLink(span ptrace.Span, raw map[string]any) {
	value := firstNonEmptyString(raw, "traceparent")
	if value == "" {
		return
	}
	parts := strings.Split(value, "-")
	if len(parts) != 4 || parts[0] != traceparentVersion || len(parts[1]) != traceparentTraceIDLen || len(parts[2]) != traceparentSpanIDLen || len(parts[3]) != traceparentFlagsLen {
		return
	}
	traceBytes, traceErr := hex.DecodeString(parts[1])
	spanBytes, spanErr := hex.DecodeString(parts[2])
	_, flagsErr := hex.DecodeString(parts[3])
	if traceErr != nil || spanErr != nil || flagsErr != nil {
		return
	}
	var traceID [16]byte
	var spanID [8]byte
	copy(traceID[:], traceBytes)
	copy(spanID[:], spanBytes)
	if traceID == ([16]byte{}) || spanID == ([8]byte{}) {
		return
	}
	link := span.Links().AppendEmpty()
	link.SetTraceID(traceID)
	link.SetSpanID(spanID)
	link.Attributes().PutString(attrLinkFormat, linkFormatTraceparent)
	link.Attributes().PutString(attrLinkTraceparent, value)
}

func putNonNegativeNumber(attrs pcommon.Map, target string, raw map[string]any, source string) {
	if value, ok := extractFloat64(raw, source); ok && value >= 0 {
		attrs.PutDouble(target, value)
	}
}
