// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
//
// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//       http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package skywalking

import (
	"bytes"
	"encoding/hex"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	conventions "go.opentelemetry.io/collector/semconv/v1.8.0"
	common "skywalking.apache.org/repo/goapi/collect/common/v3"
	agentV3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
)

const (
	AttributeRefType                   = "refType"
	AttributeParentInstance            = "parent.service.instance"
	AttributeParentEndpoint            = "parent.endpoint"
	AttributeSkywalkingSpanID          = "sw8.span_id"
	AttributeSkywalkingTraceID         = "sw8.trace_id"
	AttributeSkywalkingSegmentID       = "sw8.segment_id"
	AttributeSkywalkingParentSpanID    = "sw8.parent_span_id"
	AttributeSkywalkingParentSegmentID = "sw8.parent_segment_id"
	AttributeNetworkAddressUsedAtPeer  = "network.AddressUsedAtPeer"
	AttributeDataToken                 = "bk.data.token"
	AttributeSkywalkingSpanLayer       = "sw8.span_layer"
	AttributeSkywalkingComponentID     = "sw8.component_id"
)

var ignoreLocalIps = map[string]struct{}{
	"localhost": {},
	"127.0.0.1": {},
}

var otSpanTagsMapping = map[string]string{
	// HTTP
	"url":         conventions.AttributeHTTPURL,
	"status_code": conventions.AttributeHTTPStatusCode,

	// DB
	"db.instance": conventions.AttributeDBName,
	"db.type":     conventions.AttributeDBSystem,

	// MQ
	"mq.broker": conventions.AttributeNetPeerName,
	"mq.topic":  conventions.AttributeMessagingDestinationKindTopic,
}

var otSpanEventsMapping = map[string]string{
	"error.kind": conventions.AttributeExceptionType,
	"message":    conventions.AttributeExceptionMessage,
	"stack":      conventions.AttributeExceptionStacktrace,
}

func EncodeTraces(segment *agentV3.SegmentObject, token string, extraAttrs map[string]string) ptrace.Traces {
	traceData := ptrace.NewTraces()

	swSpans := segment.Spans
	if swSpans == nil && len(swSpans) == 0 {
		return traceData
	}

	resourceSpan := traceData.ResourceSpans().AppendEmpty()
	rs := resourceSpan.Resource()

	rs.Attributes().InsertString(conventions.AttributeServiceName, segment.GetService())
	rs.Attributes().InsertString(conventions.AttributeServiceInstanceID, segment.GetServiceInstance())
	rs.Attributes().InsertString(AttributeSkywalkingTraceID, segment.GetTraceId())
	rs.Attributes().InsertString(AttributeDataToken, token)

	// 补充数据字段内容 agentLanguage agentType agentVersion
	for k, v := range extraAttrs {
		rs.Attributes().InsertString(k, v)
	}

	il := resourceSpan.ScopeSpans().AppendEmpty()
	swSpansToSpanSlice(segment.GetTraceId(), segment.GetTraceSegmentId(), swSpans, il.Spans())

	serviceInstanceId := segment.GetServiceInstance()
	foreach.Spans(traceData.ResourceSpans(), func(span ptrace.Span) {
		attrs := span.Attributes()
		v, ok := attrs.Get(conventions.AttributeNetHostIP)
		if !ok {
			swTransformIP(serviceInstanceId, attrs)
		} else {
			ip := strings.ToLower(v.AsString())
			if _, ok = ignoreLocalIps[ip]; ok {
				swTransformIP(serviceInstanceId, attrs)
			}
		}
	})

	return traceData
}

// swTransformIP 转化 serviceInstanceId 中的 ip 字段插入 attribute 中
// serviceInstanceId 样例 70d59292f06b4245b50654f7bba0604e@10.10.25.163
func swTransformIP(instanceId string, attrs pcommon.Map) {
	s := strings.Split(instanceId, "@")
	// 如果裁剪出则插入字段，否则不做任何操作
	if len(s) >= 2 {
		attrs.UpsertString(conventions.AttributeNetHostIP, s[1])
	}
}

func swSpansToSpanSlice(traceID string, segmentID string, spans []*agentV3.SpanObject, dest ptrace.SpanSlice) {
	if len(spans) == 0 {
		return
	}

	dest.EnsureCapacity(len(spans))
	for _, span := range spans {
		if span == nil {
			continue
		}
		swSpanToSpan(traceID, segmentID, span, dest.AppendEmpty())
	}
}

