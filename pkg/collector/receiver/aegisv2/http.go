// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegisv2

import (
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// 路由、响应与采集相关的字面量集中在此，避免与 HTTP 业务逻辑混杂。
const (
	routeV2Collect        = "/collect"
	routeAegisV2Whitelist = "/aegiscontrol/whitelist"

	whitelistCodeSuccess       = 0
	whitelistMsgSuccess        = "success"
	whitelistCodeMarshalFailed = 13
	whitelistMsgMarshalFailed  = "failed to marshal error message"

	collectResponseEmpty = "{}"
	emptyPreview         = "<empty>"

	// headerTopicKey 是 Aegis SDK 开启 addXTopicKey 后携带应用标识的请求头。
	headerTopicKey = "X-Topic-Key"
)

func init() {
	receiver.RegisterReadyFunc(define.SourceAegisV2, Ready)
}

func Ready() {
	receiver.RegisterRecvHttpRoute(define.SourceAegisV2, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeV2Collect,
			HandlerFunc:  httpSvc.ExportCollect,
		},
		{
			Method:       http.MethodGet,
			RelativePath: routeAegisV2Whitelist,
			HandlerFunc:  httpSvc.Whitelist,
		},
	})

}

type httpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc httpService
var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceAegisV2)

func (s *httpService) ExportCollect(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()

	bs, err := io.ReadAll(req.Body)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordRumTraces)
		logger.Errorf("aegisv2 failed to read request body, ip=%v, error: %s", ip, err)
		receiver.WriteResponse(w, define.ContentTypeJson, int(define.StatusBadRequest), nil)
		return
	}

	traces, err := decodeCollect(req.Header.Get(define.ContentType), bs)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordRumTraces)
		logger.Warnf("aegisv2 /collect failed to decode body, ip=%v, error: %s, preview: %s", ip, err, getPayloadPreview(bs, 80))
		receiver.WriteResponse(w, define.ContentTypeJson, int(define.StatusBadRequest), nil)
		return
	}
	record := &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordRumTraces,
		Data:          traces,
		Token:         define.Token{Original: requestToken(req)},
		Metadata:      tokenparser.FromHttpUserMetadata(req),
	}
	prettyprint.Pretty(define.RecordRumTraces, traces)
	code, processorName, err := s.Validate(record)
	if err != nil {
		logger.Warnf("aegisv2 pre-check failed, rtype=%s, code=%d, ip=%v, error: %s", define.RecordRumTraces.S(), code, ip, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordRumTraces, processorName, record.Token.Original, code)
		receiver.WriteResponse(w, define.ContentTypeJson, int(code), nil)
		return
	}
	s.Publish(record)
	receiver.RecordHandleMetrics(metricMonitor, record.Token, define.RequestHttp, define.RecordRumTraces, len(bs), start)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte(collectResponseEmpty))
}

// requestToken 优先使用 tokenparser 识别的标准 token 请求头；Aegis SDK 开启
// addXTopicKey 时以 X-Topic-Key 上报应用标识，入口层未改写时用它回退，
// 否则 record.Token 为空会导致应用子配置无法命中，aes256 配置下直接 401。
func requestToken(req *http.Request) string {
	if token := tokenparser.FromHttpRequest(req); token != "" {
		return token
	}
	return strings.TrimSpace(req.Header.Get(headerTopicKey))
}

func getPayloadPreview(buf []byte, maxLen int) string {
	if len(buf) == 0 {
		return emptyPreview
	}
	if len(buf) <= maxLen {
		return escapeString(string(buf))
	}
	return escapeString(string(buf[:maxLen])) + "..."
}

func escapeString(s string) string {
	b := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 32 && c < 127 && c != '\\' && c != '"' {
			b = append(b, c)
		} else {
			switch c {
			case '\n':
				b = append(b, '\\', 'n')
			case '\r':
				b = append(b, '\\', 'r')
			case '\t':
				b = append(b, '\\', 't')
			default:
				b = append(b, '?')
			}
		}
	}
	return string(b)
}
