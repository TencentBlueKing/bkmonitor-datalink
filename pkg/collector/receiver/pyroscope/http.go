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

// HttpService 接收 pyroscope 上报的 profile 数据
type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc HttpService

func init() {
	receiver.RegisterReadyFunc(define.SourcePyroscope, Ready)
}

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourcePyroscope)

// Ready 注册 pyroscope 的 http 路由
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

func parseForm(req *http.Request) (*multipart.Form, error) {
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, err
	}

	boundary, err := ParseBoundary(req.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}

	f, err := multipart.NewReader(bytes.NewReader(raw), boundary).ReadForm(32 << 20)
	if err != nil {
		return nil, err
	}

	return f, nil
}

// ProfilesIngest 接收 pyroscope 上报的 profile 数据
func (s HttpService) ProfilesIngest(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr)
	start := time.Now()

	spyName := req.URL.Query().Get("spyName")
	format := getFormatBySpy(spyName)
	if format == "" {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		errMsg := fmt.Sprintf("spyName is unknown, spyName: %s", spyName)
		logger.Error(errMsg)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(errMsg))
		return
	}

	// TODO: if format is not goSpy, we should convert it to pprof format
	token := getBearerToken(req)
	if token == "" {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		errMsg := fmt.Sprintf("failed to get valid token in profiles ingestion from %s", ip)
		logger.Error(errMsg)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(errMsg))
		return
	}

	f, err := parseForm(req)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		errMsg := fmt.Sprintf("failed to parse form from %s, err: %s", ip, err)
		logger.Error(errMsg)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(errMsg))
		return
	}
	defer func() {
		_ = f.RemoveAll()
	}()

	thisProfile, err := ReadField(f, formFieldProfile)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		logger.Errorf("read profile failed from %s, err: %s", ip, err)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(err.Error()))
		return
	}
	// TODO: support previous profile

	logger.Debugf("profiles got, previous len: %d, this len: %d \n", len(thisProfile))

	// SampleTypeConfig is used to determine custom sample type in profile
	c, err := ReadField(f, formFieldSampleTypeConfig)
	if err != nil {
		logger.Warnf("failed to get sample type config from %s, err: %s", ip, err)
		// go on, because sample type config is optional now
	}
	if c == nil {
		// lacking sample type config is not a fatal error
		logger.Warnf("sample type config is empty")
	}

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
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordProfiles, len(thisProfile), start)
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
