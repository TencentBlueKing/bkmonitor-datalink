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
	"strings"
	"time"

	"github.com/pkg/errors"
	"google.golang.org/grpc"
	confv3 "skywalking.apache.org/repo/goapi/collect/agent/configuration/v3"
	eventv3 "skywalking.apache.org/repo/goapi/collect/event/v3"
	agentv3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"
	profilev3 "skywalking.apache.org/repo/goapi/collect/language/profile/v3"
	managementv3 "skywalking.apache.org/repo/goapi/collect/management/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc HttpService

const (
	splitKey        = "_"
	routeV3Segment  = "/v3/segment"  // segment 上报单一 trace
	routeV3Segments = "/v3/segments" // segments 上报多条 traces
)

func init() {
	receiver.RegisterReadyFunc(define.SourceSkywalking, Ready)
}

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceSkywalking)

func Ready() {
	receiver.RegisterRecvHttpRoute(define.SourceSkywalking, []receiver.RouteWithFunc{
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

	receiver.RegisterRecvGrpcRoute(func(s *grpc.Server) {
		confv3.RegisterConfigurationDiscoveryServiceServer(s, &ConfigurationDiscoveryService{})
		eventv3.RegisterEventServiceServer(s, &EventService{})
		managementv3.RegisterManagementServiceServer(s, &ManagementService{})
		agentv3.RegisterTraceSegmentReportServiceServer(s, &TraceSegmentReportService{})
		agentv3.RegisterJVMMetricReportServiceServer(s, &JVMMetricReportService{})
		agentv3.RegisterMeterReportServiceServer(s, &MeterService{})
		agentv3.RegisterCLRMetricReportServiceServer(s, &ClrService{})
		profilev3.RegisterProfileTaskServer(s, &ProfileService{})
	})
}

// extractMetadata 提取 token 与 instance
// HTTP 协议 SDK 实现无法方便设置 Header 因此从 serviceInstance 里面提取 token
// 分割符号 splitKey
func extractMetadata(s string) (token, serviceInstance string, err error) {
	parts := strings.SplitN(s, splitKey, 2) // token 不会携带 splitKey
	if len(parts) != 2 {
		return "", "", errors.Errorf("skywalking: invalid metadata '%s'", s)
	}
	token, serviceInstance = parts[0], parts[1]
	return token, serviceInstance, nil
}

func (s HttpService) reportV3Segment(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusInternalServerError, err)
		logger.Errorf("failed to read request body, error: %s", err)
		return
	}

	data := &agentv3.SegmentObject{}
	if err = json.Unmarshal(buf.Bytes(), data); err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, err)
		logger.Errorf("failed to unmarshal segment, error: %s", err)
		return
	}

	token, serviceInstance, err := extractMetadata(data.GetServiceInstance())
	if err != nil {
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, err)
		logger.Warnf("failed to extract metadata, ip=%v, error: %s", ip, err)
		return
	}

	data.ServiceInstance = serviceInstance
	traces := EncodeTraces(data, token, nil)
	r := &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordTraces,
		Data:          traces,
	}

	prettyprint.Pretty(define.RecordTraces, traces)
	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check failed, code=%d, ip=%s", code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		receiver.WriteErrResponse(w, define.ContentTypeJson, int(code), err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordTraces, processorName, r.Token.Original, code)
		return
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordTraces, buf.Len(), start)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte("OK"))
}

func (s HttpService) reportV3Segments(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusInternalServerError, err)
		logger.Errorf("failed to read request body, error: %s", err)
		return
	}

	var data []*agentv3.SegmentObject
	if err = json.Unmarshal(buf.Bytes(), &data); err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordTraces)
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, err)
		logger.Errorf("failed to unmarshal segments, error: %s", err)
		return
	}

	var traceToken define.Token
	for _, seg := range data {
		token, serviceInstance, err := extractMetadata(seg.GetServiceInstance())
		if err != nil {
			logger.Warnf("failed to extract metadata, ip=%v, error: %s", ip, err)
			continue
		}

		seg.ServiceInstance = serviceInstance
		traces := EncodeTraces(seg, token, nil)
		r := &define.Record{
			RequestType:   define.RequestHttp,
			RequestClient: define.RequestClient{IP: ip},
			RecordType:    define.RecordTraces,
			Data:          traces,
		}

		prettyprint.Traces(traces)
		code, processorName, err := s.Validate(r)
		if err != nil {
			err = errors.Wrapf(err, "run pre-check failed, code=%d, ip=%s", code, ip)
			logger.WarnRate(time.Minute, r.Token.Original, err)
			receiver.WriteErrResponse(w, define.ContentTypeJson, int(code), err)
			metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordTraces, processorName, r.Token.Original, code)
			return
		}
		s.Publish(r)
		traceToken = r.Token
	}
	receiver.RecordHandleMetrics(metricMonitor, traceToken, define.RequestHttp, define.RecordTraces, buf.Len(), start)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte("OK"))
}