func swSpanToSpan(traceID string, segmentID string, span *agentV3.SpanObject, dest ptrace.Span) {
	dest.SetTraceID(swTraceIDToTraceID(traceID))
	// skywalking defines segmentId + spanId as unique identifier
	// so use segmentId to convert to an unique otel-span
	dest.SetSpanID(segmentIDToSpanID(segmentID, uint32(span.GetSpanId())))

	// 获取 ref parentSpanID + ref SegmentID
	refParentSpanID := -1
	refSegmentID := ""
	for _, ref := range span.Refs {
		if ref.TraceId == traceID {
			refParentSpanID = int(ref.ParentSpanId)
			refSegmentID = ref.ParentTraceSegmentId
			break
		}
	}
	// 补全特殊情况，当无法直接获取父 span_id 的时候，尝试从 link 里面获取
	if span.ParentSpanId != -1 {
		dest.SetParentSpanID(segmentIDToSpanID(segmentID, uint32(span.GetParentSpanId())))
	} else {
		if refParentSpanID != -1 {
			dest.SetParentSpanID(segmentIDToSpanID(refSegmentID, uint32(refParentSpanID)))
		}
	}

	dest.SetName(span.OperationName)
	dest.SetStartTimestamp(microsecondsToTimestamp(span.GetStartTime()))
	dest.SetEndTimestamp(microsecondsToTimestamp(span.GetEndTime()))

	attrs := dest.Attributes()
	attrs.EnsureCapacity(len(span.Tags))
	swKvPairsToInternalAttributes(span.Tags, attrs)
	swTagsToAttributesByRule(attrs, span)

	attrs.UpsertString(AttributeSkywalkingSpanLayer, span.SpanLayer.String())
	attrs.UpsertInt(AttributeSkywalkingComponentID, int64(span.GetComponentId()))

	// drop the attributes slice if all of them were replaced during translation
	if attrs.Len() == 0 {
		attrs.Clear()
	}

	attrs.InsertString(AttributeSkywalkingSegmentID, segmentID)
	setSwSpanIDToAttributes(span, attrs)
	setInternalSpanStatus(span, dest.Status())

	switch {
	case span.SpanLayer == agentV3.SpanLayer_MQ:
		if span.SpanType == agentV3.SpanType_Entry {
			dest.SetKind(ptrace.SpanKindConsumer)
		} else if span.SpanType == agentV3.SpanType_Exit {
			dest.SetKind(ptrace.SpanKindProducer)
		}
	case span.GetSpanType() == agentV3.SpanType_Exit:
		dest.SetKind(ptrace.SpanKindClient)
	case span.GetSpanType() == agentV3.SpanType_Entry:
		dest.SetKind(ptrace.SpanKindServer)
	case span.GetSpanType() == agentV3.SpanType_Local:
		dest.SetKind(ptrace.SpanKindInternal)
	default:
		dest.SetKind(ptrace.SpanKindUnspecified)
	}

	swLogsToSpanEvents(span.GetLogs(), dest.Events())
	// skywalking: In the across thread and across processes, these references target the parent segments.
	swReferencesToSpanLinks(span.Refs, dest.Links())
}

