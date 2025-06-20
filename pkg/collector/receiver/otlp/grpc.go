// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package otlp

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"google.golang.org/grpc/metadata"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/tokenparser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceOtlp)

type GrpcService struct {
	traces  ptraceotlp.Server
	metrics pmetricotlp.Server
	logs    plogotlp.Server
}

var grpcSvc = GrpcService{
	traces:  tracesService{},
	metrics: metricsService{},
	logs:    logsService{},
}

type tracesService struct {
	receiver.Publisher
	pipeline.Validator
}

func (s tracesService) Export(ctx context.Context, req ptraceotlp.Request) (ptraceotlp.Response, error) {
	defer utils.HandleCrash()
	ip := utils.GetGrpcIpFromContext(ctx)

	start := time.Now()
	logger.Debugf("grpc request: service=traces, remoteAddr=%v", ip)
	traces := req.Traces()
	r := &define.Record{
		RequestType:   define.RequestGrpc,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordTraces,
		Data:          traces,
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		tk := tokenparser.FromGrpcMetadata(md)
		if len(tk) > 0 {
			r.Token = define.Token{Original: tk}
		}
	}
	r.Metadata = tokenparser.FromGrpcUserMetadata(md)
	prettyprint.Traces(traces)

	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check failed, rtype=traces, code=%d, ip=%s", code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestGrpc, define.RecordTraces, processorName, r.Token.Original, code)
		return ptraceotlp.NewResponse(), err
	}

	if traces.SpanCount() == 0 {
		metricMonitor.IncSkippedCounter(define.RequestGrpc, define.RecordTraces, r.Token.Original)
		logger.Debugf("skip empty records, ip=%v, proto=%v, rtype=%v", ip, define.RequestGrpc, define.RecordTraces)
		return ptraceotlp.NewResponse(), nil
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestGrpc, define.RecordTraces, 0, start)
	return ptraceotlp.NewResponse(), nil
}

type metricsService struct {
	receiver.Publisher
	pipeline.Validator
}

func (s metricsService) Export(ctx context.Context, req pmetricotlp.Request) (pmetricotlp.Response, error) {
	defer utils.HandleCrash()
	ip := utils.GetGrpcIpFromContext(ctx)

	start := time.Now()
	logger.Debugf("grpc request: service=metrics, remoteAddr=%v", ip)

	metrics := req.Metrics()
	r := &define.Record{
		RequestType:   define.RequestGrpc,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordMetrics,
		Data:          metrics,
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		tk := tokenparser.FromGrpcMetadata(md)
		if len(tk) > 0 {
			r.Token = define.Token{Original: tk}
		}
	}
	prettyprint.Metrics(metrics)

	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check failed, rtype=metrics, code=%d, ip=%s", code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestGrpc, define.RecordMetrics, processorName, r.Token.Original, code)
		return pmetricotlp.NewResponse(), err
	}

	if metrics.DataPointCount() == 0 {
		metricMonitor.IncSkippedCounter(define.RequestGrpc, define.RecordMetrics, r.Token.Original)
		logger.Debugf("skip empty records, ip=%v, proto=%v, rtype=%v", ip, define.RequestGrpc, define.RecordMetrics)
		return pmetricotlp.NewResponse(), nil
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestGrpc, define.RecordMetrics, 0, start)
	return pmetricotlp.NewResponse(), nil
}

type logsService struct {
	receiver.Publisher
	pipeline.Validator
}

func (s logsService) Export(ctx context.Context, req plogotlp.Request) (plogotlp.Response, error) {
	defer utils.HandleCrash()
	ip := utils.GetGrpcIpFromContext(ctx)

	start := time.Now()
	logger.Debugf("grpc request: service=logs, remoteAddr=%v", ip)

	logs := req.Logs()
	r := &define.Record{
		RequestType:   define.RequestGrpc,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordLogs,
		Data:          logs,
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if ok {
		tk := tokenparser.FromGrpcMetadata(md)
		if len(tk) > 0 {
			r.Token = define.Token{Original: tk}
		}
	}
	prettyprint.Logs(logs)

	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check failed, rtype=logs, code=%d, ip=%s", code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestGrpc, define.RecordLogs, processorName, r.Token.Original, code)
		return plogotlp.NewResponse(), err
	}

	if logs.LogRecordCount() == 0 {
		metricMonitor.IncSkippedCounter(define.RequestGrpc, define.RecordLogs, r.Token.Original)
		logger.Debugf("skip empty records, ip=%v, proto=%v, rtype=%v", ip, define.RequestGrpc, define.RecordLogs)
		return plogotlp.NewResponse(), nil
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestGrpc, define.RecordLogs, 0, start)
	return plogotlp.NewResponse(), nil
}
