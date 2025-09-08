// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package skywalking

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.uber.org/atomic"
	commonv3 "skywalking.apache.org/repo/goapi/collect/common/v3"
	agentv3 "skywalking.apache.org/repo/goapi/collect/language/agent/v3"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

const (
	localSegmentURL  = "http://127.0.0.1:4318/v3/segment"
	localSegmentsURL = "http://127.0.0.1:4318/v3/segments"
)

func TestReady(t *testing.T) {
	assert.NotPanics(t, func() {
		Ready()
	})
}

func TestExtractMetadata(t *testing.T) {
	t.Run("Empty", func(t *testing.T) {
		token, instance, err := extractMetadata("")
		assert.Empty(t, token)
		assert.Empty(t, instance)
		assert.Equal(t, "skywalking: invalid metadata ''", err.Error())
	})

	t.Run("Multi_", func(t *testing.T) {
		token, instance, err := extractMetadata("token1_my_service_instance")
		assert.Equal(t, "token1", token)
		assert.Equal(t, "my_service_instance", instance)
		assert.NoError(t, err)
	})

	t.Run("2_", func(t *testing.T) {
		token, instance, err := extractMetadata("token1_my.service.instance")
		assert.Equal(t, "token1", token)
		assert.Equal(t, "my.service.instance", instance)
		assert.NoError(t, err)
	})
}

func newSvc(code define.StatusCode, msg string, err error) (HttpService, *atomic.Int64) {
	n := atomic.NewInt64(0)
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n.Inc() }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return code, msg, err
		}},
	}
	return svc, n
}

func TestHttpRequest(t *testing.T) {
	t.Run("report segments success", func(t *testing.T) {
		segments := []*agentv3.SegmentObject{mockGrpcTraceSegment(1)}
		data, err := json.Marshal(segments)
		assert.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, localSegmentsURL, bytes.NewBuffer(data))

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.reportV3Segments(rw, req)
		assert.Equal(t, rw.Code, http.StatusOK)
		assert.Equal(t, int64(1), n.Load())
	})

	t.Run("segments precheck failed", func(t *testing.T) {
		segments := []*agentv3.SegmentObject{mockGrpcTraceSegment(1)}
		data, err := json.Marshal(segments)
		assert.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, localSegmentsURL, bytes.NewBuffer(data))

		svc, n := newSvc(define.StatusCodeTooManyRequests, "", errors.New("too many requests"))
		rw := httptest.NewRecorder()
		svc.reportV3Segments(rw, req)
		assert.Equal(t, rw.Code, http.StatusTooManyRequests)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("segments invalid body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, localSegmentsURL, bytes.NewBuffer([]byte("{-}")))

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.reportV3Segments(rw, req)
		assert.Equal(t, rw.Code, http.StatusBadRequest)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("segments read failed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, localSegmentsURL, testkits.NewBrokenReader())

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.reportV3Segments(rw, req)
		assert.Equal(t, rw.Code, http.StatusInternalServerError)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("report segment success", func(t *testing.T) {
		segment := mockGrpcTraceSegment(1)
		data, err := json.Marshal(segment)
		assert.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, localSegmentURL, bytes.NewBuffer(data))

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.reportV3Segment(rw, req)
		assert.Equal(t, rw.Code, http.StatusOK)
		assert.Equal(t, int64(1), n.Load())
	})

	t.Run("segment precheck failed", func(t *testing.T) {
		segment := mockGrpcTraceSegment(1)
		data, err := json.Marshal(segment)
		assert.NoError(t, err)
		req := httptest.NewRequest(http.MethodPost, localSegmentURL, bytes.NewBuffer(data))

		svc, n := newSvc(define.StatusCodeTooManyRequests, "", errors.New("too many requests"))
		rw := httptest.NewRecorder()
		svc.reportV3Segment(rw, req)
		assert.Equal(t, rw.Code, http.StatusTooManyRequests)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("segment invalid body", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, localSegmentURL, bytes.NewBuffer([]byte("{-}")))

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.reportV3Segment(rw, req)
		assert.Equal(t, rw.Code, http.StatusBadRequest)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("segment read failed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, localSegmentURL, testkits.NewBrokenReader())

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.reportV3Segment(rw, req)
		assert.Equal(t, rw.Code, http.StatusInternalServerError)
		assert.Equal(t, int64(0), n.Load())
	})
}

func mockGrpcTraceSegment(sequence int) *agentv3.SegmentObject {
	seq := strconv.Itoa(sequence)
	return &agentv3.SegmentObject{
		TraceId:         "trace" + seq,
		TraceSegmentId:  "trace-segment" + seq,
		Service:         "demo-segmentReportService" + seq,
		ServiceInstance: "demo-instance" + seq + "_" + token,
		IsSizeLimited:   false,
		Spans: []*agentv3.SpanObject{
			{
				SpanId:        1,
				ParentSpanId:  0,
				StartTime:     time.Now().Unix(),
				EndTime:       time.Now().Unix() + 10,
				OperationName: "operation" + seq,
				Peer:          "127.0.0.1:6666",
				SpanType:      agentv3.SpanType_Entry,
				SpanLayer:     agentv3.SpanLayer_Http,
				ComponentId:   1,
				IsError:       false,
				SkipAnalysis:  false,
				Tags: []*commonv3.KeyStringValuePair{
					{
						Key:   "mock-key" + seq,
						Value: "mock-value" + seq,
					},
				},
				Logs: []*agentv3.Log{
					{
						Time: time.Now().Unix(),
						Data: []*commonv3.KeyStringValuePair{
							{
								Key:   "error.kind",
								Value: "TestErrorKind",
							},
							{
								Key:   "message",
								Value: "TestMessage",
							},
							{
								Key:   "stack",
								Value: "TestStack",
							},
							{
								Key:   "OtherInfo",
								Value: "OtherInfoValue",
							},
						},
					},
				},
				Refs: []*agentv3.SegmentReference{
					{
						RefType:                  agentv3.RefType_CrossThread,
						TraceId:                  "trace" + seq,
						ParentTraceSegmentId:     "parent-trace-segment" + seq,
						ParentSpanId:             0,
						ParentService:            "parent" + seq,
						ParentServiceInstance:    "parent" + seq,
						ParentEndpoint:           "parent" + seq,
						NetworkAddressUsedAtPeer: "127.0.0.1:6666",
					},
				},
			},
			{
				SpanId:        2,
				ParentSpanId:  1,
				StartTime:     time.Now().Unix(),
				EndTime:       time.Now().Unix() + 20,
				OperationName: "operation" + seq,
				Peer:          "127.0.0.1:6666",
				SpanType:      agentv3.SpanType_Local,
				SpanLayer:     agentv3.SpanLayer_Http,
				ComponentId:   2,
				IsError:       false,
				SkipAnalysis:  false,
				Tags: []*commonv3.KeyStringValuePair{
					{
						Key:   "mock-key" + seq,
						Value: "mock-value" + seq,
					},
				},
				Logs: []*agentv3.Log{
					{
						Time: time.Now().Unix(),
						Data: []*commonv3.KeyStringValuePair{
							{
								Key:   "log-key" + seq,
								Value: "log-value" + seq,
							},
						},
					},
				},
			},
		},
	}
}
