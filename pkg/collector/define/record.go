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
	"time"

	"github.com/TarsCloud/TarsGo/tars/protocol/res/propertyf"
	"github.com/TarsCloud/TarsGo/tars/protocol/res/statf"
	"github.com/google/pprof/profile"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
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

	KeyToken        = "X-BK-TOKEN"
	KeyDataID       = "X-BK-DATA-ID"
	KeyUserMetadata = "X-BK-METADATA"
	KeyTenantID     = "X-Tps-TenantID"
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
	RecordBeat           RecordType = "beat"
	RecordTars           RecordType = "tars"
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
	case RecordBeat.S():
		t = RecordBeat
	case RecordTars.S():
		t = RecordTars
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

const (
	TarsStatType     = "stat"
	TarsPropertyType = "property"
)

type TarsAdapter struct {
	Name     string
	Servant  string
	Endpoint string
}

type TarsServerConfig struct {
	App      string
	Server   string
	LogPath  string
	LogLevel string
	Adapters []TarsAdapter
}

type TarsData struct {
	// 标识为 TarsStatType 或者 ProxyEvent
	Type      string
	Timestamp int64
	Data      interface{}
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

type ProxyData struct {
	DataId      int64       `json:"data_id"`
	AccessToken string      `json:"access_token"`
	Version     string      `json:"version"`
	Data        interface{} `json:"data"`
	Type        string      // 标识为 ProxyMetric 或者 ProxyEvent
}

type BeatData struct {
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

const (
	TokenAppName = "app_name"
)

// Token 描述了 Record 校验的必要信息
type Token struct {
	Type           string `config:"type"`
	Original       string `config:"token"`
	BizId          int32  `config:"bk_biz_id"`
	AppName        string `config:"bk_app_name"`
	MetricsDataId  int32  `config:"metrics_dataid"`
	TracesDataId   int32  `config:"traces_dataid"`
	ProfilesDataId int32  `config:"profiles_dataid"`
	LogsDataId     int32  `config:"logs_dataid"`
	ProxyDataId    int32  `config:"proxy_dataid"`
	BeatDataId     int32  `config:"beat_dataid"`
}

var tokenInfo = promauto.NewGaugeVec(
	prometheus.GaugeOpts{
		Namespace: MonitoringNamespace,
		Name:      "receiver_token_info",
		Help:      "Receiver decoded token info",
	},
	[]string{"token", "metrics_id", "traces_id", "logs_id", "profiles_id", "proxy_id", "app_name", "biz_id"},
)

func SetTokenInfo(token Token) {
	tokenInfo.WithLabelValues(
		token.Original,
		fmt.Sprintf("%d", token.MetricsDataId),
		fmt.Sprintf("%d", token.TracesDataId),
		fmt.Sprintf("%d", token.LogsDataId),
		fmt.Sprintf("%d", token.ProfilesDataId),
		fmt.Sprintf("%d", token.ProxyDataId),
		token.AppName,
		fmt.Sprintf("%d", token.BizId),
	).Set(1)
}

func (t Token) BizApp() string {
	return fmt.Sprintf("%d-%s", t.BizId, t.AppName)
}

func (t Token) GetDataID(rtype RecordType) int32 {
	switch rtype {
	case RecordTraces, RecordTracesDerived:
		return t.TracesDataId
	case RecordMetrics, RecordMetricsDerived, RecordPushGateway, RecordRemoteWrite, RecordPingserver, RecordFta, RecordTars:
		return t.MetricsDataId
	case RecordLogs, RecordLogsDerived:
		return t.LogsDataId
	case RecordProfiles:
		return t.ProfilesDataId
	case RecordProxy:
		return t.ProxyDataId
	case RecordBeat:
		return t.BeatDataId
	}
	return -1
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
