package bkcollector

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/beats/libbeat/logp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type SpanStubs []SpanStub

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

// SpanStub is a stand-in for a Span.
type SpanStub struct {
	Name                   string
	SpanContext            trace.SpanContext
	Parent                 trace.SpanContext
	SpanKind               trace.SpanKind
	StartTime              time.Time
	EndTime                time.Time
	Attributes             []attribute.KeyValue
	Events                 []tracesdk.Event
	Links                  []tracesdk.Link
	Status                 tracesdk.Status
	DroppedAttributes      int
	DroppedEvents          int
	DroppedLinks           int
	ChildSpanCount         int
	Resource               *resource.Resource
	InstrumentationLibrary instrumentation.Library
}

func (s SpanStub) Snapshot() tracesdk.ReadOnlySpan {
	return spanSnapshot{
		name:                 s.Name,
		spanContext:          s.SpanContext,
		parent:               s.Parent,
		spanKind:             s.SpanKind,
		startTime:            s.StartTime,
		endTime:              s.EndTime,
		attributes:           s.Attributes,
		events:               s.Events,
		links:                s.Links,
		status:               s.Status,
		droppedAttributes:    s.DroppedAttributes,
		droppedEvents:        s.DroppedEvents,
		droppedLinks:         s.DroppedLinks,
		childSpanCount:       s.ChildSpanCount,
		resource:             s.Resource,
		instrumentationScope: s.InstrumentationLibrary,
	}
}

type spanSnapshot struct {
	tracesdk.ReadOnlySpan

	name                 string
	spanContext          trace.SpanContext
	parent               trace.SpanContext
	spanKind             trace.SpanKind
	startTime            time.Time
	endTime              time.Time
	attributes           []attribute.KeyValue
	events               []tracesdk.Event
	links                []tracesdk.Link
	status               tracesdk.Status
	droppedAttributes    int
	droppedEvents        int
	droppedLinks         int
	childSpanCount       int
	resource             *resource.Resource
	instrumentationScope instrumentation.Scope
}

func (s spanSnapshot) InstrumentationLibrary() instrumentation.Library {
	return s.instrumentationScope
}

func getTime(timestamp float64) time.Time {
	// 将 float64 时间戳转换为 int64 类型
	seconds := int64(timestamp / 1e9)
	nanoseconds := int64(timestamp) % int64(1e9)

	t := time.Unix(seconds, nanoseconds)
	return t
}

func getSpanId(traceData map[string]interface{}) [8]byte {
	spanId, ok := traceData["span_id"].(string)
	if !ok {
		spanId = convertToString(traceData["span_id"])
	}
	var byteSpanId [8]byte
	copy(byteSpanId[:], spanId)
	return byteSpanId
}

func getParentId(traceData map[string]interface{}) [8]byte {
	parentSpanId, ok := traceData["span_id"].(string)
	if !ok {
		parentSpanId = convertToString(traceData["parent_span_id"])
	}
	var byteParentSpanId [8]byte
	copy(byteParentSpanId[:], parentSpanId)
	return byteParentSpanId
}

func getKeyValue(attributes map[string]interface{}) []attribute.KeyValue {
	var result = make([]attribute.KeyValue, 0)
	for key, value := range attributes {
		v, ok := value.(string)
		if !ok {
			v = convertToString(value)
		}
		_value := attribute.StringValue(v)
		attr := attribute.KeyValue{
			Key:   attribute.Key(key),
			Value: _value,
		}
		result = append(result, attr)
	}
	return result
}

func getEvents(traceData map[string]interface{}) []tracesdk.Event {
	var result = make([]tracesdk.Event, 0)
	events, ok := traceData["events"].([]interface{})
	if !ok {
		logp.Err("Cannot be converted into time data,  events:%v", traceData["events"])
		return result
	}
	for _, event := range events {
		eventMap, toEventMap := event.(map[string]interface{})
		if !toEventMap {
			continue
		}
		name := getSpanName(eventMap)
		eventTime := time.Time{}
		timestamp, toTimeStamp := eventMap["timestamp"].(float64)
		if !toTimeStamp {
			logp.Err("Cannot be converted into time data,  events_timestamp:%v", eventMap["timestamp"])
		}
		eventTime = getTime(timestamp)
		attributes := getAttributes(eventMap)
		traceEvent := tracesdk.Event{
			Name:       name,
			Time:       eventTime,
			Attributes: attributes,
		}
		result = append(result, traceEvent)
	}
	return result
}

func getLinks(traceData map[string]interface{}, traceId [16]byte) []tracesdk.Link {
	var result = make([]tracesdk.Link, 0)
	links, ok := traceData["links"].([]interface{})
	if !ok {
		logp.Err("Cannot be converted into time data,  links:%v", traceData["links"])
		return result
	}
	for _, link := range links {
		linkMap, toLinkMap := link.(map[string]interface{})
		if !toLinkMap {
			continue
		}
		spanId := getSpanId(linkMap)
		var linkTraceState string
		traceState, ok := linkMap["trace_state"]
		if ok {
			linkTraceState = traceState.(string)
		}
		spanContext := CreateSpanContext(spanId, traceId, linkTraceState)
		attributes := getAttributes(linkMap)
		traceLink := tracesdk.Link{
			SpanContext: spanContext,
			Attributes:  attributes,
		}
		result = append(result, traceLink)
	}
	return result
}

func CreateSpanContext(spanId [8]byte, traceId [16]byte, traceState string) trace.SpanContext {
	traceSate, err := trace.ParseTraceState(traceState)
	if err != nil {
		logp.Err("get traceState err: %v", err)
	}
	spanContextConfig := trace.SpanContextConfig{
		TraceID:    traceId,
		SpanID:     spanId,
		TraceState: traceSate,
	}
	spanContext := trace.NewSpanContext(spanContextConfig)
	return spanContext
}

