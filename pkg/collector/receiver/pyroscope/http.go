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
	"net/url"
	"strconv"
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
	formFieldJFR              = "jfr"
	formFieldPreviousProfile  = "prev_profile"
	formFieldSampleTypeConfig = "sample_type_config"
)

const (
	GoSpy   = "gospy"
	JavaSpy = "javaspy"
	PerfSpy = "perf_script"
)

// TagServiceName 需要忽略的服务 Tag 名称
var ignoredTagNames = []string{"__session_id__"}

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
	receiver.RegisterRecvHttpRoute(define.SourcePyroscope, []receiver.RouteWithFunc{
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

func parseForm(req *http.Request, body []byte) (*multipart.Form, error) {
	boundary, err := ParseBoundary(req.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}

	return multipart.NewReader(bytes.NewReader(body), boundary).ReadForm(32 << 20)
}

func getTimeFromUnixParam(req *http.Request, name string) (time.Time, error) {
	timeUnix, err := strconv.ParseInt(req.URL.Query().Get(name), 10, 64)
	if err != nil {
		return time.Time{}, err
	}

	return time.Unix(timeUnix, 0), nil
}

// ProfilesIngest 接收 pyroscope 上报的 profile 数据
func (s HttpService) ProfilesIngest(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr)
	start := time.Now()

	b, err := copyBody(req)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		logger.Errorf("failed to get data, err: %s", err)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(err.Error()))
		return
	}

	startSt, startStErr := getTimeFromUnixParam(req, "from")
	endSt, endStErr := getTimeFromUnixParam(req, "until")
	if startStErr != nil || endStErr != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		errMsg := fmt.Sprintf("failed to parse start or end time, err: %s", err)
		logger.Error(errMsg)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(errMsg))
		return
	}

	aggregationType := req.URL.Query().Get("aggregationType")
	units := req.URL.Query().Get("units")
	spyName := req.URL.Query().Get("spyName")
	format := req.URL.Query().Get("format")
	if format == "" {
		format = getFormatBySpy(spyName)
	}
	if format == "" {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		errMsg := fmt.Sprintf("spy: %s data is not supported, may be supported in the future :)", spyName)
		logger.Error(errMsg)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(errMsg))
		return
	}

	token := getBearerToken(req)
	if token == "" {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		errMsg := fmt.Sprintf("failed to get token in profiles ingestion from %s", ip)
		logger.Error(errMsg)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(errMsg))
		return
	}

	f, err := parseForm(req, b)
	if err != nil {
		metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordProfiles)
		logger.Errorf("failed to parse boundary, err: %s, token: %s", err, token)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(err.Error()))
		return
	}
	defer func() {
		_ = f.RemoveAll()
	}()

	// TODO 处理 prev_profile 字段
	origin, err := convertToOrigin(spyName, f)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		logger.Errorf("read profile failed from %s, err: %s, token: %s", ip, err, token)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, []byte(err.Error()))
		return
	}
	// TODO: handle SampleTypeConfig
	appName, tags := getApplicationNameAndTags(req)
	rawProfile := define.ProfilesRawData{
		Data: origin,
		Metadata: define.ProfileMetadata{
			StartTime:       startSt,
			EndTime:         endSt,
			SpyName:         spyName,
			Format:          format,
			AggregationType: aggregationType,
			Units:           units,
			Tags:            tags,
			AppName:         appName,
		},
	}

	r := &define.Record{
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordProfiles,
		Data:          rawProfile,
		Token:         define.Token{Original: token},
	}
	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check %s failed, code=%d, ip=%s", processorName, code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		receiver.WriteResponse(w, define.ContentTypeJson, int(code), []byte(err.Error()))
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordProfiles, processorName, r.Token.Original, code)
		return
	}

	s.Publish(r)
	logger.Debugf("record published, ip=%s, token=%s", ip, r.Token.Original)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte("OK"))
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordProfiles, len(b), start)
}

func getFormatBySpy(spyName string) string {
	switch spyName {
	case GoSpy:
		return define.FormatPprof
	case JavaSpy:
		return define.FormatJFR
	// TODO 暂不支持 PerfScript
	// case PerfSpy:
	//	return define.FormatPerfScript
	default:
		return ""
	}
}

func copyBody(r *http.Request) ([]byte, error) {
	buf := bytes.NewBuffer(make([]byte, 0, 64<<10))
	if _, err := io.Copy(buf, r.Body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// convertToOrigin 将 Http.Body 转换为 translator 所需的数据格式
func convertToOrigin(spyName string, form *multipart.Form) (any, error) {
	switch spyName {
	case JavaSpy:
		dataProfile, err := ReadField(form, "jfr")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read jfr field from form of spyName: %s", spyName)
		}
		labelsProfile, err := ReadField(form, "labels")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read labels field from form of spyName: %s", spyName)
		}
		logger.Debugf("receive jfr profile data, len: %d, labels len: %d", len(dataProfile), len(labelsProfile))
		return define.ProfileJfrFormatOrigin{Jfr: dataProfile, Labels: labelsProfile}, nil

	default:
		dataProfile, err := ReadField(form, "profile")
		if err != nil {
			return nil, errors.Wrapf(err, "failed to read profile field: from form of spyName: %s", spyName)
		}
		logger.Debugf("receive spyName: %s profile data, len: %d", spyName, len(dataProfile))
		return define.ProfilePprofFormatOrigin(dataProfile), nil
	}
}

// getApplicationNameAndTags 获取 url 中的 tags 信息
// example: name = appName{key1=value1,key2=value2}
func getApplicationNameAndTags(req *http.Request) (string, map[string]string) {
	reportTags := make(map[string]string)

	valueDecoded, err := url.QueryUnescape(req.URL.Query().Get("name"))
	if valueDecoded == "" {
		return "", reportTags
	}
	if err != nil {
		logger.Warnf("failed to parse query of params: name, error: %s", err)
		return "", reportTags
	}

	parts := strings.SplitN(valueDecoded, "{", 2)

	if len(parts) > 1 {
		pairs := strings.Split(strings.TrimRight(parts[1], "}"), ",")

		for _, pair := range pairs {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 {
				if !contains(ignoredTagNames, kv[0]) {
					reportTags[kv[0]] = kv[1]
				}
			}
		}
	} else {
		return "", reportTags
	}

	return parts[0], reportTags
}

func contains[S ~[]E, E comparable](s S, v E) bool {
	return index(s, v) >= 0
}

func index[S ~[]E, E comparable](s S, v E) int {
	for i := range s {
		if v == s[i] {
			return i
		}
	}
	return -1
}
