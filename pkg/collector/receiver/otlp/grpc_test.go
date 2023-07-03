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
	"strings"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

func TestGrpcEmptyRequest(t *testing.T) {
	_, err := grpcSvc.traces.Export(context.Background(), ptraceotlp.NewRequest())
	assert.NoError(t, err)

	_, err = grpcSvc.metrics.Export(context.Background(), pmetricotlp.NewRequest())
	assert.NoError(t, err)

	_, err = grpcSvc.logs.Export(context.Background(), plogotlp.NewRequest())
	assert.NoError(t, err)
}

func TestGrpcTracesFailedPreCheck(t *testing.T) {
	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
		SpanKind:  1,
	})

	svc := tracesService{}
	svc.Validator = receiver.Validator{
		Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeUnauthorized, define.ProcessorTokenChecker, errors.New("MUST ERROR")
		},
	}

	req := ptraceotlp.NewRequestFromTraces(g.Generate())
	_, err := svc.Export(context.Background(), req)
	assert.True(t, strings.Contains(err.Error(), "traces pre-check processors got code 401"))
}

func TestGrpcMetricsFailedPreCheck(t *testing.T) {
	g := generator.NewMetricsGenerator(define.MetricsOptions{
		GaugeCount: 10,
	})

	svc := metricsService{}
	svc.Validator = receiver.Validator{
		Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeUnauthorized, define.ProcessorTokenChecker, errors.New("MUST ERROR")
		},
	}

	req := pmetricotlp.NewRequestFromMetrics(g.Generate())
	_, err := svc.Export(context.Background(), req)
	assert.True(t, strings.Contains(err.Error(), "metrics pre-check processors got code 401"))
}

func TestGrpcLogsFailedPreCheck(t *testing.T) {
	g := generator.NewLogsGenerator(define.LogsOptions{
		LogCount:  10,
		LogLength: 10,
	})

	svc := logsService{}
	svc.Validator = receiver.Validator{
		Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeUnauthorized, define.ProcessorTokenChecker, errors.New("MUST ERROR")
		},
	}

	req := plogotlp.NewRequestFromLogs(g.Generate())
	_, err := svc.Export(context.Background(), req)
	assert.True(t, strings.Contains(err.Error(), "logs pre-check processors got code 401"))
}

var testToken = define.Token{
	Original:      "fortest",
	MetricsDataId: 1001,
	TracesDataId:  1002,
	LogsDataId:    1003,
}

func TestGrpcTracesTokenAfterPreCheck(t *testing.T) {
	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
		SpanKind:  1,
	})

	var token define.Token
	svc := tracesService{
		receiver.Publisher{Func: func(r *define.Record) {
			token = r.Token
		}},
		receiver.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			record.Token = testToken
			return define.StatusCodeOK, "", nil
		}},
	}
	req := ptraceotlp.NewRequestFromTraces(g.Generate())
	_, err := svc.Export(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, testToken, token)
}

func TestGrpcMetricsTokenAfterPreCheck(t *testing.T) {
	g := generator.NewMetricsGenerator(define.MetricsOptions{
		GaugeCount: 10,
	})

	var token define.Token
	svc := metricsService{
		receiver.Publisher{Func: func(r *define.Record) {
			token = r.Token
		}},
		receiver.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			record.Token = testToken
			return define.StatusCodeOK, "", nil
		}},
	}
	req := pmetricotlp.NewRequestFromMetrics(g.Generate())
	_, err := svc.Export(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, testToken, token)
}

func TestGrpcLogsTokenAfterPreCheck(t *testing.T) {
	g := generator.NewLogsGenerator(define.LogsOptions{
		LogCount:  10,
		LogLength: 10,
	})

	var token define.Token
	svc := logsService{
		receiver.Publisher{Func: func(r *define.Record) {
			token = r.Token
		}},
		receiver.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			record.Token = testToken
			return define.StatusCodeOK, "", nil
		}},
	}
	req := plogotlp.NewRequestFromLogs(g.Generate())
	_, err := svc.Export(context.Background(), req)
	assert.NoError(t, err)
	assert.Equal(t, testToken, token)
}
