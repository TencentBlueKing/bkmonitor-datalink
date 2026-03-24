// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package otlp

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/proto"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	routeV1Traces  = "/v1/traces"
	routeV1Trace   = "/v1/trace"
	routeV1Metrics = "/v1/metrics"
	routeV1Logs    = "/v1/logs"
)

func init() {
	receiver.RegisterReadyFunc(define.SourceOtlp, Ready)
}

func Ready() {
	receiver.RegisterRecvHttpRoute(define.SourceOtlp, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeV1Traces,
			HandlerFunc:  httpSvc.ExportTraces,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeV1Trace,
			HandlerFunc:  httpSvc.ExportTraces,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeV1Metrics,
			HandlerFunc:  httpSvc.ExportMetrics,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeV1Logs,
			HandlerFunc:  httpSvc.ExportLogs,
		},
	})

	receiver.RegisterRecvGrpcRoute(func(s *grpc.Server) {
		ptraceotlp.RegisterServer(s, grpcSvc.traces)
		pmetricotlp.RegisterServer(s, grpcSvc.metrics)
		plogotlp.RegisterServer(s, grpcSvc.logs)
	})
}

type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc HttpService

var fallbackMsg = []byte(`{"code": 13, "message": "failed to marshal error message"}`)

func writeError(w http.ResponseWriter, rh receiver.ResponseHandler, err error, statusCode int) {
	s, ok := status.FromError(err)
	if !ok {
		s = status.New(codes.Unknown, err.Error())
		if statusCode == http.StatusBadRequest {
			s = status.New(codes.InvalidArgument, err.Error())
		}
	}

	msg, err := rh.ErrorStatus(s.Proto())
	if err != nil {
		receiver.WriteResponse(w, rh.ContentType(), http.StatusInternalServerError, fallbackMsg)
		return
	}
	receiver.WriteResponse(w, rh.ContentType(), statusCode, msg)
}

func (s HttpService) httpExport(w http.ResponseWriter, req *http.Request, rtype define.RecordType) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, rtype)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, nil)
		logger.Errorf("failed to read body content, rtype=%s, ip=%v, error: %s", rtype.S(), ip, err)
		return
	}
	defer func() {
		_ = req.Body.Close()
	}()

	rh := s.getResponseHandler(req.Header.Get(define.ContentType))
	data, err := rh.Unmarshal(rtype, buf.Bytes())
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, rtype)
		writeError(w, rh, err, http.StatusBadRequest)
		logger.Warnf("failed to unmarshal body, rtype=%s, ip=%v, error: %s", rtype.S(), ip, err)
		return
	}

	r := &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    rtype,
		Data:          data,
	}

	tk := tokenparser.FromHttpRequest(req)
	if len(tk) > 0 {
		r.Token = define.Token{Original: tk}
	}
	r.Metadata = tokenparser.FromHttpUserMetadata(req)

	prettyprint.Pretty(rtype, data)

	code, processorName, err := s.Validate(r)
	if err != nil {
		writeError(w, rh, err, int(code))
		logger.Warnf("run pre-check failed, rtype=%s, code=%d, ip=%v, error: %s", rtype.S(), code, ip, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, rtype, processorName, r.Token.Original, code)
		return
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, rtype, buf.Len(), start)

	msg, err := rh.Response(rtype)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, rtype)
		writeError(w, rh, err, http.StatusInternalServerError)
		logger.Errorf("failed to unmarshal response, error: %s", err)
		return
	}
	receiver.WriteResponse(w, rh.ContentType(), http.StatusOK, msg)
}

func (s HttpService) ExportTraces(w http.ResponseWriter, req *http.Request) {
	s.httpExport(w, req, define.RecordTraces)
}

func (s HttpService) ExportMetrics(w http.ResponseWriter, req *http.Request) {
	s.httpExport(w, req, define.RecordMetrics)
}

func (s HttpService) ExportLogs(w http.ResponseWriter, req *http.Request) {
	s.httpExport(w, req, define.RecordLogs)
}

func (s HttpService) getResponseHandler(contentType string) receiver.ResponseHandler {
	switch contentType {
	case define.ContentTypeProtobuf:
		return HttpPbResponseHandler()
	}
	// 缺省解析器为 contentTypeJson
	return HttpJsonResponseHandler()
}

// HttpPbResponseHandler HTTP 协议 Protobuf 类型相应处理器
func HttpPbResponseHandler() receiver.ResponseHandler {
	return httpPbResponseHandler{
		encoder: PbEncoder(),
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

// HttpJson Response Handler

func HttpJsonResponseHandler() receiver.ResponseHandler {
	return httpJsonResponseHandler{
		marshaler: &jsonpb.Marshaler{},
		encoder:   JsonEncoder(),
	}
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
