// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datadogrum

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/pkg/errors"
)

const (
	routeRumV1  = "/api/v2/rum"
	routeRumV2  = "/api/v2/rum/events"
	routeReplay = "/api/v2/replay"
)

func init() {
	receiver.RegisterReadyFunc(define.SourceDatadog, Ready)
}

var (
	metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceDatadog)
	otelConverter = NewConverter()
)

func Ready() {
	receiver.RegisterRecvHttpRoute(define.SourceDatadog, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeRumV1,
			HandlerFunc:  httpSvc.RumV1,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeRumV2,
			HandlerFunc:  httpSvc.RumV2,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeReplay,
			HandlerFunc:  httpSvc.RumV2,
		},
	})
}

type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

type convertedRecord struct {
	rtype define.RecordType
	data  interface{}
}

var httpSvc HttpService

// bodyBufPool 复用 HTTP body 读取缓冲区，降低高并发时的 GC 压力。
var bodyBufPool = sync.Pool{
	New: func() interface{} { return bytes.NewBuffer(make([]byte, 0, 32*1024)) },
}

// transformRecord 将 DatadogEventV2 转换为 OTEL 格式。
func transformRecord(event *DatadogEventV2) ConversionOutput {
	return otelConverter.ToOTEL(event)
}

// splitConversionResult 按信号类型拆分转换结果。
func splitConversionResult(result ConversionOutput) []convertedRecord {
	records := make([]convertedRecord, 0, 3)

	if result.Logs.LogRecordCount() > 0 {
		records = append(records, convertedRecord{rtype: define.RecordLogs, data: result.Logs})
	}
	if result.Traces.SpanCount() > 0 {
		records = append(records, convertedRecord{rtype: define.RecordTraces, data: result.Traces})
	}
	if result.Metrics.MetricCount() > 0 {
		records = append(records, convertedRecord{rtype: define.RecordMetrics, data: result.Metrics})
	}

	return records
}

// publishConvertedRecords 按 conversionResult 分流发布 logs、traces 和 metrics。
func (s HttpService) publishConvertedRecords(conversionResult ConversionOutput, ip string, token string, bodySize int, start time.Time) {
	logger.Debugf(
		"Converted pdata result: logs=%d spans=%d metrics=%d",
		conversionResult.Logs.LogRecordCount(),
		conversionResult.Traces.SpanCount(),
		conversionResult.Metrics.MetricCount(),
	)

	for _, item := range splitConversionResult(conversionResult) {
		r := &define.Record{
			RequestType:   define.RequestHttp,
			RequestClient: define.RequestClient{IP: ip},
			RecordType:    item.rtype,
			Data:          item.data,
			Token:         define.Token{Original: token},
		}

		code, processorName, err := s.Validate(r)
		if err != nil {
			err = errors.Wrapf(err, "run pre-check failed, rtype=%s, code=%d, ip=%s", item.rtype.S(), code, ip)
			logger.WarnRate(time.Minute, r.Token.Original, err)
			metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, item.rtype, processorName, r.Token.Original, code)
			continue
		}

		s.Publish(r)
		receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, item.rtype, bodySize, start)
	}
}

func (s HttpService) RumV1(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()
	buf := bodyBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bodyBufPool.Put(buf)

	_, err := io.Copy(buf, req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordLogs)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, nil)
		logger.Errorf("failed to read datadog rum body: %v", err)
		return
	}
	defer func() {
		_ = req.Body.Close()
	}()

	dataBytes := buf.Bytes()
	logger.Debugf("RumV1: received %d bytes", len(dataBytes))

	records, err := parseDatadogRUM(dataBytes)
	if err != nil {
		logger.Warnf("failed to parse datadog rum exported content, ip=%v, err: %v", ip, err)
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordLogs)
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, err)
		return
	}

	token := tokenparser.FromHttpRequest(req)

	for idx, event := range records {
		logger.Debugf("RumV1: processing record %d, type=%s", idx, event.Type)
		conversionResult := transformRecord(event)
		s.publishConvertedRecords(conversionResult, ip, token, buf.Len(), start)
	}

	ddRequestID := req.URL.Query().Get("dd-request-id")
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte(fmt.Sprintf(`{"request_id":%q}`, ddRequestID)))
}

func (s HttpService) RumV2(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()
	buf := bodyBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	defer bodyBufPool.Put(buf)

	_, err := io.Copy(buf, req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordLogs)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, nil)
		logger.Errorf("failed to read datadog rum v2 body: %v", err)
		return
	}
	defer func() {
		_ = req.Body.Close()
	}()

	dataBytes := buf.Bytes()
	logger.Debugf("RumV2: received %d bytes", len(dataBytes))

	records, err := parseDatadogRUMV2(dataBytes)
	if err != nil {
		logger.Warnf("failed to parse datadog rum v2 exported content, ip=%v, err: %v", ip, err)
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordLogs)
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, err)
		return
	}

	token := tokenparser.FromHttpRequest(req)

	for idx, event := range records {
		logger.Debugf("RumV2: processing record %d, type=%s", idx, event.Type)
		conversionResult := transformRecord(event)
		s.publishConvertedRecords(conversionResult, ip, token, buf.Len(), start)
	}

	ddRequestID := req.URL.Query().Get("dd-request-id")
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte(fmt.Sprintf(`{"request_id":%q}`, ddRequestID)))
}
