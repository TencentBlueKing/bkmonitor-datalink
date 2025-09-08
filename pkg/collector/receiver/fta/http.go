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
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	routeFtaEvent      = "/fta/v1/event"
	routeFtaEventSlash = "/fta/v1/event/"

	tokenParamsKey = "token"
)

func init() {
	receiver.RegisterReadyFunc(define.SourceFta, Ready)
}

func Ready() {
	receiver.RegisterRecvHttpRoute(define.SourceFta, []receiver.RouteWithFunc{
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
	})
}

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceFta)

type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

func errResponse(err error) []byte {
	b, _ := json.Marshal(map[string]string{
		"status": "error",
		"error":  err.Error(),
	})
	return b
}

var httpSvc HttpService

func (s HttpService) ExportEvent(w http.ResponseWriter, req *http.Request) {
	start := time.Now()

	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	// 优先尝试从请求头中获取 token，取不到则中参数中获取
	token := req.Header.Get(define.KeyToken)
	if token == "" {
		token = req.URL.Query().Get(tokenParamsKey)
	}

	if token == "" {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordFta)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusForbidden, errResponse(errors.New("empty token")))
		logger.Warnf("no fta/token found, ip=%s", ip)
		return
	}

	// 从请求中获取数据
	buf, err := io.ReadAll(req.Body)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordFta)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, errResponse(err))
		logger.Errorf("failed to read request body, err: %v", err)
		return
	}

	// 将数据转换为 map
	var data map[string]any
	err = json.Unmarshal(buf, &data)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordFta)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, errResponse(err))
		logger.Errorf("failed to unmarshal request body, err: %v", err)
		return
	}

	// 将 headers 放入 data 中
	httpHeaders := make(map[string]string)
	for k, v := range req.Header {
		if len(v) != 0 && strings.ToUpper(k) != define.KeyToken {
			httpHeaders[k] = v[0]
		}
	}
	if len(httpHeaders) != 0 {
		data["__http_headers__"] = httpHeaders
	}

	// 将查询参数放入 data 中
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
		PluginId:   "",
		IngestTime: time.Now().Unix(),
		Data:       []map[string]any{data},
		EventId:    uuid.New().String(),
	}

	r := &define.Record{
		RecordType:    define.RecordFta,
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		Token:         define.Token{Original: token},
		Data:          event,
	}

	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check failed, rtype=fta, code=%d, ip=%s", code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordFta, processorName, r.Token.Original, code)
		receiver.WriteResponse(w, define.ContentTypeJson, int(code), errResponse(err))
		return
	}

	s.Publish(r)

	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordFta, len(buf), start)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte(`{"status": "success"}`))
}
