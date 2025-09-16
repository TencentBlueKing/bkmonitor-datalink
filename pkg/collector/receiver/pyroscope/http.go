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
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/pkg/errors"
	"golang.org/x/exp/slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/maps"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	pushv1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/pyroscope/gen/proto/go/push/v1"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/pyroscope/gen/proto/go/push/v1/pushv1connect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	routePyroscopeIngest      = "/pyroscope/ingest"
	formFieldProfile          = "profile"
	formFieldJFR              = "jfr"
	formFieldPreviousProfile  = "prev_profile"
	formFieldSampleTypeConfig = "sample_type_config"

	labelNameServiceName         = "service_name"
	defaultLabelValueServiceName = "unspecified"
)

const (
	GoSpy      = "gospy"
	JavaSpy    = "javaspy"
	EbpfSpy    = "ebpfspy"
	DDTraceSpy = "ddtrace"
	PerfSpy    = "perf_script"
)

// TagServiceName 需要忽略的服务 Tag 名称
var ignoredTagNames = []string{"__session_id__"}

var errNoTokenFound = errors.New("no profile token found")

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

	// 注册 connect rpc 服务，兼容 phlare 的数据上报，用来接收 ebpf 采集的 profiling 数据
	pushv1connect.RegisterPusherServiceHandler(receiver.RecvHttpRouter(), httpSvc)
}

func (s HttpService) Push(_ context.Context, req *connect.Request[pushv1.PushRequest]) (*connect.Response[pushv1.PushResponse], error) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.Peer().Addr, req.Header())
	start := time.Now()

	originToken := req.Header().Get(define.KeyToken)
	if originToken == "" {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		logger.Warnf("failed to get token, ip=%s, err: %s", ip, errNoTokenFound)
		return nil, connect.NewError(connect.CodeInvalidArgument, errNoTokenFound)
	}
	token := define.Token{Original: originToken}

	for _, rpcSeries := range req.Msg.Series {
		tags := make(map[string]string)
		for _, label := range rpcSeries.Labels {
			tags[label.Name] = label.Value
		}

		// 补充 service_name 字段
		serviceName, ok := tags[labelNameServiceName]
		if !ok || serviceName == "" {
			tags[labelNameServiceName] = defaultLabelValueServiceName
		}

		for _, sample := range rpcSeries.Samples {
			rawProfile := define.ProfilesRawData{
				Data: define.ProfilePprofFormatOrigin(sample.RawProfile),
				Metadata: define.ProfileMetadata{
					StartTime:       start, // 这里的时间实际没有使用，具体的时间取自 pprof 数据
					EndTime:         start, // 这里的时间实际没有使用，具体的时间取自 pprof 数据
					SpyName:         EbpfSpy,
					Format:          define.FormatPprof,
					AggregationType: "cpu",
					Units:           "nanoseconds",
					Tags:            maps.Clone(tags),
					AppName:         serviceName,
				},
			}

			r := &define.Record{
				RequestType:   define.RequestHttp,
				RequestClient: define.RequestClient{IP: ip},
				RecordType:    define.RecordProfiles,
				Data:          rawProfile,
				Token:         token,
			}
			code, processorName, err := s.Validate(r)
			if err != nil {
				err = errors.Wrapf(err, "run pre-check %s failed, code=%d, ip=%s", processorName, code, ip)
				logger.WarnRate(time.Minute, r.Token.Original, err)
				metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordProfiles, processorName, r.Token.Original, code)
				return nil, connect.NewError(connect.CodeInvalidArgument, err)
			}

			s.Publish(r)
		}
	}
	receiver.RecordHandleMetrics(metricMonitor, token, define.RequestHttp, define.RecordProfiles, 0, start)
	return connect.NewResponse(&pushv1.PushResponse{}), nil
}

