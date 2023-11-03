// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkcollector

import (
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/elastic/beats/libbeat/logp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	otelTrace "go.opentelemetry.io/otel/trace"
)

type SpanStubs []TraceData

type TraceData struct {
	Name         string                 `json:"span_name"`
	EndTime      int64                  `json:"end_time"`
	StartTime    int64                  `json:"start_time"`
	ParentSpanId string                 `json:"parent_span_id"`
	SpanId       string                 `json:"span_id"`
	TraceId      string                 `json:"trace_id"`
	Kind         int                    `json:"kind"`
	Attributes   map[string]interface{} `json:"attributes"`
	Events       []Event                `json:"events"`
	Links        []Link                 `json:"links"`
	TraceState   string                 `json:"trace_state"`
	Status       Status                 `json:"status"`
	Resource     map[string]interface{} `json:"resource"`

	droppedAttributes    int
	droppedEvents        int
	droppedLinks         int
	childSpanCount       int
	instrumentationScope instrumentation.Scope
}

type Status struct {
	Code    uint32 `json:"code"`
	Message string `json:"message"`
}

type Link struct {
	Attributes map[string]interface{} `json:"attributes"`
	TraceID    string                 `json:"trace_id"`
	SpanID     string                 `json:"span_id"`
	TraceState string                 `json:"trace_state"`
}

type Event struct {
	Name       string                 `json:"name"`
	Attributes map[string]interface{} `json:"attributes"`
	TimeStamp  int64                  `json:"timestamp"`
}

func (s SpanStubs) Snapshots() []tracesdk.ReadOnlySpan {
	if len(s) == 0 {
		return nil
	}
	ro := make([]tracesdk.ReadOnlySpan, len(s))
	for i := 0; i < len(s); i++ {
		ro[i] = s[i].Snapshot()
	}
	return ro
}

type spanSnapshot struct {
	tracesdk.ReadOnlySpan

	name                 string
	spanContext          otelTrace.SpanContext
	parent               otelTrace.SpanContext
	spanKind             otelTrace.SpanKind
	startTime            time.Time
	endTime              time.Time
	attributes           []attribute.KeyValue
	events               []tracesdk.Event
	links                []tracesdk.Link
	status               tracesdk.Status
	resource             *resource.Resource
	droppedAttributes    int
	droppedEvents        int
	droppedLinks         int
	childSpanCount       int
	instrumentationScope instrumentation.Scope
}

func (s spanSnapshot) InstrumentationLibrary() instrumentation.Library {
	return s.instrumentationScope
}

func (t *TraceData) Snapshot() tracesdk.ReadOnlySpan {
	return spanSnapshot{
		name:                 t.Name,
		spanContext:          newSpanCpanContext(t),
		parent:               newParent(t),
		startTime:            getTime(t.StartTime),
		endTime:              getTime(t.EndTime),
		spanKind:             otelTrace.SpanKind(t.Kind),
		attributes:           getAttributes(t.Attributes),
		resource:             getResource(t),
		events:               getEvents(t),
		links:                getLinks(t),
		status:               getStatus(t),
		droppedAttributes:    t.droppedAttributes,
		droppedEvents:        t.droppedEvents,
		droppedLinks:         t.droppedLinks,
		childSpanCount:       t.childSpanCount,
		instrumentationScope: t.instrumentationScope,
	}
}

func getTraceId(t *TraceData) [16]byte {
	traceId, err := hex.DecodeString(t.TraceId)
	if err != nil {
		logp.Err("trace_id cannot be converted to hexadecimal data. error:%v,  trace_id:%v", err, t.TraceId)
	}
	var byteTraceId [16]byte
	copy(byteTraceId[:], traceId)
	return byteTraceId
}

func getTime(timestamp int64) time.Time {
	return time.Unix(0, timestamp)
}

func getSpanId(t *TraceData) [8]byte {
	spanId, err := hex.DecodeString(t.SpanId)
	if err != nil {
		logp.Err("span_id cannot be converted to hexadecimal data. error:%v span_id:%v", err, t.SpanId)
	}
	var byteSpanId [8]byte
	copy(byteSpanId[:], spanId)
	return byteSpanId
}

func getParentId(t *TraceData) [8]byte {
	parentSpanId, err := hex.DecodeString(t.ParentSpanId)
	if err != nil {
		logp.Err("Parent_span_id cannot be converted to hexadecimal data. error:%V Parent_span_id:%v", err, t.ParentSpanId)
	}
	var byteParentSpanId [8]byte
	copy(byteParentSpanId[:], parentSpanId)
	return byteParentSpanId
}

