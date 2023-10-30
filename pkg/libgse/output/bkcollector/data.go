package bkcollector

import (
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
func getSpanId(spanId string) [8]byte {
	var byteSpanId [8]byte
	copy(byteSpanId[:], spanId)
	return byteSpanId
}
func getKeyValue(attributes map[string]interface{}) []attribute.KeyValue {
	var result = make([]attribute.KeyValue, 0, 0)
	for key, value := range attributes {
		v, _ := value.(string)
		_value := attribute.StringValue(v)
		attr := attribute.KeyValue{
			Key:   attribute.Key(key),
			Value: _value,
		}
		result = append(result, attr)
	}
	return result
}
func getEvents(events []interface{}) []tracesdk.Event {
	var result = make([]tracesdk.Event, 0, 0)
	for _, event := range events {
		eventMap := event.(map[string]interface{})
		Name := eventMap["name"].(string)
		timestamp := eventMap["timestamp"].(float64)
		eventTime := getTime(timestamp)
		attributes := eventMap["attributes"].(map[string]interface{})
		Attributes := getKeyValue(attributes)
		Event := tracesdk.Event{
			Name:       Name,
			Time:       eventTime,
			Attributes: Attributes,
		}
		result = append(result, Event)
	}
	return result
}
func getLinks(links []interface{}, traceId [16]byte) []tracesdk.Link {
	var result = make([]tracesdk.Link, 0, 0)
	for _, link := range links {
		linkMap := link.(map[string]interface{})
		spanId := linkMap["span_id"].(string)
		SpanId := getSpanId(spanId)
		var TraceState string
		traceState, ok := linkMap["trace_state"]
		if ok {
			TraceState = traceState.(string)
		}
		SpanContext := CreateSpanContext(SpanId, traceId, TraceState)
		attributes := linkMap["attributes"].(map[string]interface{})
		Attributes := getKeyValue(attributes)
		Link := tracesdk.Link{
			SpanContext: SpanContext,
			Attributes:  Attributes,
		}
		result = append(result, Link)
	}
	return result
}

func CreateSpanContext(spanId [8]byte, traceId [16]byte, traceState string) trace.SpanContext {
	TracesSate, err := trace.ParseTraceState(traceState)
	if err != nil {
		logp.Err("traceState err! ")
	}
	SpanContextConfig := trace.SpanContextConfig{
		TraceID:    traceId,
		SpanID:     spanId,
		TraceState: TracesSate,
	}
	SpanContext := trace.NewSpanContext(SpanContextConfig)
	return SpanContext

}
func PushData(traceData map[string]interface{}, bkDataToken string) []tracesdk.ReadOnlySpan {
	traceId := traceData["trace_id"].(string)
	spanId := traceData["span_id"].(string)
	var ParentSpanId string
	parentSpanId, ok := traceData["parent_span_id"]
	if ok {
		ParentSpanId = parentSpanId.(string)
	}
	traceState := traceData["trace_state"].(string)
	byteSpanId := getSpanId(spanId)
	byteParentSpanId := getSpanId(ParentSpanId)
	var byteTraceId [16]byte
	copy(byteTraceId[:], traceId)
	startTime := traceData["start_time"].(float64)
	endTime := traceData["end_time"].(float64)
	StartTime := getTime(startTime)
	EndTime := getTime(endTime)
	kind := traceData["kind"].(float64)
	SpanKind := int(kind)
	code := traceData["status"].(map[string]interface{})["code"].(float64)
	Code := uint32(code)
	attributes := traceData["attributes"].(map[string]interface{})
	Attributes := getKeyValue(attributes)

	tracedResource := traceData["resource"].(map[string]interface{})
	tracedResource["bk.data.token"] = bkDataToken
	TraceDataResource := getKeyValue(tracedResource)
	newResource := resource.NewSchemaless(TraceDataResource...)
	events := traceData["events"].([]interface{})
	links := traceData["links"].([]interface{})
	Links := getLinks(links, byteTraceId)
	Events := getEvents(events)
	SpanContext := CreateSpanContext(byteSpanId, byteTraceId, traceState)
	Parent := CreateSpanContext(byteParentSpanId, byteTraceId, "")

	roSpans := SpanStubs{{
		Name:        traceData["span_name"].(string),
		StartTime:   StartTime,
		EndTime:     EndTime,
		SpanKind:    trace.SpanKind(SpanKind),
		SpanContext: SpanContext,
		Parent:      Parent,
		Status: tracesdk.Status{
			Code:        codes.Code(Code),
			Description: traceData["status"].(map[string]interface{})["message"].(string),
		},
		Resource:   newResource,
		Attributes: Attributes,
		Events:     Events,
		Links:      Links,
	}}.Snapshots()

	return roSpans

}

func (s spanSnapshot) Name() string                     { return s.name }
func (s spanSnapshot) SpanContext() trace.SpanContext   { return s.spanContext }
func (s spanSnapshot) Parent() trace.SpanContext        { return s.parent }
func (s spanSnapshot) SpanKind() trace.SpanKind         { return s.spanKind }
func (s spanSnapshot) StartTime() time.Time             { return s.startTime }
func (s spanSnapshot) EndTime() time.Time               { return s.endTime }
func (s spanSnapshot) Attributes() []attribute.KeyValue { return s.attributes }
func (s spanSnapshot) Links() []tracesdk.Link           { return s.links }
func (s spanSnapshot) Events() []tracesdk.Event         { return s.events }
func (s spanSnapshot) Status() tracesdk.Status          { return s.status }
func (s spanSnapshot) DroppedAttributes() int           { return s.droppedAttributes }
func (s spanSnapshot) DroppedLinks() int                { return s.droppedLinks }
func (s spanSnapshot) DroppedEvents() int               { return s.droppedEvents }
func (s spanSnapshot) ChildSpanCount() int              { return s.childSpanCount }
func (s spanSnapshot) Resource() *resource.Resource     { return s.resource }
func (s spanSnapshot) InstrumentationScope() instrumentation.Scope {
	return s.instrumentationScope
}
