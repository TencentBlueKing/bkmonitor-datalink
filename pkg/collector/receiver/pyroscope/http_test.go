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
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"runtime/pprof"
	"testing"

	"connectrpc.com/connect"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
	pushv1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/pyroscope/gen/proto/go/push/v1"
	typesv1 "github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/pyroscope/gen/proto/go/types/v1"
)

const (
	localURL = "http://localhost/pyroscope/ingest"
)

func TestReady(t *testing.T) {
	assert.NotPanics(t, func() {
		Ready(receiver.ComponentConfig{Pyroscope: receiver.ComponentCommon{Enabled: true}})
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

func collectTestProfileBytes(t *testing.T) []byte {
	t.Helper()

	buf := bytes.NewBuffer(nil)
	require.NoError(t, pprof.WriteHeapProfile(buf))
	return buf.Bytes()
}

func TestPusherServiceHandler(t *testing.T) {
	const (
		DefaultToken        = "valid token"
		DefaultInvalidToken = "invalid token"
	)

	newTestSvc := func() (HttpService, *atomic.Int64) {
		n := atomic.NewInt64(0)
		svc := HttpService{
			receiver.Publisher{Func: func(record *define.Record) { n.Inc() }},
			pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
				if record.Token.Original != DefaultToken {
					return define.StatusCodeUnauthorized, define.ProcessorTokenChecker, fmt.Errorf("invalid profile token")
				}
				return define.StatusCodeOK, "", nil
			}},
		}
		return svc, n
	}

	newTestReq := func() *connect.Request[pushv1.PushRequest] {
		return connect.NewRequest(&pushv1.PushRequest{
			Series: []*pushv1.RawProfileSeries{
				{
					Labels: []*typesv1.LabelPair{
						{Name: labelNameServiceName, Value: "serviceName"},
						{Name: "env", Value: "test"},
					},
					Samples: []*pushv1.RawSample{
						{
							RawProfile: collectTestProfileBytes(t),
						},
					},
				},
			},
		})
	}

	t.Run("test no token", func(t *testing.T) {
		svc, _ := newTestSvc()
		testReq := newTestReq()
		_, err := svc.Push(context.Background(), testReq)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("test invalid token", func(t *testing.T) {
		svc, _ := newTestSvc()
		testReq := newTestReq()
		testReq.Header().Set(define.KeyToken, DefaultInvalidToken)
		_, err := svc.Push(context.Background(), testReq)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})

	t.Run("test success", func(t *testing.T) {
		svc, n := newTestSvc()
		testReq := newTestReq()
		testReq.Header().Set(define.KeyToken, DefaultToken)
		resp, err := svc.Push(context.Background(), testReq)
		assert.NotNil(t, resp)
		assert.Nil(t, err)
		assert.Equal(t, int64(1), n.Load())
	})
}

func TestHttpRequest(t *testing.T) {
	t.Run("invalid params", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, localURL, &bytes.Buffer{})

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("invalid spyName", func(t *testing.T) {
		url := localURL + "?aggregationType=sum&from=1698053090&name=fuxi%7B%7D&sampleRate=100&spyName=hahaha&units=samples&until=1698053100"
		req := httptest.NewRequest(http.MethodPost, url, &bytes.Buffer{})

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("invalid body", func(t *testing.T) {
		url := localURL + "?aggregationType=sum&from=1698053090&name=fuxi%7B%7D&sampleRate=100&spyName=javaspy&units=samples&until=1698053100"
		buf := bytes.NewBufferString("{-}")
		req := httptest.NewRequest(http.MethodPost, url, buf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("broken request", func(t *testing.T) {
		svc, n := newSvc(define.StatusCodeOK, "", nil)
		buf := testkits.NewBrokenReader()
		req := httptest.NewRequest(http.MethodPost, localURL, buf)
		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusBadRequest, rw.Code)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("validate failed", func(t *testing.T) {
		svc, n := newSvc(define.StatusCodeUnauthorized, "", errors.New("MUST ERROR"))
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		fw, err := writer.CreateFormFile("profile", "profile.pprof")
		assert.NoError(t, err)

		_, err = fw.Write([]byte("any profiles"))
		assert.NoError(t, err)
		assert.NoError(t, writer.Close())

		url := localURL + "?aggregationType=sum&from=1698053090&name=fuxi%7B%7D&sampleRate=100&spyName=javaspy&units=samples&until=1698053100"
		req := httptest.NewRequest(http.MethodPost, url, body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer token_instance")

		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusUnauthorized, rw.Code)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("report success", func(t *testing.T) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		fw, err := writer.CreateFormFile("profile", "profile.pprof")
		assert.NoError(t, err)

		_, err = fw.Write([]byte("any profiles"))
		assert.NoError(t, err)
		assert.NoError(t, writer.Close())

		url := localURL + "?aggregationType=sum&from=1698053090&name=fuxi&sampleRate=100&spyName=gospy&units=samples&until=1698053100"
		req := httptest.NewRequest(http.MethodPost, url, body)
		req.Header.Set("Content-Type", writer.FormDataContentType())
		req.Header.Set("Authorization", "Bearer token_instance")

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.ProfilesIngest(rw, req)
		assert.Equal(t, http.StatusOK, rw.Code)
		assert.Equal(t, int64(1), n.Load())
	})
}

func TestParseForm(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		body := &bytes.Buffer{}
		writer := multipart.NewWriter(body)
		assert.NoError(t, writer.WriteField("test_field", "test_value"))
		assert.NoError(t, writer.Close())

		req := httptest.NewRequest(http.MethodPost, localURL, body)
		req.Header.Set("Content-Type", "multipart/form-data; boundary="+writer.Boundary())

		form, err := parseForm(req, body.Bytes())
		assert.NoError(t, err)
		assert.Equal(t, "test_value", form.Value["test_field"][0])
	})

	t.Run("failed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, localURL, nil)
		req.Header.Set("Content-Type", "application/json")

		form, err := parseForm(req, nil)
		assert.Error(t, err)
		assert.Nil(t, form)
	})
}

func TestParseAppNameAndTags(t *testing.T) {
	t.Run("valid data", func(t *testing.T) {
		url := localURL + "?name=profiling-test%7Bcustom1%3D123456%2CserviceName%3Dmy-profiling-proj%7D&units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr"
		req := httptest.NewRequest(http.MethodPost, url, &bytes.Buffer{})
		appName, tags := parseAppNameAndTags(req)
		assert.Equal(t, "profiling-test", appName)
		assert.Equal(t, map[string]string{"custom1": "123456", "serviceName": "my-profiling-proj"}, tags)
	})

	t.Run("empty tags", func(t *testing.T) {
		url := localURL + "?name=profiling-test%7B%7D&units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr"
		req := httptest.NewRequest(http.MethodPost, url, &bytes.Buffer{})
		appName, tags := parseAppNameAndTags(req)
		assert.Equal(t, "profiling-test", appName)
		assert.Empty(t, tags)
	})

	t.Run("empty tags and empty app", func(t *testing.T) {
		url := localURL + "?units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr"
		req := httptest.NewRequest(http.MethodPost, url, &bytes.Buffer{})
		appName, tags := parseAppNameAndTags(req)
		assert.Empty(t, appName)
		assert.Empty(t, tags)
	})

	t.Run("invalid tags", func(t *testing.T) {
		url := localURL + "?name=profiling-test%7B%7D%7D%7D&units=samples&aggregationType=sum&sampleRate=100&from=1708585375&until=1708585385&spyName=javaspy&format=jfr"
		req := httptest.NewRequest(http.MethodPost, url, &bytes.Buffer{})
		appName, tags := parseAppNameAndTags(req)
		assert.Equal(t, "profiling-test", appName)
		assert.Empty(t, tags)
	})
}