func getKeyValue(attributes map[string]interface{}) []attribute.KeyValue {
	var result = make([]attribute.KeyValue, 0, len(attributes))
	for key, value := range attributes {
		v, ok := value.(string)
		if !ok {
			v = convertToString(value)
		}
		attr := attribute.KeyValue{
			Key:   attribute.Key(key),
			Value: attribute.StringValue(v),
		}
		result = append(result, attr)
	}
	return result
}

func getEvents(t *TraceData) []tracesdk.Event {
	events := t.Events
	var result = make([]tracesdk.Event, 0, len(events))
	for _, event := range events {
		timeStamp := getTime(event.TimeStamp)
		attributes := getAttributes(event.Attributes)
		traceEvent := tracesdk.Event{
			Name:       event.Name,
			Time:       timeStamp,
			Attributes: attributes,
		}
		result = append(result, traceEvent)
	}
	return result
}

func getLinks(t *TraceData) []tracesdk.Link {
	NewTraceData := &TraceData{}
	var result = make([]tracesdk.Link, 0)
	for _, link := range t.Links {
		NewTraceData.TraceId = link.TraceID
		NewTraceData.SpanId = link.SpanID
		NewTraceData.TraceState = link.TraceState
		spanContext := newSpanCpanContext(NewTraceData)
		attributes := getAttributes(link.Attributes)
		traceLink := tracesdk.Link{
			SpanContext: spanContext,
			Attributes:  attributes,
		}
		result = append(result, traceLink)
	}
	return result
}

func newSpanCpanContext(t *TraceData) otelTrace.SpanContext {
	traceSate, err := otelTrace.ParseTraceState(t.TraceState)
	if err != nil {
		logp.Err("get traceState err: %v", err)
	}
	spanContextConfig := otelTrace.SpanContextConfig{
		TraceID:    getTraceId(t),
		SpanID:     getSpanId(t),
		TraceState: traceSate,
	}
	return otelTrace.NewSpanContext(spanContextConfig)
}

func newParent(t *TraceData) otelTrace.SpanContext {
	spanContextConfig := otelTrace.SpanContextConfig{
		TraceID: getTraceId(t),
		SpanID:  getParentId(t),
	}
	spanContext := otelTrace.NewSpanContext(spanContextConfig)
	return spanContext
}

func convertToString(value interface{}) string {

	jsonStr, err := json.Marshal(value)
	if err != nil {
		logp.Err("Data cannot be converted to string. data:%v", value)
		return ""
	}

	return string(jsonStr)
}

func getStatus(t *TraceData) tracesdk.Status {
	traceStatus := tracesdk.Status{
		Code:        codes.Code(t.Status.Code),
		Description: t.Status.Message,
	}
	return traceStatus
}

func getAttributes(attributes map[string]interface{}) []attribute.KeyValue {
	return getKeyValue(attributes)
}

func getResource(t *TraceData) *resource.Resource {
	traceResource := getKeyValue(t.Resource)
	newResource := resource.NewSchemaless(traceResource...)
	return newResource
}

func (s spanSnapshot) Name() string { return s.name }

func (s spanSnapshot) SpanContext() otelTrace.SpanContext { return s.spanContext }

func (s spanSnapshot) Parent() otelTrace.SpanContext { return s.parent }

func (s spanSnapshot) SpanKind() otelTrace.SpanKind { return s.spanKind }

func (s spanSnapshot) StartTime() time.Time { return s.startTime }

func (s spanSnapshot) EndTime() time.Time { return s.endTime }

func (s spanSnapshot) Attributes() []attribute.KeyValue { return s.attributes }

func (s spanSnapshot) Links() []tracesdk.Link { return s.links }

func (s spanSnapshot) Events() []tracesdk.Event { return s.events }

func (s spanSnapshot) Status() tracesdk.Status { return s.status }

func (s spanSnapshot) DroppedAttributes() int { return s.droppedAttributes }

func (s spanSnapshot) DroppedLinks() int { return s.droppedLinks }

func (s spanSnapshot) DroppedEvents() int { return s.droppedEvents }

func (s spanSnapshot) ChildSpanCount() int { return s.childSpanCount }

func (s spanSnapshot) Resource() *resource.Resource { return s.resource }

func (s spanSnapshot) InstrumentationScope() instrumentation.Scope {
	return s.instrumentationScope
}
