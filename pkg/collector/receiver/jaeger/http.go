// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jaeger

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/jaegertracing/jaeger/proto-gen/api_v2"
	"github.com/pkg/errors"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/grpc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	routeJaegerTraces = "/jaeger/v1/traces"
)

func init() {
	receiver.RegisterReadyFunc(define.SourceJaeger, Ready)
}

func Ready() {
	receiver.RegisterHttpRoute(define.SourceJaeger, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeJaegerTraces,
			HandlerFunc:  httpSvc.JaegerTraces,
		},
	})

	receiver.RegisterGrpcRoute(func(s *grpc.Server) {
		api_v2.RegisterCollectorServiceServer(s, GrpcService{})
	})
}

type HttpService struct {
	receiver.Publisher
	receiver.Validator
}

var httpSvc HttpService

var acceptedThriftFormats = map[string]struct{}{
	"application/x-thrift":                 {},
	"application/vnd.apache.thrift.binary": {},
}

func (s HttpService) JaegerTraces(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr)

	start := time.Now()
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, nil)
		logger.Errorf("failed to read jaeger body: %v", err)
		return
	}
	defer func() {
		_ = req.Body.Close()
	}()

	traces, httpCode, err := decodeThriftHTTPBody(buf.Bytes(), req.Header.Get("Content-Type"))
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteResponse(w, define.ContentTypeJson, httpCode, []byte(err.Error()))
		logger.Warnf("failed to parse jaeger exported content, error %s", err)
		return
	}

	r := &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordTraces,
		Data:          traces,
	}
	prettyprint.Traces(traces)

	code, processorName, err := s.Validate(r)
	if err != nil {
		logger.Warnf("failed to run pre-check processors, code=%d, ip=%v, error %s", code, ip, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordTraces, processorName, r.Token.Original, code)
		receiver.WriteResponse(w, define.ContentTypeJson, int(code), []byte(err.Error()))
		return
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordTraces, buf.Len(), start)
}

func decodeThriftHTTPBody(bs []byte, ctype string) (ptrace.Traces, int, error) {
	contentType, _, err := mime.ParseMediaType(ctype)
	if err != nil {
		return ptrace.Traces{}, http.StatusBadRequest, err
	}

	if _, ok := acceptedThriftFormats[contentType]; !ok {
		return ptrace.Traces{}, http.StatusBadRequest, errors.Errorf("unsupported content type: %v", contentType)
	}

	traces, err := newThriftV1Encoder().UnmarshalTraces(bs)
	if err != nil {
		return ptrace.Traces{}, http.StatusBadRequest, errors.Errorf("unable to process request body: %v", err)
	}

	return traces, http.StatusOK, nil
}
