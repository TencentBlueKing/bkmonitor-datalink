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
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/throttle"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/converter"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/parser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	routeV2Rum    = "/api/v2/rum"
	routeV1Rum    = "/v1/rum"
	routeV2Replay = "/api/v2/replay"

	// 默认写死，后期再扩展为配置项。
	maxRequestBodyBytes   = 10 << 20 // 10MB
	maxDecodedEventsBytes = 10 << 20 // 10MB
	skipInvalid           = false
)

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceDatadogRum)

var (
	readyMu          sync.Mutex
	routesRegistered bool
)

// HttpService is the Datadog RUM HTTP receiving service.
type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc HttpService

func init() {
	receiver.RegisterReadyFunc(define.SourceDatadogRum, Ready)
	throttle.RegisterHTTPRecordType(routeV2Rum, define.RecordRum)
	throttle.RegisterHTTPRecordType(routeV1Rum, define.RecordRum)
}

// Factory returns the init function used by the upper layer.
func Factory() func() {
	return Ready
}

// Ready registers the Datadog RUM HTTP route during receiver startup.
func Ready() {
	readyMu.Lock()
	defer readyMu.Unlock()
	if routesRegistered {
		return
	}

	receiver.RegisterRecvHttpRoute(define.SourceDatadogRum, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeV2Rum,
			HandlerFunc:  httpSvc.Export,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeV1Rum,
			HandlerFunc:  httpSvc.Export,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeV2Replay,
			HandlerFunc:  httpSvc.Replay,
		},
	})
	routesRegistered = true
}

// Export handles Datadog RUM HTTP upload requests.
func (s HttpService) Export(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	defer closeRequestBody(req)

	clientIP := utils.ParseRequestIP(req.RemoteAddr, req.Header)
	startTime := time.Now()

	rawEvents, bodySize, err := decodeRequestEvents(req)
	if err != nil {
		writeBadRequest(w, clientIP, "failed to decode datadog rum request", err)
		return
	}

	batch, err := parseRawEvents(rawEvents)
	if err != nil {
		writeBadRequest(w, clientIP, "failed to parse datadog rum events", err)
		return
	}

	record := newRecord(req, clientIP, batch)
	if err := s.validateRecord(w, record, clientIP); err != nil {
		return
	}

	s.Publish(record)
	receiver.RecordHandleMetrics(metricMonitor, record.Token, define.RequestHttp, define.RecordRum, bodySize, startTime)
	receiver.WriteResponse(w, define.ContentTypeText, http.StatusOK, nil)
}

// Replay handles Datadog session replay upload requests.
// 当前为占位实现，不处理任何逻辑，直接返回空响应。
func (s HttpService) Replay(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	defer closeRequestBody(req)

	receiver.WriteResponse(w, define.ContentTypeText, http.StatusOK, nil)
}

func (s HttpService) validateRecord(w http.ResponseWriter, record *define.Record, clientIP string) error {
	code, processorName, err := s.Validate(record)
	if err == nil {
		return nil
	}

	logger.Warnf("run pre-check failed, rtype=%s, code=%d, ip=%v, error: %s", define.RecordRum.S(), code, clientIP, err)
	receiver.WriteErrResponse(w, define.ContentTypeText, int(code), err)
	metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordRum, processorName, record.Token.Original, code)
	return err
}

func closeRequestBody(req *http.Request) {
	if req == nil || req.Body == nil {
		return
	}
	_ = req.Body.Close()
}

func decodeRequestEvents(req *http.Request) ([][]byte, int, error) {
	buf, err := decoder.ReadBody(req, maxRequestBodyBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("read body: %w", err)
	}

	rawEvents, err := decoder.DecodeEvents(buf.Bytes(), maxDecodedEventsBytes)
	if err != nil {
		return nil, 0, fmt.Errorf("decode payload: %w", err)
	}
	return rawEvents, buf.Len(), nil
}

// parseRawEvents validates and converts raw payloads into a RUM batch.
func parseRawEvents(rawEvents [][]byte) (*model.Batch, error) {
	p := parser.New(skipInvalid)
	return p.ParseBatch(rawEvents)
}

// newRecord creates the pipeline record for a parsed RUM batch.
func newRecord(req *http.Request, ip string, batch *model.Batch) *define.Record {
	query := req.URL.Query()
	token := tokenparser.FromHttpRequest(req)
	if token == "" {
		token = query.Get("dd-api-key")
	}
	userAgent := req.Header.Get("user-agent")
	metadata := tokenparser.FromHttpUserMetadata(req)
	if userAgent != "" {
		if metadata == nil {
			metadata = make(map[string]string)
		}
		metadata["user-agent"] = userAgent
	}
	return &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordRum,
		Data:          converter.Convert(batch, userAgent),
		Token:         define.Token{Original: token},
		Metadata:      metadata,
	}
}

// writeBadRequest records and logs a malformed request before returning HTTP 400.
func writeBadRequest(w http.ResponseWriter, ip string, msg string, err error) {
	metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordRum)
	receiver.WriteResponse(w, define.ContentTypeText, http.StatusBadRequest, nil)
	logger.Errorf("%s, ip=%v, error: %s", msg, ip, err)
}
