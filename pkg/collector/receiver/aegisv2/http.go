package aegisv2

// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/pmetric"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	routeV2Collect        = "/collect"
	routeAegisV2Whitelist = "/aegiscontrol/whitelist"
)

func init() {
	receiver.RegisterReadyFunc(define.SourceAegisV2, Ready)
}

func Ready() {
	receiver.RegisterRecvHttpRoute(define.SourceAegisV2, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeV2Collect,
			HandlerFunc:  httpSvc.ExportCollect,
		},
		{
			Method:       http.MethodGet,
			RelativePath: routeAegisV2Whitelist,
			HandlerFunc:  httpSvc.Whitelist,
		},
	})

}

type httpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc httpService
var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceAegisV2)

// 使用结构体而非 map，避免 JSON 输出字段顺序不稳定。
var whitelistSample = whitelistSampleMap{
	API:                100,
	AssetSpeed:         100,
	PagePerformance:    100,
	PV:                 100,
	Custom:             100,
	Session:            100,
	Error:              100,
	BridgeSpeed:        100,
	LoadPackageSpeed:   100,
	Websocket:          100,
	Replay:             100,
	ProcessPerformance: 100,
}

var fallbackMsg = []byte(`{"code": 13, "message": "failed to marshal error message"}`)

type whitelistResponse struct {
	Code          int                `json:"code"`
	Msg           string             `json:"msg"`
	IsInWhiteList int                `json:"is_in_white_list"`
	SampleMap     whitelistSampleMap `json:"sample_map"`
	// ServerTime 为服务端收到本次 whitelist 请求的时间（毫秒时间戳，数字格式）。
	ServerTime int64 `json:"server_time"`
	// StartServerTime 为客户端发起请求的时间（毫秒时间戳，数字格式）。
	StartServerTime int64 `json:"start_server_time"`
}

// whitelistSampleMap 各上报类型的采样率，100 表示全量上报，0 表示不上报。
// 字段需与 types.go 中的 EventType 常量保持一一对应。
type whitelistSampleMap struct {
	API                int `json:"api"`                 // API 请求
	AssetSpeed         int `json:"asset_speed"`         // 静态资源加载速度
	PagePerformance    int `json:"page_performance"`    // 页面性能
	PV                 int `json:"pv"`                  // 页面访问量
	Custom             int `json:"custom"`              // 自定义事件
	Session            int `json:"session"`             // 用户会话
	Error              int `json:"error"`               // 错误事件
	BridgeSpeed        int `json:"bridge_speed"`        // JSBridge 调用速度
	LoadPackageSpeed   int `json:"load_package_speed"`  // 包加载速度
	Websocket          int `json:"websocket"`           // WebSocket 事件
	Replay             int `json:"replay"`              // 会话回放
	ProcessPerformance int `json:"process_performance"` // 进程性能
}

func (s *httpService) processExport(
	ip string,
	start time.Time,
	contentType string,
	tk string,
	metadata map[string]string,
	bs []byte,
	rtype define.RecordType,
) (define.StatusCode, error) {
	var traceID pcommon.TraceID
	if rtype == define.RecordTraces {
		traceID = random.TraceID()
	}
	rh := newResponseHandler(contentType, traceID)
	data, err := rh.Unmarshal(rtype, sanitizePayload(bs))
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, rtype)
		preview := "<empty>"
		if len(bs) > 0 {
			if len(bs) > 80 {
				preview = string(bs[:80]) + "..."
			} else {
				preview = string(bs)
			}
		}
		logger.Warnf("aegisv2 failed to unmarshal body, rtype=%s, ip=%v, error: %s, preview: %v", rtype.S(), ip, err, preview)
		return define.StatusBadRequest, err
	}
	return s.publishRecord(ip, start, tk, metadata, bs, rtype, data)
}

