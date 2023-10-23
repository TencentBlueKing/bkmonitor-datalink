// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pyroscope

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	routePyroscopeIngest      = "/pyroscope/ingest"
	formFieldProfile          = "profile"
	formFileProfile           = "profile.pprof"
	formFieldPreviousProfile  = "prev_profile"
	formFilePreviousProfile   = "profile.pprof"
	formFieldSampleTypeConfig = "sample_type_config"
	formFileSampleTypeConfig  = "sample_type_config.json"
)

const (
	FormatPprof = "pprof"
	FormatJFR   = "jfr"
	// TODO: determine the format of c/c++ profiler or start a new receiver instead of pyroscope
)

const (
	GoSpy   = "gospy"
	JavaSpy = "jfr"
)

type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc HttpService

func init() {
	receiver.RegisterReadyFunc(define.SourcePyroscope, Ready)
}

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourcePyroscope)

func Ready() {
	receiver.RegisterHttpRoute(define.SourcePyroscope, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routePyroscopeIngest,
			HandlerFunc:  httpSvc.ProfilesIngest,
		},
	})
}

func getBearerToken(req *http.Request) string {
	token := strings.Split(req.Header.Get("Authorization"), "Bearer ")
	if len(token) < 2 {
		return ""
	}
	return token[1]
}

// ProfilesIngest 接收 pyroscope 上报的 profile 数据
func (s HttpService) ProfilesIngest(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr)
	start := time.Now()

	raw, err := io.ReadAll(req.Body)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		logger.Errorf("failed to get data, err: %s", err)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(err.Error()))
		return
	}

	spyName := req.URL.Query().Get("spyName")
	format := getFormatBySpy(spyName)
	if format == "" {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		errMsg := fmt.Sprintf("spyName is unknown, spyName: %s", spyName)
		logger.Error(errMsg)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(errMsg))
		return
	}
	logger.Debugf("format: %s profiles data received", format)
	// TODO: if format is not goSpy, we should convert it to pprof format
	token := getBearerToken(req)
	if token == "" {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		errMsg := "failed to get valid token from profiles ingestion"
		logger.Error(errMsg)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(errMsg))
		return
	}

	boundary, err := ParseBoundary(req.Header.Get("Content-Type"))
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordProfiles)
		logger.Errorf("failed to parse boundary, err: %s", err)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(err.Error()))
		return
	}

	f, err := multipart.NewReader(bytes.NewReader(raw), boundary).ReadForm(32 << 20)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordProfiles)
		logger.Errorf("failed to read form body, err: %s", err)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(err.Error()))
		return
	}
	defer func() {
		_ = f.RemoveAll()
	}()

	thisProfile, err := ReadField(f, formFieldProfile)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		logger.Errorf("read profile failed, err: %s", err)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(err.Error()))
		return
	}
	previousProfile, err := ReadField(f, formFieldPreviousProfile)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		logger.Errorf("read previous profile failed, err: %s", err)
		return
	}
	logger.Debugf("profiles got, previous len: %d, this len: %d \n", len(previousProfile), len(thisProfile))

	c, err := ReadField(f, formFieldSampleTypeConfig)
	if err != nil {
		logger.Warnf("failed to get sample type config, err: %s", err)
		// no need to return, because sample type config is optional
	}
	if c == nil {
		logger.Warnf("sample type config is empty")
	}
	// TODO: implement sample type config from user

	r := &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordProfiles,
		Data:          thisProfile,
		Token:         define.Token{Original: token},
	}
	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check failed, code=%d, ip=%s", code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		receiver.WriteResponse(w, define.ContentTypeJson, int(code), []byte(err.Error()))
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordProfiles, processorName, r.Token.Original, code)
		return
	}

	s.Publish(r)
	logger.Debugf("record published, ip=%s, token=%s", ip, r.Token.Original)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte("OK"))
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordProfiles, len(raw), start)
}

func getFormatBySpy(spyName string) string {
	switch spyName {
	case GoSpy:
		return FormatPprof
	case JavaSpy:
		return FormatJFR
	default:
		return ""
	}
}
