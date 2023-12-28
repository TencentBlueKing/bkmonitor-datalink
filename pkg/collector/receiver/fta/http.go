// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fta

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

const (
	routeFtaEvent            = "/fta/v1/event"
	routeFtaEventSlash       = "/fta/v1/event/"
	routeFtaEventPlugin      = "/fta/v1/event/{pluginId}"
	routeFtaEventPluginSlash = "/fta/v1/event/{pluginId}/"

	ftaTokenKey    = "X-BK-FTA-TOKEN"
	tokenKey       = "X-BK-TOKEN"
	tokenParamsKey = "token"
	statusError    = "error"
)

func init() {
	receiver.RegisterReadyFunc(define.SourceFta, Ready)
}

func Ready() {
	receiver.RegisterHttpRoute(define.SourceFta, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeFtaEvent,
			HandlerFunc:  httpSvc.ExportEvent,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeFtaEventSlash,
			HandlerFunc:  httpSvc.ExportEvent,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeFtaEventPlugin,
			HandlerFunc:  httpSvc.ExportEvent,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeFtaEventPluginSlash,
			HandlerFunc:  httpSvc.ExportEvent,
		},
	})
}

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceFta)

type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

type response struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}

func (s HttpService) getResponse(status, err string) []byte {
	bs, _ := json.Marshal(response{Status: status, Error: err})
	return bs
}

var httpSvc HttpService

func (s HttpService) ExportEvent(w http.ResponseWriter, req *http.Request) {
	start := time.Now()

	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr)

	// 从请求中获取pluginId
	pluginId := mux.Vars(req)["pluginId"]

	// 从请求头中获取token
	token := req.Header.Get(tokenKey)
	if token == "" {
		token = req.Header.Get(ftaTokenKey)
	}
	if token == "" {
		token = req.URL.Query().Get(tokenParamsKey)
	}
	if token == "" {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordFta)
		resp := s.getResponse(statusError, "token is empty")
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusForbidden, resp)
		return
	}

	// 从请求中获取数据
	buf, err := io.ReadAll(req.Body)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordFta)
		resp := s.getResponse(statusError, err.Error())
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, resp)
		return
	}

	// 将数据转换为map
	var data map[string]interface{}
	err = json.Unmarshal(buf, &data)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordFta)
		resp := s.getResponse(statusError, err.Error())
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, resp)
		return
	}

	// 将headers放入data中
	httpHeaders := make(map[string]string)
	for k, v := range req.Header {
		if len(v) != 0 && strings.ToUpper(k) != tokenKey && strings.ToUpper(k) != ftaTokenKey {
			httpHeaders[k] = v[0]
		}
	}
	if len(httpHeaders) != 0 {
		data["__http_headers__"] = httpHeaders
	}

	// 将查询参数放入data中
	httpQueryParams := make(map[string]string)
	for k, v := range req.URL.Query() {
		if len(v) != 0 && k != tokenParamsKey {
			httpQueryParams[k] = v[0]
		}
	}
	if len(httpQueryParams) != 0 {
		data["__http_query_params__"] = httpQueryParams
	}

	event := &define.FtaData{
		PluginId:   pluginId,
		IngestTime: time.Now().Unix(),
		Data:       []map[string]interface{}{data},
		EventId:    uuid.New().String(),
	}

	r := &define.Record{
		RecordType:    define.RecordFta,
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		Token:         define.Token{Original: token, AppName: pluginId},
		Data:          event,
	}

	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "failed to validate record, code: %d, processor: %s", code, processorName)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordFta, processorName, r.Token.Original, code)
		resp := s.getResponse(statusError, err.Error())
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, resp)
		return
	}

	s.Publish(r)

	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordMetrics, len(buf), start)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte(`{"status": "success"}`))
}