// publishRecord 构建 Record、执行 pre-check 校验并发布，同时记录处理指标。
func (s *httpService) publishRecord(
	ip string,
	start time.Time,
	tk string,
	metadata map[string]string,
	bs []byte,
	rtype define.RecordType,
	data any,
) (define.StatusCode, error) {
	r := &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    rtype,
		Data:          data,
	}
	if len(tk) > 0 {
		r.Token = define.Token{Original: tk}
	}
	r.Metadata = metadata

	prettyprint.Pretty(rtype, data)

	code, processorName, err := s.Validate(r)
	if err != nil {
		logger.Warnf("aegisv2 pre-check failed, rtype=%s, code=%d, ip=%v, error: %s", rtype.S(), code, ip, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, rtype, processorName, r.Token.Original, code)
		return code, err
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, rtype, len(bs), start)
	return define.StatusCodeOK, nil
}

func (s *httpService) ExportCollect(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()
	bs, err := io.ReadAll(req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, nil)
		logger.Errorf("aegisv2 /collect failed to read body, ip=%v, error: %s", ip, err)
		return
	}

	contentType := req.Header.Get(define.ContentType)
	tk := tokenparser.FromHttpRequest(req)
	metadata := tokenparser.FromHttpUserMetadata(req)

	code, err := s.processCollect(ip, start, contentType, tk, metadata, bs)
	if err != nil {
		receiver.WriteResponse(w, define.ContentTypeJson, int(code), nil)
		return
	}
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte("{}"))
}

// processCollect 将 /collect 请求只解析一次 payload，派发 traces；
// 若 payload 含有效 web_vitals 数据，额外派生 metrics，以兼容只打 /collect 的 SDK。
func (s *httpService) processCollect(
	ip string,
	start time.Time,
	contentType string,
	tk string,
	metadata map[string]string,
	bs []byte,
) (define.StatusCode, error) {
	sanitizedBs := sanitizePayload(bs)

	// Protobuf 或非 aegisv2 JSON 格式，走普通 traces 路径，不派生 metrics。
	if contentType == define.ContentTypeProtobuf {
		return s.processExport(ip, start, contentType, tk, metadata, bs, define.RecordTraces)
	}

	payload, err := parseCollectPayload(sanitizedBs)
	if err != nil {
		return s.processExport(ip, start, contentType, tk, metadata, bs, define.RecordTraces)
	}

	records, err := parseD2Records(payload.D2)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordTraces)
		logger.Warnf("aegisv2 /collect failed to parse records, ip=%v, error: %s", ip, err)
		return define.StatusBadRequest, err
	}

	traceID := random.TraceID()
	traces, collector, err := convertTraces(payload, records, traceID)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordTraces)
		logger.Warnf("aegisv2 /collect failed to build traces, ip=%v, error: %s", ip, err)
		return define.StatusBadRequest, err
	}
	if code, err := s.publishRecord(ip, start, tk, metadata, bs, define.RecordTraces, traces); err != nil {
		return code, err
	}

	if len(collector.data) == 0 {
		return define.StatusCodeOK, nil
	}
	metrics := pmetric.NewMetrics()
	rm := metrics.ResourceMetrics().AppendEmpty()
	resourceAttrs := rm.Resource().Attributes()
	sessionID := ""
	if len(records) > 0 {
		sessionID = records[0].Fields.Session.ID
	}
	putCommonResourceAttrs(resourceAttrs, payload, sessionID)
	upsertString(resourceAttrs, "referer", payload.Bean.Referer)
	sm := rm.ScopeMetrics().AppendEmpty()
	putCollectorScope(sm.Scope(), payload.Bean.Version)
	if err := collector.export(sm); err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordMetrics)
		logger.Warnf("aegisv2 /collect failed to derive metrics, ip=%v, error: %s", ip, err)
		return define.StatusCodeOK, nil
	}
	if code, err := s.publishRecord(ip, time.Now(), tk, metadata, bs, define.RecordMetrics, metrics); err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordMetrics)
		logger.Warnf("aegisv2 /collect failed to publish derived metrics, ip=%v, code=%d, error: %s", ip, code, err)
	}
	return define.StatusCodeOK, nil
}

func (s *httpService) Whitelist(w http.ResponseWriter, req *http.Request) {
	serverTime := time.Now().UnixMilli()

	resp := whitelistResponse{
		Code:            0,
		Msg:             "success",
		IsInWhiteList:   0,
		SampleMap:       whitelistSample,
		ServerTime:      serverTime,
		StartServerTime: serverTime,
	}
	b, err := json.Marshal(resp)
	if err != nil {
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, fallbackMsg)
		return
	}
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, b)
}
