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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
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
	receiver.RegisterRecvHttpRoute(define.SourceJaeger, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeJaegerTraces,
			HandlerFunc:  httpSvc.JaegerTraces,
		},
	})

	receiver.RegisterRecvGrpcRoute(func(s *grpc.Server) {
		api_v2.RegisterCollectorServiceServer(s, GrpcService{})
	})
}

type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc HttpService

var acceptedFormats = map[string]Encoder{
	"application/x-thrift":                 newThriftV1Encoder(),
	"application/vnd.apache.thrift.binary": newThriftV1Encoder(),
}

func (s HttpService) JaegerTraces(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

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

	traces, httpCode, err := decodeHTTPBody(buf.Bytes(), req.Header.Get("Content-Type"))
	if err != nil {
		logger.Warnf("failed to parse jaeger exported content, ip=%v, err: %v", ip, err)
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteErrResponse(w, define.ContentTypeJson, httpCode, err)
		return
	}

	token := tokenparser.FromHttpRequest(req)
	r := &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordTraces,
		Data:          traces,
		Token:         define.Token{Original: token},
	}
	prettyprint.Traces(traces)

	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check failed, rtype=traces, code=%d, ip=%s", code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordTraces, processorName, r.Token.Original, code)
		receiver.WriteErrResponse(w, define.ContentTypeJson, int(code), err)
		return
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordTraces, buf.Len(), start)
}

func decodeHTTPBody(bs []byte, ctype string) (ptrace.Traces, int, error) {
	contentType, _, err := mime.ParseMediaType(ctype)
	if err != nil {
		return ptrace.Traces{}, http.StatusBadRequest, err
	}

	encoder, ok := acceptedFormats[contentType]
	if !ok {
		return ptrace.Traces{}, http.StatusBadRequest, errors.Errorf("unsupported content type: %v", contentType)
	}

	traces, err := encoder.UnmarshalTraces(bs)
	if err != nil {
		return ptrace.Traces{}, http.StatusBadRequest, errors.Wrap(err, "unmarshal request body failed")
	}

	return traces, http.StatusOK, nil
}
