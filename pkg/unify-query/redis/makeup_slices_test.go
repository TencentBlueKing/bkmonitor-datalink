// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package redis

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRTState_PickSlices(t *testing.T) {
	tests := []struct {
		name               string
		rtType             string
		initialSlices      []SliceState
		maxRunningCount    int
		maxFailedCount     int
		expectedSliceCount int
		expectedError      bool
	}{
		{
			name:               "从空状态创建3个slice",
			rtType:             "",
			initialSlices:      nil,
			maxRunningCount:    3,
			maxFailedCount:     2,
			expectedSliceCount: 3,
			expectedError:      false,
		},
		{
			name:   "已有1个running slice，补充2个",
			rtType: "",
			initialSlices: []SliceState{
				{
					SliceID:     0,
					StartOffset: 0,
					EndOffset:   1000,
					Size:        1000,
					Status:      SliceStatusRunning,
					MaxRetries:  3,
					ConnectInfo: "http://127.0.0.1:9200",
				},
			},
			maxRunningCount:    3,
			maxFailedCount:     2,
			expectedSliceCount: 3,
			expectedError:      false,
		},
		{
			name:   "已有3个running slice，不需要补充",
			rtType: "",
			initialSlices: []SliceState{
				{SliceID: 0, Status: SliceStatusRunning, Size: 1000, MaxRetries: 3},
				{SliceID: 1, Status: SliceStatusRunning, Size: 1000, MaxRetries: 3},
				{SliceID: 2, Status: SliceStatusRunning, Size: 1000, MaxRetries: 3},
			},
			maxRunningCount:    3,
			maxFailedCount:     2,
			expectedSliceCount: 3,
			expectedError:      false,
		},
		{
			name:   "失败slice超过阈值，应该返回错误",
			rtType: "",
			initialSlices: []SliceState{
				{SliceID: 0, Status: SliceStatusFailed, RetryCount: 0, MaxRetries: 3},
				{SliceID: 1, Status: SliceStatusFailed, RetryCount: 0, MaxRetries: 3},
				{SliceID: 2, Status: SliceStatusFailed, RetryCount: 0, MaxRetries: 3},
			},
			maxRunningCount:    3,
			maxFailedCount:     2,
			expectedSliceCount: 0,
			expectedError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rtState := &RTState{
				Type:        tt.rtType,
				SliceStates: tt.initialSlices,
			}

			result, err := rtState.PickSlices(tt.maxRunningCount, tt.maxFailedCount)

			if tt.expectedError {
				assert.Error(t, err, "应该返回错误")
				return
			}

			assert.NoError(t, err, "不应该返回错误")
			assert.Equal(t, tt.expectedSliceCount, len(result), "slice数量应该正确")

			for _, slice := range result {
				assert.True(t, rtState.isSliceActive(slice), "返回的slice应该都是active状态")
			}

			if len(result) > 0 {
				for i, slice := range result {
					assert.Equal(t, i, slice.SliceID, "slice ID应该连续")
				}
			}
		})
	}
}
