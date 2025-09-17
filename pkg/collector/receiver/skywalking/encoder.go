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
	"net"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	semconv "go.opentelemetry.io/collector/semconv/v1.8.0"
	commonv3 "skywalking.apache.org/repo/goapi/collect/common/v3"
	agentv3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/foreach"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
)

const (
	attributeRefType                  = "refType"
	attributeParentInstance           = "parent.service.instance"
	attributeParentEndpoint           = "parent.endpoint"
	attributeNetworkAddressUsedAtPeer = "network.AddressUsedAtPeer"
	attributeDataToken                = "bk.data.token"

	attributeSkywalkingSpanID          = "sw8.span_id"
	attributeSkywalkingTraceID         = "sw8.trace_id"
	attributeSkywalkingSegmentID       = "sw8.segment_id"
	attributeSkywalkingParentSpanID    = "sw8.parent_span_id"
	attributeSkywalkingParentSegmentID = "sw8.parent_segment_id"
	attributeSkywalkingSpanLayer       = "sw8.span_layer"
	attributeSkywalkingComponentID     = "sw8.component_id"
)

var rewriteIps = map[string]struct{}{
	"localhost": {},
	"127.0.0.1": {},
}

var otSpanTagsMapping = map[string]string{
	// HTTP
	"url":         semconv.AttributeHTTPURL,
	"status_code": semconv.AttributeHTTPStatusCode,

	// DB
	"db.instance": semconv.AttributeDBName,
	"db.type":     semconv.AttributeDBSystem,

	// cache
	"cache.type": semconv.AttributeDBSystem,
	"cache.cmd":  semconv.AttributeDBOperation,

	// MQ
	"mq.broker": semconv.AttributeNetPeerName,
	"mq.topic":  semconv.AttributeMessagingDestinationKindTopic,
}

var otSpanEventsMapping = map[string]string{
	"error.kind": semconv.AttributeExceptionType,
	"message":    semconv.AttributeExceptionMessage,
	"stack":      semconv.AttributeExceptionStacktrace,
}

func EncodeTraces(segment *agentv3.SegmentObject, token string, extraAttrs map[string]string) ptrace.Traces {
	pdTraces := ptrace.NewTraces()

	swSpans := segment.Spans
	if swSpans == nil && len(swSpans) == 0 {
		return pdTraces
	}

	resourceSpan := pdTraces.ResourceSpans().AppendEmpty()
	rs := resourceSpan.Resource().Attributes()
	rs.InsertString(semconv.AttributeServiceName, segment.GetService())
	rs.InsertString(semconv.AttributeServiceInstanceID, segment.GetServiceInstance())
	rs.InsertString(attributeSkywalkingTraceID, segment.GetTraceId())
	rs.InsertString(attributeDataToken, token)

	// 补充数据字段内容 agentLanguage agentType agentVersion
	for k, v := range extraAttrs {
		rs.InsertString(k, v)
	}

	il := resourceSpan.ScopeSpans().AppendEmpty()
	swSpansToSpanSlice(segment.GetTraceId(), segment.GetTraceSegmentId(), swSpans, il.Spans())

	serviceInstanceId := segment.GetServiceInstance()
	foreach.Spans(pdTraces, func(span ptrace.Span) {
		attrs := span.Attributes()
		v, ok := attrs.Get(semconv.AttributeNetHostIP)
		if !ok {
			swTransformIP(serviceInstanceId, attrs)
		} else {
			ip := strings.ToLower(v.AsString())
			if _, ok = rewriteIps[ip]; ok {
				swTransformIP(serviceInstanceId, attrs)
			}
		}
	})

	return pdTraces
}

// swTransformIP 转化 serviceInstanceId 中的 ip 字段插入 attribute 中
// serviceInstanceId 样例 70d59292f06b4245b50654f7bba0604e@127.0.0.1
func swTransformIP(instanceId string, attrs pcommon.Map) {
	s := strings.Split(instanceId, "@")
	// 如果裁剪出则插入字段，否则不做任何操作
	if len(s) >= 2 {
		attrs.UpsertString(semconv.AttributeNetHostIP, s[1])
	}
}

