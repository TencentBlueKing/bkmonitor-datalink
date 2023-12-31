// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Code generated by MockGen. DO NOT EDIT.
// Source: transfer/redis (interfaces: ClientOfRedis)

// Package testsuite is a generated GoMock package.
package testsuite

import (
	reflect "reflect"

	redis "github.com/go-redis/redis"
	gomock "github.com/golang/mock/gomock"
)

// MockClientOfRedis is a mock of ClientOfRedis interface.
type MockClientOfRedis struct {
	ctrl     *gomock.Controller
	recorder *MockClientOfRedisMockRecorder
}

// MockClientOfRedisMockRecorder is the mock recorder for MockClientOfRedis.
type MockClientOfRedisMockRecorder struct {
	mock *MockClientOfRedis
}

// NewMockClientOfRedis creates a new mock instance.
func NewMockClientOfRedis(ctrl *gomock.Controller) *MockClientOfRedis {
	mock := &MockClientOfRedis{ctrl: ctrl}
	mock.recorder = &MockClientOfRedisMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockClientOfRedis) EXPECT() *MockClientOfRedisMockRecorder {
	return m.recorder
}

// Close mocks base method.
func (m *MockClientOfRedis) Close() error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Close")
	ret0, _ := ret[0].(error)
	return ret0
}

// Close indicates an expected call of Close.
func (mr *MockClientOfRedisMockRecorder) Close() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Close", reflect.TypeOf((*MockClientOfRedis)(nil).Close))
}

// LLen mocks base method.
func (m *MockClientOfRedis) LLen(arg0 string) *redis.IntCmd {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "LLen", arg0)
	ret0, _ := ret[0].(*redis.IntCmd)
	return ret0
}

// LLen indicates an expected call of LLen.
func (mr *MockClientOfRedisMockRecorder) LLen(arg0 interface{}) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "LLen", reflect.TypeOf((*MockClientOfRedis)(nil).LLen), arg0)
}

// Ping mocks base method.
func (m *MockClientOfRedis) Ping() *redis.StatusCmd {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Ping")
	ret0, _ := ret[0].(*redis.StatusCmd)
	return ret0
}

// Ping indicates an expected call of Ping.
func (mr *MockClientOfRedisMockRecorder) Ping() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Ping", reflect.TypeOf((*MockClientOfRedis)(nil).Ping))
}
