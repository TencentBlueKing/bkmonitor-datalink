// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	nethttp "net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/http"
	testmock "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/test/mock"
)

// GatherSuite :
type GatherSuite struct {
	suite.Suite

	ctrl      *gomock.Controller
	client    *testmock.MockClient
	newClient func(conf *configs.HTTPTaskConfig, proxyMap map[string]string) http.Client
}

// TestHTTPGather :
func TestHTTPGather(t *testing.T) {
	suite.Run(t, &GatherSuite{})
}

// SetupTestSuite :
func (s *GatherSuite) SetupTest() {
	s.ctrl = gomock.NewController(s.T())
	s.client = testmock.NewMockClient(s.ctrl)
	s.newClient = http.NewClient
	http.NewClient = func(conf *configs.HTTPTaskConfig, proxyMap map[string]string) http.Client {
		return s.client
	}
}

// TearDownTestSuite :
func (s *GatherSuite) TearDownTest() {
	s.ctrl.Finish()
	http.NewClient = s.newClient
}

func (s *GatherSuite) newGather(steps []*configs.HTTPTaskStepConfig, checkAll bool) *http.Gather {
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

	return http.New(globalConf, taskConf).(*http.Gather)
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
		code, response_code int
		error_code          define.BeatErrorCode
		expect              string
		err                 error
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
		s.client.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *nethttp.Request) (*nethttp.Response, error) {
			if c.err != nil {
				return nil, c.err
			}
			response := &nethttp.Response{
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

		s.Equal(c.response_code, event["response_code"])
		s.Equal(c.error_code, event["error_code"])
	}
}

// TestUpdateEventByResponse 测试根据http返回更新event信息
func (s *GatherSuite) TestUpdateEventByResponse() {
	type fields struct {
		globalConfig define.Config
		taskConfig   define.TaskConfig
	}
	type args struct {
		event    *http.Event
		response *nethttp.Response
	}
	type want struct {
		mediaType string
		charset   string
	}
	getResponse := func(header map[string]string) *nethttp.Response {
		h := nethttp.Header{}
		for k, v := range header {
			h.Add(k, v)
		}
		return &nethttp.Response{
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
				event: &http.Event{},
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
				event: &http.Event{},
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
				event: &http.Event{},
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
				event: &http.Event{},
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
			g := http.New(tt.fields.globalConfig, tt.fields.taskConfig).(*http.Gather)
			if err := g.UpdateEventByResponse(tt.args.event, tt.args.response); err != nil {
				t.Errorf("UpdateEventByResponse() error = %v", err)
			}
			s.Equalf(tt.want.mediaType, tt.args.event.MediaType, "UpdateEventByResponse() MediaType = %v, want %v", tt.args.event.MediaType, tt.want.mediaType)
			s.Equalf(tt.want.charset, tt.args.event.Charset, "UpdateEventByResponse() Charset = %v, want %v", tt.args.event.Charset, tt.want.charset)
		})
	}
}

func TestNewClient302Response(t *testing.T) {
	// 创建一个返回302的测试服务器
	ts := httptest.NewServer(nethttp.HandlerFunc(func(w nethttp.ResponseWriter, r *nethttp.Request) {
		nethttp.Redirect(w, r, "http://example.com", nethttp.StatusFound)
	}))
	defer ts.Close()

	// 创建一个HTTPTaskConfig
	taskConf := configs.NewHTTPTaskConfig()

	// 创建一个Client
	client := http.NewClient(taskConf, nil)

	// 创建一个请求
	req, err := nethttp.NewRequest("GET", ts.URL, nil)
	assert.NoError(t, err)

	// 执行请求
	resp, err := client.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	// 验证响应码
	assert.Equal(t, nethttp.StatusFound, resp.StatusCode)
}