// ProfilesIngest 接收 pyroscope 上报的 profile 数据
func (s HttpService) ProfilesIngest(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)
	start := time.Now()

	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, req.Body); err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		logger.Errorf("failed to read request body, err: %v", err)
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, err)
		return
	}
	defer req.Body.Close()

	token := tokenparser.FromHttpRequest(req)
	if token == "" {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		logger.Warnf("failed to get profiles token, ip=%s, err: %v", ip, errNoTokenFound)
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, errNoTokenFound)
		return
	}

	query := req.URL.Query()
	startTime, endTime, err := parseTimeFromQuery(query)
	if err != nil {
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		logger.Warnf("failed to parse startTime or endTime: %v", err)
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, err)
		return
	}

	aggregationType := query.Get("aggregationType")
	units := query.Get("units")
	spyName := query.Get("spyName")
	sampleRate, err := strconv.ParseUint(query.Get("sampleRate"), 10, 64)
	if err != nil {
		sampleRate = 0
	}

	var origin any
	format := query.Get("format")
	contentType := req.Header.Get("Content-Type")
	switch {
	case format == define.FormatPprof:
		origin = define.ProfilePprofFormatOrigin(buf.Bytes())
	case format == define.FormatJFR || strings.Contains(contentType, "multipart/form-data"):
		f, err := parseForm(req, buf.Bytes())
		if err != nil {
			metricMonitor.IncInternalErrorCounter(define.RequestHttp, define.RecordProfiles)
			logger.Warnf("failed to parse boundary, token=%s, err: %v", token, err)
			receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, err)
			return
		}
		defer func() {
			_ = f.RemoveAll()
		}()

		// TODO 处理 prev_profile 字段
		origin, err = parseField(spyName, f)
		if err != nil {
			metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
			logger.Warnf("read profile failed, ip=%s, token=%s, err: %v", ip, token, err)
			receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, err)
			return
		}
	default:
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordProfiles)
		err = errors.Errorf("spyName '%s' is not supported", spyName)
		logger.Warn(err)
		receiver.WriteErrResponse(w, define.ContentTypeJson, http.StatusBadRequest, err)
		return
	}

	// TODO: handle SampleTypeConfig
	appName, tags := parseAppNameAndTags(req)
	rawProfile := define.ProfilesRawData{
		Data: origin,
		Metadata: define.ProfileMetadata{
			StartTime:       startTime,
			EndTime:         endTime,
			SpyName:         spyName,
			Format:          format,
			AggregationType: aggregationType,
			Units:           units,
			SampleRate:      uint32(sampleRate),
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
		receiver.WriteErrResponse(w, define.ContentTypeJson, int(code), err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordProfiles, processorName, r.Token.Original, code)
		return
	}

	s.Publish(r)
	logger.Debugf("record published, ip=%s, token=%s", ip, r.Token.Original)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte("OK"))
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordProfiles, buf.Len(), start)
}

func parseForm(req *http.Request, body []byte) (*multipart.Form, error) {
	boundary, err := ParseBoundary(req.Header.Get("Content-Type"))
	if err != nil {
		return nil, err
	}

	return multipart.NewReader(bytes.NewReader(body), boundary).ReadForm(32 << 20)
}

const nanoTimestamp2020 = 1577836800000000000 // nanoseconds for 2020-01-01 00:00:00 +0000 UTC
// parseTime Used to parse timestamp format, compatible with seconds and nanosecond formats
// 2020-01-01 00:00:00 +0000 UTC
// 1577836800           // seconds
// 1577836800000        // milliseconds
// 1577836800000000     // microseconds
// 1577836800000000000  // nanoseconds
// if the timestamp is greater than 1577836800000000000, it must be nanosecond format
// Notice: only use to parse pyroscope time format, do not copy to other place
func parseTime(timestamp int64) time.Time {
	if timestamp > nanoTimestamp2020 {
		return time.Unix(0, timestamp)
	} else {
		return time.Unix(timestamp, 0)
	}
}

func parseTimeFromQuery(query url.Values) (time.Time, time.Time, error) {
	var zero time.Time
	startTs, err := strconv.ParseInt(query.Get("from"), 10, 64)
	if err != nil {
		return zero, zero, err
	}
	endTs, err := strconv.ParseInt(query.Get("until"), 10, 64)
	if err != nil {
		return zero, zero, err
	}

	return parseTime(startTs), parseTime(endTs), nil
}

// parseField 将 Http.Body 转换为 translator 所需的数据格式
func parseField(spyName string, form *multipart.Form) (any, error) {
	switch spyName {
	case JavaSpy:
		jfrBytes, err := ReadField(form, "jfr")
		if err != nil {
			return nil, errors.Wrapf(err, "%s read jfr field failed", spyName)
		}

		labelsBytes, err := ReadField(form, "labels")
		if err != nil {
			return nil, errors.Wrapf(err, "%s read labels field failed", spyName)
		}
		return define.ProfileJfrFormatOrigin{Jfr: jfrBytes, Labels: labelsBytes}, nil

	default:
		profileBytes, err := ReadField(form, "profile")
		if err != nil {
			return nil, errors.Wrapf(err, "%s read profile field failed", spyName)
		}
		return define.ProfilePprofFormatOrigin(profileBytes), nil
	}
}

// parseAppNameAndTags 获取 url 中的 tags 信息
// example: name = appName{key1=value1,key2=value2}
func parseAppNameAndTags(req *http.Request) (string, map[string]string) {
	tags := make(map[string]string)

	valueDecoded, err := url.QueryUnescape(req.URL.Query().Get("name"))
	if err != nil {
		logger.Warnf("failed to parse name params: %s", err)
		return "", tags
	}

	if valueDecoded == "" {
		return "", tags
	}

	parts := strings.SplitN(valueDecoded, "{", 2)
	if len(parts) < 2 {
		return valueDecoded, tags
	}

	pairs := strings.Split(strings.TrimRight(parts[1], "}"), ",")
	for _, pair := range pairs {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			if !slices.Contains(ignoredTagNames, kv[0]) {
				tags[kv[0]] = kv[1]
			}
		}
	}
	return parts[0], tags
}
