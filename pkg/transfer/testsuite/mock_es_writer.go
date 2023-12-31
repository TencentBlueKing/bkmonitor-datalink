// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Code generated by MockGen. DO NOT EDIT.
// Source: transfer/elasticsearch (interfaces: BulkWriter)

// Package testsuite is a generated GoMock package.
package testsuite

import (
	context "context"
	reflect "reflect"

	gomock "github.com/golang/mock/gomock"

	elasticsearch "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/elasticsearch"
)

// MockBulkWriter is a mock of BulkWriter interface.
type MockBulkWriter struct {
	ctrl     *gomock.Controller
	recorder *MockBulkWriterMockRecorder
}

// MockBulkWriterMockRecorder is the mock recorder for MockBulkWriter.
type MockBulkWriterMockRecorder struct {
	mock *MockBulkWriter
}

// NewMockBulkWriter creates a new mock instance.
func NewMockBulkWriter(ctrl *gomock.Controller) *MockBulkWriter {
	mock := &MockBulkWriter{ctrl: ctrl}
	mock.recorder = &MockBulkWriterMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockBulkWriter) EXPECT() *MockBulkWriterMockRecorder {
	return m.recorder
}

// Close mocks base method.
func (m *MockBulkWriter) Close() error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Close")
	ret0, _ := ret[0].(error)
	return ret0
}

// Close indicates an expected call of Close.
func (mr *MockBulkWriterMockRecorder) Close() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Close", reflect.TypeOf((*MockBulkWriter)(nil).Close))
}

// Write mocks base method.
func (m *MockBulkWriter) Write(arg0 context.Context, arg1 string, arg2 elasticsearch.Records) (*elasticsearch.Response, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Write", arg0, arg1, arg2)
	ret0, _ := ret[0].(*elasticsearch.Response)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// Write indicates an expected call of Write.
func (mr *MockBulkWriterMockRecorder) Write(arg0, arg1, arg2 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Write", reflect.TypeOf((*MockBulkWriter)(nil).Write), arg0, arg1, arg2)
}
