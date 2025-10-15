// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package beat

import (
	"bytes"
	"io"
	"net/http"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	routeV1Beat = "/v1/beat"
)

func init() {
	receiver.RegisterReadyFunc(define.SourceBeat, Ready)
}

func Ready() {
	receiver.RegisterRecvHttpRoute(define.SourceBeat, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeV1Beat,
			HandlerFunc:  httpSvc.Export,
		},
	})
}

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceBeat)

type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc HttpService

func (s HttpService) Export(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, req.Body)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordBeat)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusInternalServerError, nil)
		logger.Errorf("failed to read body content, ip=%v, error: %s", ip, err)
		return
	}
	defer func() {
		_ = req.Body.Close()
	}()

	r := &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordBeat,
		Data:          &define.BeatData{Data: buf.Bytes()},
		Token:         define.Token{Original: req.Header.Get(define.KeyDataID)},
	}

	code, processorName, err := s.Validate(r)
	if err != nil {
		logger.Warnf("run pre-check failed, rtype=%s, code=%d, ip=%v, error: %s", define.RecordBeat.S(), code, ip, err)
		receiver.WriteErrResponse(w, define.ContentTypeJson, int(code), err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordBeat, processorName, r.Token.Original, code)
		return
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordBeat, buf.Len(), start)
	receiver.WriteResponse(w, define.ContentTypeText, http.StatusOK, nil)
}
