// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"fmt"
	"strings"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/prompb"
)

const (
	MonitoringNamespace = "bk_collector"

	ContentType         = "Content-Type"
	ContentTypeProtobuf = "application/x-protobuf"
	ContentTypeJson     = "application/json"
	ContentTypeText     = "text/plain; charset=utf-8"

	SourceJaeger      = "jaeger"
	SourcePyroscope   = "pyroscope"
	SourceOtlp        = "otlp"
	SourcePushGateway = "pushgateway"
	SourceRemoteWrite = "remotewrite"
	SourceZipkin      = "zipkin"
	SourceProxy       = "proxy"
	SourceSkywalking  = "skywalking"
)

type RecordType string

func (r RecordType) S() string { return string(r) }

const (
	RecordUndefined      RecordType = "undefined"
	RecordTraces         RecordType = "traces"
	RecordProfiles       RecordType = "profiles"
	RecordMetrics        RecordType = "metrics"
	RecordLogs           RecordType = "logs"
	RecordTracesDerived  RecordType = "traces.derived"
	RecordMetricsDerived RecordType = "metrics.derived"
	RecordLogsDerived    RecordType = "logs.derived"
	RecordPushGateway    RecordType = "pushgateway"
	RecordRemoteWrite    RecordType = "remotewrite"
	RecordProxy          RecordType = "proxy"
	RecordPingserver     RecordType = "pingserver"
)

// IntoRecordType 将字符串描述转换为 RecordType 并返回是否为 Derived 类型
func IntoRecordType(s string) (RecordType, bool) {
	var t RecordType
	switch s {
	case RecordTraces.S():
		t = RecordTraces
	case RecordMetrics.S():
		t = RecordMetrics
	case RecordLogs.S():
		t = RecordLogs
	case RecordTracesDerived.S():
		t = RecordTracesDerived
	case RecordMetricsDerived.S():
		t = RecordMetricsDerived
	case RecordLogsDerived.S():
		t = RecordLogsDerived
	case RecordPushGateway.S():
		t = RecordPushGateway
	case RecordRemoteWrite.S():
		t = RecordRemoteWrite
	case RecordProxy.S():
		t = RecordProxy
	case RecordPingserver.S():
		t = RecordPingserver
	case RecordProfiles.S():
		t = RecordProfiles
	default:
		t = RecordUndefined
	}
	return t, strings.HasSuffix(s, ".derived")
}

// RequestType 标记请求类型：Http、Grpc 用于后续做统计
type RequestType string

func (r RequestType) S() string { return string(r) }

const (
	RequestHttp    RequestType = "http"
	RequestGrpc    RequestType = "grpc"
	RequestICMP    RequestType = "icmp"
	RequestDerived RequestType = "derived"
)

type RequestClient struct {
	IP string
}

type PreCheckValidateFunc func(*Record) (StatusCode, string, error)

// Record 是 Processor 链传输的数据类型
type Record struct {
	RecordType    RecordType
	RequestType   RequestType
	RequestClient RequestClient
	Token         Token
	Data          interface{}
}

func (r *Record) Unwrap() {
	switch r.RecordType {
	case RecordTracesDerived:
		r.RecordType = RecordTraces
	case RecordMetricsDerived:
		r.RecordType = RecordMetrics
	case RecordLogsDerived:
		r.RecordType = RecordLogs
	}
}

type PushGatewayData struct {
	MetricFamilies *dto.MetricFamily
	Labels         map[string]string
}

type RemoteWriteData struct {
	Timeseries []prompb.TimeSeries
}

type ProxyData struct {
	DataId      int64                  `json:"data_id"`
	AccessToken string                 `json:"access_token"`
	Version     string                 `json:"version"`
	Data        interface{}            `json:"data"`
	Extra       map[string]interface{} `json:"bk_info"`
}

type PingserverData struct {
	DataId  int64                  `json:"data_id"`
	Version string                 `json:"version"`
	Data    map[string]interface{} `json:"data"`
}

type PushMode string

const (
	PushModeGuarantee  PushMode = "guarantee"
	PushModeDropIfFull PushMode = "dropIfFull"
)

type RecordQueue struct {
	records chan *Record
	mode    PushMode
}

// NewRecordQueue 生成 Records 消息队列
func NewRecordQueue(mode PushMode) *RecordQueue {
	return &RecordQueue{
		mode:    mode,
		records: make(chan *Record, Concurrency()*QueueAmplification),
	}
}

func (q *RecordQueue) Push(r *Record) {
	switch q.mode {
	case PushModeGuarantee:
		q.records <- r
	case PushModeDropIfFull:
		select {
		case q.records <- r:
		default:
		}
	}
}

func (q *RecordQueue) Get() <-chan *Record {
	return q.records
}

// Token 描述了 Record 校验的必要信息
type Token struct {
	Original       string
	MetricsDataId  int32
	TracesDataId   int32
	ProfilesDataId int32
	LogsDataId     int32
	ProxyDataId    int32
	BizId          int32
	AppName        string
}

func (t Token) GetDataID(rtype RecordType) int32 {
	switch rtype {
	case RecordTraces, RecordTracesDerived:
		return t.TracesDataId
	case RecordMetrics, RecordMetricsDerived, RecordPushGateway, RecordRemoteWrite, RecordPingserver:
		return t.MetricsDataId
	case RecordLogs, RecordLogsDerived:
		return t.LogsDataId
	case RecordProfiles:
		return t.ProfilesDataId
	case RecordProxy:
		return t.ProxyDataId
	}
	return -1
}

func WrapProxyToken(token Token) string {
	return fmt.Sprintf("%d/%s", token.ProxyDataId, token.Original)
}