func convertToString(value interface{}) string {
	switch v := value.(type) {
	case int:
		return strconv.Itoa(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case []int:
		strSlice := make([]string, len(v))
		for i, num := range v {
			strSlice[i] = strconv.Itoa(num)
		}
		return "[" + strings.Join(strSlice, ", ") + "]"
	case map[string]interface{}:
		jsonString, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(jsonString)
	default:
		return ""
	}
}

func getSpanName(traceData map[string]interface{}) string {
	name, ok := traceData["span_name"].(string)
	if !ok {
		name = convertToString(traceData["span_name"])
	}
	return name
}

func getTraceId(traceData map[string]interface{}) [16]byte {
	traceId, ok := traceData["trace_id"].(string)
	if !ok {
		traceId = convertToString(traceData["trace_id"])
	}
	var byteTraceId [16]byte
	copy(byteTraceId[:], traceId)
	return byteTraceId
}

func getStartTime(traceData map[string]interface{}) time.Time {
	floatStartTime, ok := traceData["start_time"].(float64)
	if !ok {
		logp.Err("Cannot be converted into time data,  start_time:%v", floatStartTime)
		return time.Time{}
	}
	startTime := getTime(floatStartTime)
	return startTime
}

func getEndTime(traceData map[string]interface{}) time.Time {
	floatEndTime, ok := traceData["end_time"].(float64)
	if !ok {
		logp.Err("Cannot be converted into time data,  end_time:%v", floatEndTime)
		return time.Time{}
	}
	endTime := getTime(floatEndTime)
	return endTime
}

func getKind(traceData map[string]interface{}) int {
	kind, ok := traceData["kind"].(int)
	if !ok {
		switch reflect.TypeOf(kind).Kind() {
		case reflect.Float64:
			return int(traceData["kind"].(float64))
		default:
			logp.Err("trace kind Wrong data format, kind:%v", traceData["kind"])
			return 0
		}
	}
	return kind
}

func getTraceState(traceData map[string]interface{}) string {
	traceState, ok := traceData["trace_state"].(string)
	if !ok {
		logp.Err("trace_state Wrong data format, trace_state:%v", traceState)
		return ""
	}
	return traceState
}

func getCode(status map[string]interface{}) uint32 {
	switch reflect.TypeOf(status["code"]).Kind() {
	case reflect.Int:
		return uint32(status["code"].(int))
	case reflect.Float64:
		return uint32(status["code"].(float64))
	default:
		logp.Err("trace_state Wrong data format, code:%v", status["code"])
		return uint32(0)
	}
}

func getStatus(traceData map[string]interface{}) tracesdk.Status {
	status, ok := traceData["status"].(map[string]interface{})
	if !ok {
		logp.Err("trace_state Wrong data format, status:%v", status)
		return tracesdk.Status{}
	}
	code := getCode(status)
	statusMessage := getMessage(status)
	traceStatus := tracesdk.Status{
		Code:        codes.Code(code),
		Description: statusMessage,
	}
	return traceStatus
}

func getMessage(status map[string]interface{}) string {
	statusMessage, ok := status["message"].(string)
	if !ok {
		statusMessage = convertToString(status["message"])
		return statusMessage
	}
	return statusMessage
}

func getAttributes(traceData map[string]interface{}) []attribute.KeyValue {
	attributes, ok := traceData["attributes"].(map[string]interface{})
	if !ok {
		var attributes = make([]attribute.KeyValue, 0)
		return attributes
	}
	return getKeyValue(attributes)
}

func getResource(traceData map[string]interface{}, bkDataToken string) []attribute.KeyValue {
	traceResource, ok := traceData["resource"].(map[string]interface{})
	if !ok {
		resourceMap := make(map[string]interface{})
		resourceMap["bk.data.token"] = bkDataToken
		return getKeyValue(resourceMap)
	}
	traceResource["bk.data.token"] = bkDataToken
	return getKeyValue(traceResource)
}

func PushData(traceData map[string]interface{}, bkDataToken string) SpanStub {
	name := getSpanName(traceData)
	traceId := getTraceId(traceData)
	traceState := getTraceState(traceData)
	byteSpanId := getSpanId(traceData)
	byteParentSpanId := getParentId(traceData)
	startTime := getStartTime(traceData)
	endTime := getEndTime(traceData)
	kind := getKind(traceData)
	status := getStatus(traceData)
	attributes := getAttributes(traceData)
	tracedResource := getResource(traceData, bkDataToken)
	newResource := resource.NewSchemaless(tracedResource...)
	traceLinks := getLinks(traceData, traceId)
	traceEvents := getEvents(traceData)
	spanContext := CreateSpanContext(byteSpanId, traceId, traceState)
	parent := CreateSpanContext(byteParentSpanId, traceId, "")
	spanStub := SpanStub{
		Name:        name,
		StartTime:   startTime,
		EndTime:     endTime,
		SpanKind:    trace.SpanKind(kind),
		SpanContext: spanContext,
		Parent:      parent,
		Status:      status,
		Resource:    newResource,
		Attributes:  attributes,
		Events:      traceEvents,
		Links:       traceLinks,
	}
	return spanStub
}

func (s spanSnapshot) Name() string { return s.name }

func (s spanSnapshot) SpanContext() trace.SpanContext { return s.spanContext }

func (s spanSnapshot) Parent() trace.SpanContext { return s.parent }

func (s spanSnapshot) SpanKind() trace.SpanKind { return s.spanKind }

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
