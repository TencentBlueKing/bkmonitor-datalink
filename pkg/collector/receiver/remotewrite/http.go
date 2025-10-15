// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package remotewrite

import (
	"net/http"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	routeRemoteWrite = "/prometheus/write"
)

func init() {
	receiver.RegisterReadyFunc(define.SourceRemoteWrite, Ready)
}

func Ready() {
	receiver.RegisterRecvHttpRoute(define.SourceRemoteWrite, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeRemoteWrite,
			HandlerFunc:  httpSvc.Write,
		},
	})
}

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceRemoteWrite)

type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc HttpService

func (s HttpService) Write(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()

	token := tokenparser.FromHttpRequest(req)
	r := &define.Record{
		RecordType:    define.RecordRemoteWrite,
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		Token:         define.Token{Original: token},
	}
	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check failed, code=%d, ip=%s", code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		receiver.WriteErrResponse(w, define.ContentTypeText, int(code), err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordRemoteWrite, processorName, r.Token.Original, code)
		return
	}

	writeReq, size, err := utils.DecodeWriteRequest(req.Body)
	if err != nil {
		receiver.WriteErrResponse(w, define.ContentTypeText, http.StatusBadRequest, err)
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordRemoteWrite)
		logger.Warnf("failed to decode write request, code=%d, ip=%v, error: %s", code, ip, err)
		return
	}
	defer func() {
		_ = req.Body.Close()
	}()

	s.Publish(&define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordRemoteWrite,
		Token:         r.Token,
		Data: &define.RemoteWriteData{
			Timeseries: writeReq.Timeseries,
		},
	})
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordRemoteWrite, size, start)
	receiver.WriteResponse(w, define.ContentTypeText, http.StatusOK, nil)
}