func swSpansToSpanSlice(traceID string, segmentID string, spans []*agentv3.SpanObject, dest ptrace.SpanSlice) {
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

func swSpanToSpan(traceID string, segmentID string, span *agentv3.SpanObject, dest ptrace.Span) {
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

	attrs.UpsertString(attributeSkywalkingSpanLayer, span.SpanLayer.String())
	attrs.UpsertInt(attributeSkywalkingComponentID, int64(span.GetComponentId()))

	// drop the attributes slice if all of them were replaced during translation
	if attrs.Len() == 0 {
		attrs.Clear()
	}

	attrs.InsertString(attributeSkywalkingSegmentID, segmentID)
	setSwSpanIDToAttributes(span, attrs)
	setInternalSpanStatus(span, dest.Status())

	switch {
	case span.SpanLayer == agentv3.SpanLayer_MQ:
		if span.SpanType == agentv3.SpanType_Entry {
			dest.SetKind(ptrace.SpanKindConsumer)
		} else if span.SpanType == agentv3.SpanType_Exit {
			dest.SetKind(ptrace.SpanKindProducer)
		}
	case span.GetSpanType() == agentv3.SpanType_Exit:
		dest.SetKind(ptrace.SpanKindClient)
	case span.GetSpanType() == agentv3.SpanType_Entry:
		dest.SetKind(ptrace.SpanKindServer)
	case span.GetSpanType() == agentv3.SpanType_Local:
		dest.SetKind(ptrace.SpanKindInternal)
	default:
		dest.SetKind(ptrace.SpanKindUnspecified)
	}

	swLogsToSpanEvents(span.GetLogs(), dest.Events())
	// skywalking: In the across thread and across processes, these references target the parent segments.
	swReferencesToSpanLinks(span.Refs, dest.Links())
}

const unknownVal = "unknown"

// swTagsToAttributesByRule 对于 attributes 中的特定属性进行兜底策略判断
func swTagsToAttributesByRule(dest pcommon.Map, span *agentv3.SpanObject) {
	peerName := span.Peer
	switch span.SpanLayer {
	case agentv3.SpanLayer_Http:
		spanType := span.GetSpanType()
		// attributes.http.route: spanOperationName 样例
		// - java: GET:/api/leader/list/
		// - python: /api/leader/list/
		if spanType == agentv3.SpanType_Entry {
			if _, ok := dest.Get(semconv.AttributeHTTPRoute); !ok {
				if opName := span.GetOperationName(); opName != "" {
					routes := strings.Split(opName, ":")
					dest.UpsertString(semconv.AttributeHTTPRoute, routes[len(routes)-1])
				}
			}

			// http-server 类型判断步骤如下
			// 1) 如果 peerName 字段存在 则优先使用该值
			// 2) 尝试获取 refs[0]的 parent.service.name 进行插入
			// 3) 使用 unknownVal 作为兜底值
			if refs := span.Refs; len(refs) > 0 && refs[0].ParentService != "" {
				dest.InsertString(semconv.AttributeNetPeerName, refs[0].ParentService)
			} else {
				if peerName != "" {
					dest.InsertString(semconv.AttributeNetPeerName, peerName)
				} else {
					dest.InsertString(semconv.AttributeNetPeerName, unknownVal)
				}
			}
		}

		// http-client 的情况下
		if spanType == agentv3.SpanType_Exit {
			if peerName != "" {
				dest.InsertString(semconv.AttributeNetPeerName, peerName)
			} else {
				dest.InsertString(semconv.AttributeNetPeerName, unknownVal)
			}
		}

		// 获取 urlObj
		var urlObj *url.URL
		if u, ok := dest.Get(semconv.AttributeHTTPURL); ok && u.StringVal() != "" {
			if v, err := url.Parse(u.StringVal()); err == nil {
				urlObj = v
			}
		}

		if urlObj != nil {
			// attributes.http.scheme
			httpScheme, ok := dest.Get(semconv.AttributeHTTPScheme)
			if !ok {
				dest.InsertString(semconv.AttributeHTTPScheme, utils.FirstUpper(urlObj.Scheme, unknownVal))
			} else {
				dest.UpsertString(semconv.AttributeHTTPScheme, utils.FirstUpper(httpScheme.StringVal(), unknownVal))
			}
			// attribute.http.target / attribute.http.host
			if spanType == agentv3.SpanType_Exit {
				if _, ok := dest.Get(semconv.AttributeHTTPTarget); !ok {
					dest.InsertString(semconv.AttributeHTTPTarget, urlObj.Path)
				}
				if _, ok := dest.Get(semconv.AttributeHTTPHost); !ok {
					dest.InsertString(semconv.AttributeHTTPHost, urlObj.Host)
				}
			}
		}

	case agentv3.SpanLayer_RPCFramework:
		// attributes.rpc.method
		if _, ok := dest.Get(semconv.AttributeRPCMethod); !ok {
			if rpcMethod := span.GetOperationName(); rpcMethod != "" {
				dest.UpsertString(semconv.AttributeRPCMethod, rpcMethod)
			}
		}

	case agentv3.SpanLayer_Database, agentv3.SpanLayer_Cache:
		// attributes.db.system: spanOperationName 样例 Mysql/MysqlClient/execute
		if span.SpanLayer == agentv3.SpanLayer_Database {
			if opName := span.GetOperationName(); opName != "" {
				dbs := strings.Split(opName, "/")
				dest.UpsertString(semconv.AttributeDBSystem, utils.FirstUpper(dbs[0], unknownVal))
			}
		} else {
			// 对于 Cache 类型尝试进行首字母大写转化工作
			if v, ok := dest.Get(semconv.AttributeDBSystem); ok {
				dest.UpsertString(semconv.AttributeDBSystem, utils.FirstUpper(v.StringVal(), unknownVal))
			}
		}
		// attributes.db.operation
		if _, ok := dest.Get(semconv.AttributeDBOperation); !ok {
			if v, ok := dest.Get(semconv.AttributeDBStatement); ok && v.StringVal() != "" {
				ops := strings.Split(v.StringVal(), " ")
				dest.InsertString(semconv.AttributeDBOperation, ops[0])
			} else {
				// spanOperationName 样例 Mysql/JDBI/Connection/commit
				if opName := span.GetOperationName(); opName != "" {
					ops := strings.Split(opName, "/")
					dest.InsertString(semconv.AttributeDBOperation, ops[len(ops)-1])
				}
			}
		}

		if peerName != "" {
			dest.InsertString(semconv.AttributeNetPeerName, peerName)
		} else {
			dest.InsertString(semconv.AttributeNetPeerName, unknownVal)
		}

	case agentv3.SpanLayer_MQ:
		// attributes.messaging.system
		if opName := span.GetOperationName(); opName != "" {
			dbs := strings.Split(opName, "/")
			dest.UpsertString(semconv.AttributeMessagingSystem, utils.FirstUpper(dbs[0], unknownVal))
		}

		if peerName != "" {
			dest.InsertString(semconv.AttributeNetPeerName, peerName)
		} else {
			dest.InsertString(semconv.AttributeNetPeerName, unknownVal)
		}
	}

	if span.GetSpanType() == agentv3.SpanType_Entry && len(span.Refs) > 0 {
		var hosts []string
		var port int
		addrs := strings.Split(span.Refs[0].NetworkAddressUsedAtPeer, ";")
		for _, addr := range addrs {
			h, p, err := net.SplitHostPort(addr)
			if err != nil {
				continue
			}
			hosts = append(hosts, h)
			if port == 0 {
				if p, err := strconv.Atoi(p); err == nil {
					port = p
				}
			}
		}
		dest.UpsertString(semconv.AttributeNetHostIP, strings.Join(hosts, ","))
		dest.UpsertInt(semconv.AttributeNetHostPort, int64(port))
	}
}

func swReferencesToSpanLinks(refs []*agentv3.SegmentReference, dest ptrace.SpanLinkSlice) {
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
		kvParis := []*commonv3.KeyStringValuePair{
			{
				Key:   attributeParentInstance,
				Value: ref.ParentServiceInstance,
			},
			{
				Key:   attributeParentEndpoint,
				Value: ref.ParentEndpoint,
			},
			{
				Key:   attributeNetworkAddressUsedAtPeer,
				Value: ref.NetworkAddressUsedAtPeer,
			},
			{
				Key:   attributeRefType,
				Value: ref.RefType.String(),
			},
			{
				Key:   attributeSkywalkingTraceID,
				Value: ref.TraceId,
			},
			{
				Key:   attributeSkywalkingParentSegmentID,
				Value: ref.ParentTraceSegmentId,
			},
			{
				Key:   attributeSkywalkingParentSpanID,
				Value: strconv.Itoa(int(ref.ParentSpanId)),
			},
		}
		swKvPairsToInternalAttributes(kvParis, link.Attributes())
	}
}

func setInternalSpanStatus(span *agentv3.SpanObject, dest ptrace.SpanStatus) {
	if span.GetIsError() {
		dest.SetCode(ptrace.StatusCodeError)
		dest.SetMessage("ERROR")
	} else {
		dest.SetCode(ptrace.StatusCodeOk)
		dest.SetMessage("SUCCESS")
	}
}

func setSwSpanIDToAttributes(span *agentv3.SpanObject, dest pcommon.Map) {
	dest.InsertInt(attributeSkywalkingSpanID, int64(span.GetSpanId()))
	if span.ParentSpanId != -1 {
		dest.InsertInt(attributeSkywalkingParentSpanID, int64(span.GetParentSpanId()))
	}
}

func swLogsToSpanEvents(logs []*agentv3.Log, dest ptrace.SpanEventSlice) {
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

func swTagsToEventsAttributes(tags []*commonv3.KeyStringValuePair, dest pcommon.Map) {
	for _, tag := range tags {
		if v, ok := otSpanEventsMapping[tag.Key]; ok {
			dest.UpsertString(v, tag.Value)
		}
	}
}

func swKvPairsToInternalAttributes(pairs []*commonv3.KeyStringValuePair, dest pcommon.Map) {
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
