// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package hashconsul_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/hashconsul"
)

// MockConsulClient 实现了 ConsulClient 接口，用于模拟 Consul 的行为
type MockConsulClient struct {
	expectedModifyIndex uint64
	lastValue           string
	putCalled           bool
}

// Put 模拟 Consul 的 Put 方法，当 ModifyIndex 与预期不符时返回错误
func (m *MockConsulClient) Put(key, val string, modifyIndex uint64) error {
	m.putCalled = true
	if modifyIndex != m.expectedModifyIndex {
		return errors.New("modify index mismatch")
	}
	m.lastValue = val
	return nil
}

func TestPutCas(t *testing.T) {
	tests := []struct {
		name             string
		initialIndex     uint64
		inputModifyIndex uint64
		oldValueBytes    []byte
		newValue         string
		expectPutCalled  bool
		expectError      bool
	}{
		{
			name:             "modify index matches, should succeed",
			initialIndex:     10,
			inputModifyIndex: 10,
			oldValueBytes:    []byte(`{"foo": "bar"}`),
			newValue:         `{"foo": "baz"}`,
			expectPutCalled:  true,
			expectError:      false,
		},
		{
			name:             "modify index mismatch, should fail",
			initialIndex:     10,
			inputModifyIndex: 9,
			oldValueBytes:    []byte(`{"foo": "bar"}`),
			newValue:         `{"foo": "baz"}`,
			expectPutCalled:  true,
			expectError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &MockConsulClient{expectedModifyIndex: tt.initialIndex}

			// 执行 PutCas 测试
			err := hashconsul.PutCas(mockClient, "test-key", tt.newValue, tt.inputModifyIndex, tt.oldValueBytes)

			if tt.expectError {
				assert.Error(t, err, "expected an error but got none")
			} else {
				assert.NoError(t, err, "expected no error but got one")
			}

			assert.Equal(t, tt.expectPutCalled, mockClient.putCalled, "Put was not called as expected")
		})
	}
}
