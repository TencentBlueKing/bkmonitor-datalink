package bkcollector

import (
	"github.com/stretchr/testify/assert"
	"net"
	"reflect"
	"testing"
)

var jsonStr = "{\"attributes\": {\"api_name\": \"GET\"}, " +
	"\"elapsed_time\": 62345667, \"end_time\": 1697182342209576, " +
	"\"events\": [{\"timestamp\": 1697601000959429, " +
	"\"attributes\": {\"api_name\": \"GET\"}, \"name\": \"log\"}], " +
	"\"kind\": 2, \"links\": [{\"span_id\": \"a49c0fc65429cf78\", " +
	"\"attributes\": {\"api_name\": \"GET\"}}], " +
	"\"parent_span_id\": \"b8fd7234e727c351\"," +
	" \"resource\": {\"service.name\": \"service1\"}, " +
	"\"span_id\": \"a49c0fc65429cf78\", " +
	"\"span_name\": \"HTTP GET\", " +
	"\"start_time\": 1697182279863908, " +
	"\"status\": {\"code\": 0, \"message\": \"\"}, " +
	"\"time\": \"1697182343000\", " +
	"\"trace_id\": \"a47d4bb2397def77bd80c3b2ffbf1a33\", " +
	"\"trace_state\": \"rojo=00f067aa0ba902b7\"}"

func TestGetIpPort(t *testing.T) {
	host := "http://127.0.0.1:4317"
	ip, port, _ := GetIpPort(host)
	assert.Equal(t, "127.0.0.1", ip)
	assert.Equal(t, "4317", port)
}

func TestToMap(t *testing.T) {

	mapData := ToMap(jsonStr)
	attributes := map[string]interface{}{
		"api_name": "GET",
	}
	var kind float64
	kind = 2
	events := mapData["events"].([]interface{})
	event := events[0].(map[string]interface{})
	assert.Equal(t, "a47d4bb2397def77bd80c3b2ffbf1a33", mapData["trace_id"].(string))
	assert.Equal(t, "a49c0fc65429cf78", mapData["span_id"].(string))
	assert.Equal(t, "b8fd7234e727c351", mapData["parent_span_id"].(string))
	assert.Equal(t, "HTTP GET", mapData["span_name"].(string))
	assert.Equal(t, kind, mapData["kind"].(float64))
	assert.Equal(t, "rojo=00f067aa0ba902b7", mapData["trace_state"].(string))
	assert.Equal(t, attributes, mapData["attributes"].(map[string]interface{}))
	assert.Equal(t, "log", event["name"].(string))
	assert.Equal(t, attributes, event["attributes"].(map[string]interface{}))

}
func TestGetEvents(t *testing.T) {
	mapData := ToMap(jsonStr)
	events := mapData["events"].([]interface{})
	result := getEvents(events)
	assert.Equal(t, "[]trace.Event", reflect.TypeOf(result).String())
}

func TestGetLinks(t *testing.T) {
	mapData := ToMap(jsonStr)
	links := mapData["links"].([]interface{})
	traceId := mapData["trace_id"].(string)
	var byteTraceId [16]byte
	copy(byteTraceId[:], traceId)
	result := getLinks(links, byteTraceId)
	assert.Equal(t, "[]trace.Link", reflect.TypeOf(result).String())
}
func TestGetKeyValue(t *testing.T) {
	mapData := ToMap(jsonStr)
	attributes := mapData["attributes"].(map[string]interface{})

	result := getKeyValue(attributes)
	assert.Equal(t, "[]attribute.KeyValue", reflect.TypeOf(result).String())
}

func TestPushData(t *testing.T) {
	bkDataToken := "123"
	mapData := ToMap(jsonStr)
	result := PushData(mapData, bkDataToken)
	assert.Equal(t, "[]trace.ReadOnlySpan", reflect.TypeOf(result).String())

}

func TestCreateSpanContext(t *testing.T) {
	traceId := "a47d4bb2397def77bd80c3b2ffbf1a33"
	var byteTraceId [16]byte
	copy(byteTraceId[:], traceId)
	byteSpanId := getSpanId("a49c0fc65429cf78")
	traceState := "rojo=00f067aa0ba902b7"
	result := CreateSpanContext(byteSpanId, byteTraceId, traceState)
	assert.Equal(t, "trace.SpanContext", reflect.TypeOf(result).String())

}

func TestBkCollectorConnect(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:4317")
	if err != nil {
		t.Fatalf("Failed to grab an available port: %v", err)
	}
	result := BkCollectorConnect("localhost", "4317")
	assert.Equal(t, nil, result)

	_ = ln.Close()
}

func TestNewExporter(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:4317")
	if err != nil {
		t.Fatalf("Failed to grab an available port: %v", err)
	}
	result := NewExporter("localhost", "4317")
	_ = ln.Close()
	assert.Equal(t, "*otlptrace.Exporter", reflect.TypeOf(result).String())
}
func TestNewOutput(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:4317")
	bkDataToken := "123"
	if err != nil {
		t.Fatalf("Failed to grab an available port: %v", err)
	}
	result := NewOutput("localhost", "4317", bkDataToken)
	_ = ln.Close()
	assert.Equal(t, "*bkcollector.Output", reflect.TypeOf(result).String())
	assert.Equal(t, bkDataToken, result.bkdatatoken)
	assert.Equal(t, "bkcollector", result.String())
}
