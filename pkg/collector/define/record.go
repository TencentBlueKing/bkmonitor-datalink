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
	"strings"
	"time"

	"github.com/TarsCloud/TarsGo/tars/protocol/res/propertyf"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/statf"
	"github.com/google/pprof/profile"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/prompb"
)

const (
	MonitoringNamespace = "bk_collector"

	ContentType         = "Content-Type"
	ContentTypeProtobuf = "application/x-protobuf"
	ContentTypeJson     = "application/json"
	ContentTypeText     = "text/plain; charset=utf-8"

	SourceFta         = "fta"
	SourceJaeger      = "jaeger"
	SourcePyroscope   = "pyroscope"
	SourceOtlp        = "otlp"
	SourcePushGateway = "pushgateway"
	SourceRemoteWrite = "remotewrite"
	SourceZipkin      = "zipkin"
	SourceProxy       = "proxy"
	SourceSkywalking  = "skywalking"
	SourceBeat        = "beat"
	SourceTars        = "tars"
	SourceLogPush     = "logpush"

	KeyToken        = "X-BK-TOKEN"
	KeyDataID       = "X-BK-DATA-ID"
	KeyUserMetadata = "X-BK-METADATA"
	KeyTenantID     = "X-Tps-TenantID"
)

type RecordType string

func (r RecordType) S() string { return string(r) }

const (
	RecordUndefined       RecordType = "undefined"
	RecordTraces          RecordType = "traces"
	RecordProfiles        RecordType = "profiles"
	RecordMetrics         RecordType = "metrics"
	RecordLogs            RecordType = "logs"
	RecordPushGateway     RecordType = "pushgateway"
	RecordFta             RecordType = "fta"
	RecordRemoteWrite     RecordType = "remotewrite"
	RecordProxy           RecordType = "proxy"
	RecordPingserver      RecordType = "pingserver"
	RecordBeat            RecordType = "beat"
	RecordTars            RecordType = "tars"
	RecordLogPush         RecordType = "logpush"
	RecordMetricV2        RecordType = "metricv2"
	RecordMetricV2Derived RecordType = "metricv2.derived" // 仅在内部流转使用
	RecordEventV2         RecordType = "eventv2"
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
	case RecordFta.S():
		t = RecordFta
	case RecordBeat.S():
		t = RecordBeat
	case RecordTars.S():
		t = RecordTars
	case RecordLogPush.S():
		t = RecordLogPush
	case RecordMetricV2.S():
		t = RecordMetricV2
	case RecordMetricV2Derived.S():
		t = RecordMetricV2Derived
	case RecordEventV2.S():
		t = RecordEventV2
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
	RequestTars    RequestType = "tars"
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
	Metadata      map[string]string
	Data          any
}

func (r *Record) Unwrap() {
	switch r.RecordType {
	case RecordMetricV2Derived:
		r.RecordType = RecordMetricV2
	}
}

type PushGatewayData struct {
	MetricFamilies *dto.MetricFamily
	Labels         map[string]string
}

type RemoteWriteData struct {
	Timeseries []prompb.TimeSeries
}

type LogPushData struct {
	Data   []string
	Labels map[string]string
}

const (
	TarsStatType     = "stat"
	TarsPropertyType = "property"
)

type TarsData struct {
	Type      string // 标识为 TarsStatType 或者 EventV2
	Timestamp int64
	Data      any
}

// TarsPropertyData 属性统计数据
type TarsPropertyData struct {
	Props map[propertyf.StatPropMsgHead]propertyf.StatPropMsgBody
}

// TarsStatData 服务指标数据
type TarsStatData struct {
	FromClient bool
	Stats      map[statf.StatMicMsgHead]statf.StatMicMsgBody
}

type BeatData struct {
	Data []byte
}

type ProxyData struct {
	DataId      int64  `json:"data_id"`
	AccessToken string `json:"access_token"`
	Version     string `json:"version"`
	Data        any    `json:"data"`
	Type        string // 标识为 MetricV2 或者 EventV2
}

const (
	ProxyMetricType = "metric"
	ProxyEventType  = "event"
)

// MetricV2 自定义指标格式
type MetricV2 struct {
	Metrics   map[string]float64 `json:"metrics"`
	Target    string             `json:"target"`
	Dimension map[string]string  `json:"dimension"`
	Timestamp int64              `json:"timestamp"`
}

// EventV2 自定义事件格式
type EventV2 struct {
	EventName string            `json:"event_name"`
	Event     map[string]any    `json:"event"`
	Target    string            `json:"target"`
	Dimension map[string]string `json:"dimension"`
	Timestamp int64             `json:"timestamp"`
}

type MetricV2Data struct {
	Data []MetricV2
}

type EventV2Data struct {
	Data []EventV2
}

type PingserverData struct {
	DataId  int64          `json:"data_id"`
	Version string         `json:"version"`
	Data    map[string]any `json:"data"`
}

type FtaData struct {
	PluginId   string           `json:"bk_plugin_id"`
	IngestTime int64            `json:"bk_ingest_time"`
	Data       []map[string]any `json:"data"`
	EventId    string           `json:"__bk_event_id__"`
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

const (
	FormatPprof = "pprof"
	FormatJFR   = "jfr"
)

// ProfileMetadata Profile 元数据格式
type ProfileMetadata struct {
	StartTime       time.Time
	EndTime         time.Time
	AppName         string
	BkBizID         int
	SpyName         string
	Format          string
	SampleRate      uint32
	Units           string
	AggregationType string
	Tags            map[string]string
}

type ProfilePprofFormatOrigin []byte

type ProfileJfrFormatOrigin struct {
	Jfr    []byte
	Labels []byte
}

type ProfilesRawData struct {
	Metadata ProfileMetadata
	// Data Profile 原始数据
	// Format = pprof -> PprofFormatOrigin
	// Format = jfr -> JfrFormatOrigin
	Data any
}

// ProfilesData 为 ProfilesRawData 经过处理后的数据格式
type ProfilesData struct {
	Profiles []*profile.Profile
	Metadata ProfileMetadata
}
