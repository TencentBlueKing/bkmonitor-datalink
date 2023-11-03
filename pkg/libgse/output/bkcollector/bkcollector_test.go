//// Tencent is pleased to support the open source community by making
//// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
//// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
//// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
//// You may obtain a copy of the License at http://opensource.org/licenses/MIT
//// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
//// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
//// specific language governing permissions and limitations under the License.

package bkcollector

import (
	"encoding/json"
	"net"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
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

func TestGetEvents(t *testing.T) {
	var traceData TraceData
	err := json.Unmarshal([]byte(jsonStr), &traceData)
	result := getEvents(&traceData)
	assert.Equal(t, "[]trace.Event", reflect.TypeOf(result).String())
	assert.Equal(t, nil, err)
}

func TestGetLinks(t *testing.T) {
	var traceData TraceData
	err := json.Unmarshal([]byte(jsonStr), &traceData)
	result := getLinks(&traceData)
	assert.Equal(t, "[]trace.Link", reflect.TypeOf(result).String())
	assert.Equal(t, nil, err)
}

func TestGetKeyValue(t *testing.T) {
	var traceData TraceData
	err := json.Unmarshal([]byte(jsonStr), &traceData)
	result := getKeyValue(traceData.Attributes)
	assert.Equal(t, "[]attribute.KeyValue", reflect.TypeOf(result).String())
	assert.Equal(t, nil, err)

}

//	func TestCreateSpanContext(t *testing.T) {
//		mapData := ToMap(jsonStr)
//		traceId := getTraceId(mapData)
//		byteSpanId := getSpanId(mapData)
//		traceState := "rojo=00f067aa0ba902b7"
//		result := CreateSpanContext(byteSpanId, traceId, traceState)
//		assert.Equal(t, "trace.SpanContext", reflect.TypeOf(result).String())
//	}
func TestNewExporter(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:4317")
	if err != nil {
		t.Fatalf("Failed to grab an available port: %v", err)
	}
	result, err := NewExporter("localhost:4317")
	_ = ln.Close()
	assert.Equal(t, "*otlptrace.Exporter", reflect.TypeOf(result).String())
	assert.Equal(t, nil, err)
}
func TestNewOutput(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:4317")
	if err != nil {
		t.Fatalf("Failed to grab an available port: %v", err)
	}
	testConfig := defaultConfig
	testConfig.BkDataToken = "123"
	testConfig.GrpcHost = "localhost:4317"
	result, err := NewOutput(testConfig)
	_ = ln.Close()
	assert.Equal(t, "*bkcollector.Output", reflect.TypeOf(result).String())
	assert.Equal(t, "123", result.bkDataToken)
	assert.Equal(t, "bkcollector", result.String())
	assert.Equal(t, nil, err)
}

func TestGetCode(t *testing.T) {
	var traceData TraceData
	err := json.Unmarshal([]byte(jsonStr), &traceData)
	result := getStatus(&traceData)
	assert.Equal(t, "trace.Status", reflect.TypeOf(result).String())
	assert.Equal(t, nil, err)

}
