// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jaeger

import (
	"context"
	"time"

	"github.com/jaegertracing/jaeger/model"
	"github.com/jaegertracing/jaeger/proto-gen/api_v2"
	jaegertranslator "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/jaeger"
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pkg/prettyprint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pkg/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var metricMonitor = receiver.DefaultMetricMonitor.Source(define.SourceJaeger)

type GrpcService struct {
	receiver.Publisher
	pipeline.Validator
}

func (s GrpcService) PostSpans(ctx context.Context, req *api_v2.PostSpansRequest) (*api_v2.PostSpansResponse, error) {
	defer utils.HandleCrash()
	ip := utils.GetGrpcIpFromContext(ctx)

	start := time.Now()
	logger.Debugf("grpc request: remoteAddr=%v", ip)
	batch := req.GetBatch()
	traces, err := jaegertranslator.ProtoToTraces([]*model.Batch{&batch})
	if err != nil {
		err = errors.Wrapf(err, "jaeger translate to otlp failed, ip=%s", ip)
		logger.Warn(err)
		metricMonitor.IncDroppedCounter(define.RequestGrpc, define.RecordTraces)
		return &api_v2.PostSpansResponse{}, err
	}

	r := &define.Record{
		RequestType:   define.RequestGrpc,
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordTraces,
		Data:          traces,
	}
	prettyprint.Traces(traces)

	code, processorName, err := s.Validate(r)
	if err != nil {
		err = errors.Wrapf(err, "run pre-check failed, rtype=traces, code=%d, ip=%s", code, ip)
		logger.WarnRate(time.Minute, r.Token.Original, err)
		metricMonitor.IncPreCheckFailedCounter(define.RequestGrpc, define.RecordTraces, processorName, r.Token.Original, code)
		return &api_v2.PostSpansResponse{}, err
	}

	if traces.SpanCount() == 0 {
		metricMonitor.IncSkippedCounter(define.RequestGrpc, define.RecordTraces, r.Token.Original)
		logger.Debugf("skip empty records, ip=%v, proto=%v, rtype=%v", ip, define.RequestGrpc, define.RecordTraces)
		return &api_v2.PostSpansResponse{}, nil
	}

	s.Publish(r)
	receiver.RecordHandleMetrics(metricMonitor, r.Token, define.RequestGrpc, define.RecordTraces, 0, start)
	return &api_v2.PostSpansResponse{}, nil
}
