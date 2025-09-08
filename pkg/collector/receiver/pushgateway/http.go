// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pushgateway

import (
	"encoding/base64"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"github.com/matttproud/golang_protobuf_extensions/pbutil"
	"github.com/pkg/errors"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	routeMetricsLabelsBase = "/metrics/job@base64/{job}"
	routeMetricsLabels     = "/metrics/job/{job}/{labels:.*}"
	routeMetricsJob        = "/metrics/job/{job}"
	routeMetricsJobBase    = "/metrics/job@base64/{job}/{labels:.*}"

	base64Suffix = "@base64"
	fieldJob     = "job"
	fieldLabels  = "labels"
)

func init() {
	receiver.RegisterReadyFunc(define.SourcePushGateway, Ready)
}

func Ready() {
	receiver.RegisterRecvHttpRoute(define.SourcePushGateway, []receiver.RouteWithFunc{
		{
			Method:       http.MethodPost,
			RelativePath: routeMetricsJobBase,
			HandlerFunc:  httpSvc.ExportBase64Metrics,
		},
		{
			Method:       http.MethodPut,
			RelativePath: routeMetricsJobBase,
			HandlerFunc:  httpSvc.ExportBase64Metrics,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeMetricsJob,
			HandlerFunc:  httpSvc.ExportMetrics,
		},
		{
			Method:       http.MethodPut,
			RelativePath: routeMetricsJob,
			HandlerFunc:  httpSvc.ExportMetrics,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeMetricsLabelsBase,
			HandlerFunc:  httpSvc.ExportBase64Metrics,
		},
		{
			Method:       http.MethodPut,
			RelativePath: routeMetricsLabelsBase,
			HandlerFunc:  httpSvc.ExportBase64Metrics,
		},
		{
			Method:       http.MethodPost,
			RelativePath: routeMetricsLabels,
			HandlerFunc:  httpSvc.ExportMetrics,
		},
		{
			Method:       http.MethodPut,
			RelativePath: routeMetricsLabels,
			HandlerFunc:  httpSvc.ExportMetrics,
		},
	})
}

// Http Server

type HttpService struct {
	receiver.Publisher
	pipeline.Validator
}

var httpSvc HttpService

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourcePushGateway)

func (s HttpService) exportMetrics(w http.ResponseWriter, req *http.Request, jobBase64Encoded bool) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)
	contentLength := utils.GetContentLength(req.Header)

	start := time.Now()
	vars := extractVars(req)
	job := vars[fieldJob]

	var err error
	if jobBase64Encoded {
		if job, err = decodeBase64(job); err != nil {
			logger.Warnf("invalid base64 encoding in job name, job=%s, err: %v", job, err)
			metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordPushGateway)
			receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, errResponse(err))
			return
		}
	}

	if job == "" {
		err = errors.Errorf("empty job name in request url: %s", req.URL)
		logger.Warn(err)
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordPushGateway)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, errResponse(err))
		return
	}

	lbs := vars[fieldLabels]
	labels, err := splitLabels(lbs)
	if err != nil {
		logger.Warnf("invalid labels field in request url=%s, err: %v", lbs, err)
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordPushGateway)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, errResponse(err))
		return
	}

	labels["job"] = job
	logger.Debugf("extract labels from request url=%s, lbs=%+v", req.URL, labels)

	var metricFamilies map[string]*dto.MetricFamily
	ctMediatype, ctParams, ctErr := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if ctErr == nil && ctMediatype == "application/vnd.google.protobuf" &&
		ctParams["encoding"] == "delimited" &&
		ctParams["proto"] == "io.prometheus.client.MetricFamily" {
		metricFamilies = map[string]*dto.MetricFamily{}
		for {
			mf := &dto.MetricFamily{}
			if _, err = pbutil.ReadDelimited(req.Body, mf); err != nil {
				if err == io.EOF {
					err = nil
				}
				break
			}
			metricFamilies[mf.GetName()] = mf
		}
	} else {
		// We could do further content-type checks here, but the
		// fallback for now will anyway be the text format
		// version 0.0.4, so just go for it and see if it works.
		var parser expfmt.TextParser
		metricFamilies, err = parser.TextToMetricFamilies(req.Body)
	}

	if err != nil {
		logger.Warnf("failed to parse body, ip=%v, err: %v", ip, err)
		metricMonitor.IncDroppedCounter(define.RequestHttp, define.RecordPushGateway)
		receiver.WriteResponse(w, define.ContentTypeJson, http.StatusBadRequest, errResponse(err))
		return
	}

	token := tokenparser.FromHttpRequest(req)
	r := &define.Record{
		RecordType:    define.RecordPushGateway,
		RequestType:   define.RequestHttp,
		RequestClient: define.RequestClient{IP: ip},
		Token:         define.Token{Original: token},
	}
	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check failed, code=%d, ip=%s", code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		receiver.WriteResponse(w, define.ContentTypeJson, int(code), errResponse(err))
		metricMonitor.IncPreCheckFailedCounter(define.RequestHttp, define.RecordPushGateway, processorName, r.Token.Original, code)
		return
	}

	logger.Debugf("receive metricFamilies count=%d, ip=%s", len(metricFamilies), ip)
	for _, mf := range metricFamilies {
		s.Publish(&define.Record{
			RequestType:   define.RequestHttp,
			RequestClient: define.RequestClient{IP: ip},
			RecordType:    define.RecordPushGateway,
			Token:         r.Token,
			Data: &define.PushGatewayData{
				MetricFamilies: mf,
				Labels:         utils.CloneMap(labels),
			},
		})
	}

	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestHttp, define.RecordPushGateway, contentLength, start)
	receiver.WriteResponse(w, define.ContentTypeJson, http.StatusOK, []byte(`{"status": "success"}`))
}

func (s HttpService) ExportBase64Metrics(w http.ResponseWriter, req *http.Request) {
	s.exportMetrics(w, req, true)
}

func (s HttpService) ExportMetrics(w http.ResponseWriter, req *http.Request) {
	s.exportMetrics(w, req, false)
}

func errResponse(err error) []byte {
	b, _ := json.Marshal(map[string]string{
		"status": "error",
		"error":  err.Error(),
	})
	return b
}

func extractVars(req *http.Request) map[string]string {
	vars := mux.Vars(req)
	if vars == nil {
		vars = make(map[string]string)
	}
	return vars
}

func decodeBase64(in string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(in, "="))
	return string(b), err
}

// splitLabels splits a labels string into a label map mapping names to values.
func splitLabels(labels string) (map[string]string, error) {
	result := map[string]string{}
	if len(labels) <= 1 {
		return result, nil
	}
	components := strings.Split(labels, "/")
	if len(components)%2 != 0 {
		return nil, errors.Errorf("odd number of components in label string %q", labels)
	}

	for i := 0; i < len(components)-1; i += 2 {
		name, value := components[i], components[i+1]
		trimmedName := strings.TrimSuffix(name, base64Suffix)
		if !model.LabelNameRE.MatchString(trimmedName) ||
			strings.HasPrefix(trimmedName, model.ReservedLabelPrefix) {
			return nil, errors.Errorf("improper label name %q", trimmedName)
		}
		if name == trimmedName {
			result[name] = value
			continue
		}
		decodedValue, err := decodeBase64(value)
		if err != nil {
			return nil, errors.Errorf("invalid base64 encoding for label %s=%q: %v", trimmedName, value, err)
		}
		result[trimmedName] = decodedValue
	}
	return result, nil
}