// swTagsToAttributesByRule 对于 attributes 中的特定属性进行兜底策略判断
func swTagsToAttributesByRule(dest pcommon.Map, span *agentV3.SpanObject) {
	switch span.SpanLayer {
	case agentV3.SpanLayer_Http:
		spanType := span.GetSpanType()
		// attributes.http.route: spanOperationName 样例 java: GET:/api/leader/list/ python: /api/leader/list/
		if spanType == agentV3.SpanType_Entry {
			if _, ok := dest.Get(conventions.AttributeHTTPRoute); !ok {
				if opName := span.GetOperationName(); opName != "" {
					opNameSplit := strings.Split(opName, ":")
					dest.UpsertString(conventions.AttributeHTTPRoute, opNameSplit[len(opNameSplit)-1])
				}
			}
		}

		// 获取 urlObj
		var urlObj *url.URL
		if u, ok := dest.Get(conventions.AttributeHTTPURL); ok {
			if u.StringVal() != "" {
				if v, err := url.Parse(u.StringVal()); err == nil {
					urlObj = v
				}
			}
		}

		if urlObj != nil {
			// attributes.http.scheme
			if _, ok := dest.Get(conventions.AttributeHTTPScheme); !ok {
				dest.InsertString(conventions.AttributeHTTPScheme, urlObj.Scheme)
			}
			// attribute.http.target / attribute.http.host
			if spanType == agentV3.SpanType_Exit {
				if _, ok := dest.Get(conventions.AttributeHTTPTarget); !ok {
					dest.InsertString(conventions.AttributeHTTPTarget, urlObj.Path)
				}
				if _, ok := dest.Get(conventions.AttributeHTTPHost); !ok {
					dest.InsertString(conventions.AttributeHTTPHost, urlObj.Host)
				}
			}
		}

	case agentV3.SpanLayer_RPCFramework:
		// attributes.rpc.method
		if _, ok := dest.Get(conventions.AttributeRPCMethod); !ok {
			if rpcMethod := span.GetOperationName(); rpcMethod != "" {
				dest.UpsertString(conventions.AttributeRPCMethod, rpcMethod)
			}
		}

	case agentV3.SpanLayer_Database, agentV3.SpanLayer_Cache:
		// attributes.db.system: spanOperationName 样例 Mysql/MysqlClient/execute
		if _, ok := dest.Get(conventions.AttributeDBSystem); !ok {
			if opName := span.GetOperationName(); opName != "" {
				opNameSplit := strings.Split(opName, "/")
				dest.InsertString(conventions.AttributeDBSystem, opNameSplit[0])
			}
		}
		// attributes.db.operation
		if _, ok := dest.Get(conventions.AttributeDBOperation); !ok {
			if v, ok := dest.Get(conventions.AttributeDBStatement); ok && v.StringVal() != "" {
				statementSplit := strings.Split(v.StringVal(), " ")
				dest.InsertString(conventions.AttributeDBOperation, statementSplit[0])
			} else {
				// spanOperationName 样例 Mysql/JDBI/Connection/commit
				if opName := span.GetOperationName(); opName != "" {
					opNameSplit := strings.Split(opName, "/")
					dest.InsertString(conventions.AttributeDBOperation, opNameSplit[len(opNameSplit)-1])
				}
			}
		}

	case agentV3.SpanLayer_MQ:
		// attributes.messaging.system
		if _, ok := dest.Get(conventions.AttributeMessagingSystem); !ok {
			if opName := span.GetOperationName(); opName != "" {
				opNameSplit := strings.Split(opName, "/")
				dest.InsertString(conventions.AttributeMessagingSystem, opNameSplit[0])
			}
		}
	}
}

func swReferencesToSpanLinks(refs []*agentV3.SegmentReference, dest ptrace.SpanLinkSlice) {
	if len(refs) == 0 {
		return
	}

	dest.EnsureCapacity(len(refs))

	for _, ref := range refs {
		link := dest.AppendEmpty()
		link.SetTraceID(swTraceIDToTraceID(ref.TraceId))
		link.SetSpanID(segmentIDToSpanID(ref.ParentTraceSegmentId, uint32(ref.ParentSpanId)))
		// link.TraceState().FromRaw("")
		link.SetTraceState("")
		kvParis := []*common.KeyStringValuePair{
			{
				Key:   AttributeParentInstance,
				Value: ref.ParentServiceInstance,
			},
			{
				Key:   AttributeParentEndpoint,
				Value: ref.ParentEndpoint,
			},
			{
				Key:   AttributeNetworkAddressUsedAtPeer,
				Value: ref.NetworkAddressUsedAtPeer,
			},
			{
				Key:   AttributeRefType,
				Value: ref.RefType.String(),
			},
			{
				Key:   AttributeSkywalkingTraceID,
				Value: ref.TraceId,
			},
			{
				Key:   AttributeSkywalkingParentSegmentID,
				Value: ref.ParentTraceSegmentId,
			},
			{
				Key:   AttributeSkywalkingParentSpanID,
				Value: strconv.Itoa(int(ref.ParentSpanId)),
			},
		}
		swKvPairsToInternalAttributes(kvParis, link.Attributes())
	}
}

func setInternalSpanStatus(span *agentV3.SpanObject, dest ptrace.SpanStatus) {
	if span.GetIsError() {
		dest.SetCode(ptrace.StatusCodeError)
		dest.SetMessage("ERROR")
	} else {
		dest.SetCode(ptrace.StatusCodeOk)
		dest.SetMessage("SUCCESS")
	}
}

func setSwSpanIDToAttributes(span *agentV3.SpanObject, dest pcommon.Map) {
	dest.InsertInt(AttributeSkywalkingSpanID, int64(span.GetSpanId()))
	if span.ParentSpanId != -1 {
		dest.InsertInt(AttributeSkywalkingParentSpanID, int64(span.GetParentSpanId()))
	}
}

func swLogsToSpanEvents(logs []*agentV3.Log, dest ptrace.SpanEventSlice) {
	if len(logs) == 0 {
		return
	}
	dest.EnsureCapacity(len(logs))

	for i, log := range logs {
		var event ptrace.SpanEvent
		if dest.Len() > i {
			event = dest.At(i)
		} else {
			event = dest.AppendEmpty()
		}

		event.SetName("logs")
		event.SetTimestamp(microsecondsToTimestamp(log.GetTime()))
		if len(log.GetData()) == 0 {
			continue
		}

		attrs := event.Attributes()
		attrs.Clear()
		attrs.EnsureCapacity(len(log.GetData()))
		swTagsToEventsAttributes(log.GetData(), attrs)
	}
}

