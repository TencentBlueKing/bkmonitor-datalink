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
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/proto"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	spb "google.golang.org/genproto/googleapis/rpc/status"

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
			HandlerFunc:  httpSvc.ExportTraces,
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

func (s *httpService) httpExport(w http.ResponseWriter, req *http.Request, rtype define.RecordType) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()
	bs, err := io.ReadAll(req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, rtype)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, nil)
		logger.Errorf("aegisv2 failed to read body content, rtype=%s, ip=%v, error: %s", rtype.S(), ip, err)
		return
	}

	// 接收到请求立即返回成功，后续处理异步进行
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte("{}"))

	contentType := req.Header.Get(define.ContentType)
	tk := tokenparser.FromHttpRequest(req)
	metadata := tokenparser.FromHttpUserMetadata(req)

	go func() {
		defer utils.HandleCrash()
		// traceID 仅 traces 类型使用，其他类型传零值即可。
		var traceID pcommon.TraceID
		if rtype == define.RecordTraces {
			traceID = random.TraceID()
		}
		rh := s.getResponseHandler(contentType, traceID)
		data, err := rh.Unmarshal(rtype, bs)
		if err != nil {
			metricMonitor.IncDroppedCounter(define.RequestHttp, rtype)
			logger.Warnf("aegisv2 failed to unmarshal body, rtype=%s, ip=%v, error: %s", rtype.S(), ip, err)
			return
		}

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
			return
		}

		s.Publish(r)
		receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, rtype, len(bs), start)
	}()
}

func (s *httpService) ExportTraces(w http.ResponseWriter, req *http.Request) {
	s.httpExport(w, req, define.RecordTraces)
}

func (s *httpService) Whitelist(w http.ResponseWriter, req *http.Request) {
	serverTime := time.Now().UnixMilli()

	// server_time 表示服务端收到 whitelist 请求的时间；
	// start_server_time
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

func (s *httpService) getResponseHandler(contentType string, traceID pcommon.TraceID) receiver.ResponseHandler {
	switch contentType {
	case define.ContentTypeProtobuf:
		return httpPbResponseHandler{encoder: PbEncoder()}
	}
	// 缺省解析器为 contentTypeJson
	return httpJsonResponseHandler{
		marshaler: &jsonpb.Marshaler{},
		encoder:   JsonEncoderWithTraceID(traceID),
	}
}

type httpPbResponseHandler struct {
	encoder Encoder
}

func (h httpPbResponseHandler) ContentType() string {
	return define.ContentTypeProtobuf
}

func (h httpPbResponseHandler) Response(rtype define.RecordType) ([]byte, error) {
	switch rtype {
	case define.RecordTraces:
		return ptraceotlp.NewResponse().MarshalProto()
	case define.RecordMetrics:
		return pmetricotlp.NewResponse().MarshalProto()
	case define.RecordLogs:
		return plogotlp.NewResponse().MarshalProto()
	}
	return nil, define.ErrUnknownRecordType
}

func (h httpPbResponseHandler) Unmarshal(rtype define.RecordType, b []byte) (any, error) {
	return unmarshalRecordData(h.encoder, rtype, b)
}

func (h httpPbResponseHandler) ErrorStatus(status any) ([]byte, error) {
	buf := new(bytes.Buffer)
	s, ok := status.(*spb.Status)
	if !ok {
		return buf.Bytes(), nil
	}
	return proto.Marshal(s)
}

type httpJsonResponseHandler struct {
	marshaler *jsonpb.Marshaler
	encoder   Encoder
}

func (h httpJsonResponseHandler) ContentType() string {
	return define.ContentTypeJson
}

func (h httpJsonResponseHandler) Response(rtype define.RecordType) ([]byte, error) {
	switch rtype {
	case define.RecordTraces:
		return ptraceotlp.NewResponse().MarshalJSON()
	case define.RecordMetrics:
		return pmetricotlp.NewResponse().MarshalJSON()
	case define.RecordLogs:
		return plogotlp.NewResponse().MarshalJSON()
	}
	return nil, define.ErrUnknownRecordType
}

func (h httpJsonResponseHandler) Unmarshal(rtype define.RecordType, b []byte) (any, error) {
	return unmarshalRecordData(h.encoder, rtype, b)
}

func (h httpJsonResponseHandler) ErrorStatus(status any) ([]byte, error) {
	buf := new(bytes.Buffer)
	s, ok := status.(*spb.Status)
	if !ok {
		return buf.Bytes(), nil
	}
	err := h.marshaler.Marshal(buf, s)
	return buf.Bytes(), err
}
