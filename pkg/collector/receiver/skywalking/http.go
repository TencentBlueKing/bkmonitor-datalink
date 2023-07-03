// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package skywalking

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"google.golang.org/grpc"
	conf "skywalking.apache.org/repo/goapi/collect/agent/configuration/v3"
	event "skywalking.apache.org/repo/goapi/collect/event/v3"
	segment "skywalking.apache.org/repo/goapi/collect/language/agent/v3"
	profile "skywalking.apache.org/repo/goapi/collect/language/profile/v3"
	management "skywalking.apache.org/repo/goapi/collect/management/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type HttpService struct {
	receiver.Publisher
	receiver.Validator
}

var httpSvc HttpService

const (
	tokenKey        = "X-BK-TOKEN"
	routeV3Segment  = "/v3/segment"  // segment 上报单一 trace
	routeV3Segments = "/v3/segments" // segments 上报多条 traces
)

func init() {
	receiver.RegisterReadyFunc(define.SourceSkywalking, Ready)
}

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceSkywalking)

func Ready() {
	receiver.RegisterHttpRoute(define.SourceSkywalking, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeV3Segment,
			HandlerFunc:  httpSvc.reportV3Segment,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeV3Segments,
			HandlerFunc:  httpSvc.reportV3Segments,
		},
	})

	receiver.RegisterGrpcRoute(func(s *grpc.Server) {
		conf.RegisterConfigurationDiscoveryServiceServer(s, &ConfigurationDiscoveryService{})
		event.RegisterEventServiceServer(s, &EventService{})
		management.RegisterManagementServiceServer(s, &ManagementService{})
		segment.RegisterTraceSegmentReportServiceServer(s, &TraceSegmentReportService{})
		segment.RegisterJVMMetricReportServiceServer(s, &JVMMetricReportService{})
		segment.RegisterMeterReportServiceServer(s, &MeterService{})
		segment.RegisterCLRMetricReportServiceServer(s, &ClrService{})
		profile.RegisterProfileTaskServer(s, &ProfileService{})
	})
}

func (s HttpService) reportV3Segment(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr)

	start := time.Now()

	token := req.Header.Get(tokenKey)
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, []byte(err.Error()))
		logger.Errorf("failed to read skywalking HTTP request body, error: %s", err)
		return
	}

	data := &segment.SegmentObject{}
	if err = json.Unmarshal(buf.Bytes(), data); err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(err.Error()))
		logger.Errorf("failed to unmarshal skywalking segment, error: %s", err)
		return
	}

	traces := EncodeTraces(data, token)
	r := &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordTraces,
		Data:          traces,
	}

	prettyprint.Pretty(define.RecordTraces, traces)
	code, processorName, err := s.Validate(r)
	if err != nil {
		receiver.WriteResponse(w, define.ContentTypeJson, int(code), []byte(err.Error()))
		logger.Warnf("failed to run pre-check processors, code=%d, ip=%v, error %s", code, ip, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordTraces, processorName, r.Token.Original, code)
		return
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordTraces, buf.Len(), start)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte("OK"))
}

func (s HttpService) reportV3Segments(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr)

	start := time.Now()
	token := req.Header.Get(tokenKey)
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, []byte(err.Error()))
		logger.Errorf("failed to read skywalking HTTP request body, error: %s", err)
		return
	}

	var data []*segment.SegmentObject
	if err = json.Unmarshal(buf.Bytes(), &data); err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(err.Error()))
		logger.Errorf("failed to unmarshal skywalking segments, error: %s", err)
		return
	}

	var traceToken define.Token
	for _, seg := range data {
		traces := EncodeTraces(seg, token)
		r := &define.Record{
			RequestType:   define.RequestHttp,
			RequestClient: define.RequestClient{IP: ip},
			RecordType:    define.RecordTraces,
			Data:          traces,
		}

		prettyprint.Pretty(define.RecordTraces, traces)
		code, processorName, err := s.Validate(r)
		if err != nil {
			receiver.WriteResponse(w, define.ContentTypeJson, int(code), []byte(err.Error()))
			logger.Warnf("failed to run pre-check processors, code=%d, ip=%v, error: %s", code, ip, err)
			metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordTraces, processorName, r.Token.Original, code)
			return
		}

		s.Publish(r)
		traceToken = r.Token
	}

	receiver.RecordHandleMetrics(metricMonitor, traceToken, define.RequestHttp, define.RecordTraces, buf.Len(), start)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte("OK"))
}