func swTagsToEventsAttributes(tags []*common.KeyStringValuePair, dest pcommon.Map) {
	for _, tag := range tags {
		if v, ok := otSpanEventsMapping[tag.Key]; ok {
			dest.UpsertString(v, tag.Value)
		}
	}
}

func swKvPairsToInternalAttributes(pairs []*common.KeyStringValuePair, dest pcommon.Map) {
	if pairs == nil {
		return
	}

	for _, pair := range pairs {
		// OT 数据格式转换的时候将所有数据都插入 Resource 维度
		if v, ok := otSpanTagsMapping[pair.Key]; ok {
			dest.InsertString(v, pair.Value)
		} else {
			dest.InsertString(pair.Key, pair.Value)
		}
	}
}

// microsecondsToTimestamp converts epoch microseconds to pcommon.Timestamp
func microsecondsToTimestamp(ms int64) pcommon.Timestamp {
	return pcommon.NewTimestampFromTime(time.UnixMilli(ms))
}

func swTraceIDToTraceID(traceID string) pcommon.TraceID {
	// skywalking traceid format:
	// de5980b8-fce3-4a37-aab9-b4ac3af7eedd: from browser/js-sdk/envoy/nginx-lua sdk/py-agent
	// 56a5e1c519ae4c76a2b8b11d92cead7f.12.16563474296430001: from java-agent

	if len(traceID) <= 36 { // 36: uuid length (rfc4122)
		uid, err := uuid.Parse(traceID)
		if err != nil {
			return pcommon.InvalidTraceID()
		}
		return pcommon.NewTraceID(uid)
	}
	return pcommon.NewTraceID(swStringToUUID(traceID, 0))
}

func segmentIDToSpanID(segmentID string, spanID uint32) pcommon.SpanID {
	// skywalking segmentid format:
	// 56a5e1c519ae4c76a2b8b11d92cead7f.12.16563474296430001: from TraceSegmentId
	// 56a5e1c519ae4c76a2b8b11d92cead7f: from ParentTraceSegmentId

	if len(segmentID) < 32 {
		return pcommon.InvalidSpanID()
	}
	return pcommon.NewSpanID(uuidTo8Bytes(swStringToUUID(segmentID, spanID)))
}

func swStringToUUID(s string, extra uint32) (dst [16]byte) {
	// there are 2 possible formats for 's':
	// s format = 56a5e1c519ae4c76a2b8b11d92cead7f.0000000000.000000000000000000
	//            ^ start(length=32)               ^ mid(u32) ^ last(u64)
	// uid = UUID(start) XOR ([4]byte(extra) . [4]byte(uint32(mid)) . [8]byte(uint64(last)))

	// s format = 56a5e1c519ae4c76a2b8b11d92cead7f
	//            ^ start(length=32)
	// uid = UUID(start) XOR [4]byte(extra)

	if len(s) < 32 {
		return
	}

	t := unsafeGetBytes(s)
	var uid [16]byte
	_, err := hex.Decode(uid[:], t[:32])
	if err != nil {
		return uid
	}

	for i := 0; i < 4; i++ {
		uid[i] ^= byte(extra)
		extra >>= 8
	}

	if len(s) == 32 {
		return uid
	}

	index1 := bytes.IndexByte(t, '.')
	index2 := bytes.LastIndexByte(t, '.')
	if index1 != 32 || index2 < 0 {
		return
	}

	mid, err := strconv.Atoi(s[index1+1 : index2])
	if err != nil {
		return
	}

	last, err := strconv.Atoi(s[index2+1:])
	if err != nil {
		return
	}

	for i := 4; i < 8; i++ {
		uid[i] ^= byte(mid)
		mid >>= 8
	}

	for i := 8; i < 16; i++ {
		uid[i] ^= byte(last)
		last >>= 8
	}

	return uid
}

func uuidTo8Bytes(uuid [16]byte) [8]byte {
	// high bit XOR low bit
	var dst [8]byte
	for i := 0; i < 8; i++ {
		dst[i] = uuid[i] ^ uuid[i+8]
	}
	return dst
}

func unsafeGetBytes(s string) []byte {
	return (*[0x7fff0000]byte)(unsafe.Pointer(
		(*reflect.StringHeader)(unsafe.Pointer(&s)).Data),
	)[:len(s):len(s)]
}
