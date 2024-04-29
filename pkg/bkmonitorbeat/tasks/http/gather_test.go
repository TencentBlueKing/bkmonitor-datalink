// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	testmock "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/test/mock"
)

type GatherSuite struct {
	suite.Suite

	ctrl      *gomock.Controller
	client    *testmock.MockClient
	newClient func(conf *configs.HTTPTaskConfig, proxyMap map[string]string) Client
}

func TestHTTPGather(t *testing.T) {
	suite.Run(t, &GatherSuite{})
}

func (s *GatherSuite) SetupTest() {
	s.ctrl = gomock.NewController(s.T())
	s.client = testmock.NewMockClient(s.ctrl)
	s.newClient = NewClient
	NewClient = func(conf *configs.HTTPTaskConfig, proxyMap map[string]string) Client {
		return s.client
	}
}

func (s *GatherSuite) TearDownTest() {
	s.ctrl.Finish()
	NewClient = s.newClient
}

func (s *GatherSuite) newGather(steps []*configs.HTTPTaskStepConfig, checkAll bool) *Gather {
	globalConf := configs.NewConfig()

	// 提供一个心跳的data_id，防止命中data_id防御机制
	globalConf.HeartBeat.GlobalDataID = 1000
	taskConf := configs.NewHTTPTaskConfig()
	if checkAll {
		taskConf.DNSCheckMode = configs.CheckModeAll
	}

	for _, step := range steps {
		taskConf.Steps = append(taskConf.Steps, step)
	}

	s.Nil(globalConf.Clean())
	s.Nil(taskConf.Clean())

	return New(globalConf, taskConf).(*Gather)
}

type timeoutTestError struct {
	timeout bool
}

func (e *timeoutTestError) Error() string {
	return "test"
}

func (e *timeoutTestError) Timeout() bool {
	return e.timeout
}

func (s *GatherSuite) TestGatherRun() {
	cases := []struct {
		code         int
		responseCode int
		errorCode    define.BeatErrorCode
		expect       string
		err          error
	}{
		{200, 200, define.BeatErrCodeOK, "", nil},
		{200, 200, define.BeatErrCodeResponseMatchError, "x", nil},
		{400, 400, define.BeatErrCodeResponseCodeError, "", nil},
		{500, 0, define.BeatErrCodeResponseError, "", &url.Error{
			Op: "parse", URL: "", Err: errors.New("test"),
		}},
		{200, 0, define.BeatErrCodeResponseTimeoutError, "", &url.Error{
			Op: "parse", URL: "", Err: &timeoutTestError{timeout: true},
		}},
	}

	for _, c := range cases {
		s.client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
			if c.err != nil {
				return nil, c.err
			}
			response := &http.Response{
				Request:    request,
				Status:     "?",
				StatusCode: c.code,
				Body:       io.NopCloser(bytes.NewReader([]byte("test"))),
			}
			return response, nil
		})
		gather := s.newGather([]*configs.HTTPTaskStepConfig{
			{
				URL: "http://localhost/1",
				SimpleMatchParam: configs.SimpleMatchParam{
					Request:        "base64://cmVxdWVzdA==",
					Response:       c.expect,
					ResponseFormat: "startswith",
				},
			},
		}, false)
		e := make(chan define.Event, 1)
		gather.Run(context.Background(), e)

		gather.Wait()
		ev := <-e
		event := ev.AsMapStr()

		s.Equal(c.responseCode, event["response_code"])
		s.Equal(c.errorCode, event["error_code"])
	}
}

func (s *GatherSuite) TestUpdateEventByResponse() {
	type fields struct {
		globalConfig define.Config
		taskConfig   define.TaskConfig
	}
	type args struct {
		event    *Event
		response *http.Response
	}
	type want struct {
		mediaType string
		charset   string
	}
	getResponse := func(header map[string]string) *http.Response {
		h := http.Header{}
		for k, v := range header {
			h.Add(k, v)
		}
		return &http.Response{
			Header: h,
		}
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   want
	}{
		{
			"无charset",
			fields{
				configs.NewConfig(),
				configs.NewHTTPTaskConfig(),
			},
			args{
				event: &Event{},
				response: getResponse(map[string]string{
					"content-type": "text/html",
				}),
			},
			want{
				mediaType: "text/html",
				charset:   "",
			},
		},
		{
			"无charset有boundary",
			fields{
				configs.NewConfig(),
				configs.NewHTTPTaskConfig(),
			},
			args{
				event: &Event{},
				response: getResponse(map[string]string{
					"content-type": "multipart/form-data; boundary=something",
				}),
			},
			want{
				mediaType: "multipart/form-data",
				charset:   "",
			},
		},
		{
			"有charset",
			fields{
				configs.NewConfig(),
				configs.NewHTTPTaskConfig(),
			},
			args{
				event: &Event{},
				response: getResponse(map[string]string{
					"content-type": "text/html; charset=utf-8",
				}),
			},
			want{
				mediaType: "text/html",
				charset:   "utf-8",
			},
		},
		{
			"有空格",
			fields{
				configs.NewConfig(),
				configs.NewHTTPTaskConfig(),
			},
			args{
				event: &Event{},
				response: getResponse(map[string]string{
					"content-type": "text/html  ; charset=utf-8",
				}),
			},
			want{
				mediaType: "text/html",
				charset:   "utf-8",
			},
		},
	}
	for _, tt := range tests {
		s.T().Run(tt.name, func(t *testing.T) {
			g := New(tt.fields.globalConfig, tt.fields.taskConfig).(*Gather)
			if err := g.UpdateEventByResponse(tt.args.event, tt.args.response); err != nil {
				t.Errorf("UpdateEventByResponse() error = %v", err)
			}
			s.Equalf(tt.want.mediaType, tt.args.event.MediaType, "UpdateEventByResponse() MediaType = %v, want %v", tt.args.event.MediaType, tt.want.mediaType)
			s.Equalf(tt.want.charset, tt.args.event.Charset, "UpdateEventByResponse() Charset = %v, want %v", tt.args.event.Charset, tt.want.charset)
		})
	}
}

func TestValidateConfig(t *testing.T) {
	excepted := &configs.HTTPTaskStepConfig{
		SimpleMatchParam: configs.SimpleMatchParam{
			Request:  "request",
			Response: "response",
		},
		Headers: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
	}

	t.Run("normal", func(t *testing.T) {
		conf := &configs.HTTPTaskStepConfig{
			SimpleMatchParam: configs.SimpleMatchParam{
				Response: "response",
				Request:  "request",
			},
			Headers: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
		}
		validateConfig(conf)
		assert.Equal(t, *excepted, *conf)
	})

	t.Run("base64", func(t *testing.T) {
		conf := &configs.HTTPTaskStepConfig{
			SimpleMatchParam: configs.SimpleMatchParam{
				Response: "base64://cmVzcG9uc2U=",
				Request:  "base64://cmVxdWVzdA==",
			},
			Headers: map[string]string{
				"key1": "base64://dmFsdWUx",
				"key2": "value2",
			},
		}
		validateConfig(conf)
		assert.Equal(t, *excepted, *conf)
	})
}
