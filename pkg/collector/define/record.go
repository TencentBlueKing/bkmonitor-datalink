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
	"net/http"
	"strings"
	"time"

	"github.com/google/pprof/profile"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/prometheus/prompb"
	"google.golang.org/grpc/metadata"
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
	SourceLogBeat     = "logbeat"

	KeyToken    = "X-BK-TOKEN"
	KeyDataID   = "X-BK-DATA-ID"
	KeyTenantID = "X-Tps-TenantID"

	basicAuthUsername = "bkmonitor"
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
	RecordFta            RecordType = "fta"
	RecordRemoteWrite    RecordType = "remotewrite"
	RecordProxy          RecordType = "proxy"
	RecordPingserver     RecordType = "pingserver"
	RecordLogBeat        RecordType = "logbeat"
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
	case RecordFta.S():
		t = RecordFta
	case RecordLogBeat.S():
		t = RecordLogBeat
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
	DataId      int64       `json:"data_id"`
	AccessToken string      `json:"access_token"`
	Version     string      `json:"version"`
	Data        interface{} `json:"data"`
	Type        string      // 标识为 ProxyMetric 或者 ProxyEvent
}

type LogBeatData struct {
	Data []byte
}

const (
	ProxyMetricType = "metric"
	ProxyEventType  = "event"
)

type ProxyMetric struct {
	Metrics   map[string]float64 `json:"metrics"`
	Target    string             `json:"target"`
	Dimension map[string]string  `json:"dimension"`
	Timestamp int64              `json:"timestamp"`
}

type ProxyEvent struct {
	EventName string                 `json:"event_name"`
	Event     map[string]interface{} `json:"event"`
	Target    string                 `json:"target"`
	Dimension map[string]string      `json:"dimension"`
	Timestamp int64                  `json:"timestamp"`
}

type PingserverData struct {
	DataId  int64                  `json:"data_id"`
	Version string                 `json:"version"`
	Data    map[string]interface{} `json:"data"`
}

type FtaData struct {
	PluginId   string                   `json:"bk_plugin_id"`
	IngestTime int64                    `json:"bk_ingest_time"`
	Data       []map[string]interface{} `json:"data"`
	EventId    string                   `json:"__bk_event_id__"`
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

func (t Token) BizApp() string {
	return fmt.Sprintf("%d-%s", t.BizId, t.AppName)
}

func (t Token) GetDataID(rtype RecordType) int32 {
	switch rtype {
	case RecordTraces, RecordTracesDerived:
		return t.TracesDataId
	case RecordMetrics, RecordMetricsDerived, RecordPushGateway, RecordRemoteWrite, RecordPingserver, RecordFta:
		return t.MetricsDataId
	case RecordLogs, RecordLogsDerived, RecordLogBeat:
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

func TokenFromHttpRequest(req *http.Request) string {
	// 1) 从 tokenKey 中读取
	token := req.URL.Query().Get(KeyToken)
	if token == "" {
		token = req.Header.Get(KeyToken)
	}
	if token != "" {
		return token
	}

	// 2) 从 tenantidKey 中读取
	token = req.Header.Get(KeyTenantID)
	if token == "" {
		token = req.URL.Query().Get(KeyTenantID)
	}
	if token != "" {
		return token
	}

	// 3）从 basicauth 中读取（当且仅当 username 为 bkmonitor 才生效
	username, password, ok := req.BasicAuth()
	if ok && username == basicAuthUsername && password != "" {
		return password
	}

	// 4）从 bearerauth 中读取 token
	bearer := strings.Split(req.Header.Get("Authorization"), "Bearer ")
	if len(bearer) == 2 {
		return bearer[1]
	}

	// 弃疗 ┓(-´∀`-)┏
	return ""
}

func TokenFromGrpcMetadata(md metadata.MD) string {
	// 1) 从 tokenKey 中读取
	token := md.Get(KeyToken)
	if len(token) > 0 {
		return token[0]
	}

	// 2) 从 tenantidKey 中读取
	token = md.Get(KeyTenantID)
	if len(token) > 0 {
		return token[0]
	}
	return ""
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
