// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Code generated by MockGen. DO NOT EDIT.
// Source: github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend/influxdb (interfaces: Client)

// Package mocktest is a generated GoMock package.
package mocktest

import (
	http "net/http"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"
)

// HTTPClient is a mock of Client interface.
type HTTPClient struct {
	ctrl     *gomock.Controller
	recorder *HTTPClientMockRecorder
}

// HTTPClientMockRecorder is the mock recorder for HTTPClient.
type HTTPClientMockRecorder struct {
	mock *HTTPClient
}

// NewHTTPClient creates a new mock instance.
func NewHTTPClient(ctrl *gomock.Controller) *HTTPClient {
	mock := &HTTPClient{ctrl: ctrl}
	mock.recorder = &HTTPClientMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *HTTPClient) EXPECT() *HTTPClientMockRecorder {
	return m.recorder
}

// Do mocks base method.
func (m *HTTPClient) Do(arg0 *http.Request) (*http.Response, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Do", arg0)
	ret0, _ := ret[0].(*http.Response)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Do indicates an expected call of Do.
func (mr *HTTPClientMockRecorder) Do(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Do", reflect.TypeOf((*HTTPClient)(nil).Do), arg0)
}
