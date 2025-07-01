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

func TestRTState_makeUpSlices(t *testing.T) {
	tests := []struct {
		name           string
		initialSlices  []SliceState
		lastOne        SliceState
		count          int
		expectedTotal  int
		expectedNewIDs []int
	}{
		{
			name:          "从空状态创建3个slice",
			initialSlices: nil,
			lastOne: SliceState{
				SliceID:     0,
				StartOffset: 0,
				EndOffset:   1000,
				Size:        1000,
				Status:      SliceStatusRunning,
				MaxRetries:  3,
			},
			count:          3,
			expectedTotal:  3,
			expectedNewIDs: []int{0, 1, 2},
		},
		{
			name: "已有1个slice，补充2个slice达到3个",
			initialSlices: []SliceState{
				{
					SliceID:     0,
					StartOffset: 0,
					EndOffset:   1000,
					Size:        1000,
					Status:      SliceStatusRunning,
					MaxRetries:  3,
				},
			},
			lastOne: SliceState{
				SliceID:     0,
				StartOffset: 0,
				EndOffset:   1000,
				Size:        1000,
				Status:      SliceStatusRunning,
				MaxRetries:  3,
			},
			count:          3,
			expectedTotal:  3,
			expectedNewIDs: []int{0, 1, 2},
		},
		{
			name: "已有2个slice，补充1个slice达到3个",
			initialSlices: []SliceState{
				{
					SliceID:     0,
					StartOffset: 0,
					EndOffset:   1000,
					Size:        1000,
					Status:      SliceStatusRunning,
					MaxRetries:  3,
				},
				{
					SliceID:     1,
					StartOffset: 1000,
					EndOffset:   2000,
					Size:        1000,
					Status:      SliceStatusFailed,
					MaxRetries:  3,
				},
			},
			lastOne: SliceState{
				SliceID:     1,
				StartOffset: 1000,
				EndOffset:   2000,
				Size:        1000,
				Status:      SliceStatusFailed,
				MaxRetries:  3,
			},
			count:          3,
			expectedTotal:  3,
			expectedNewIDs: []int{0, 1, 2},
		},
		{
			name: "已有3个slice，不需要补充",
			initialSlices: []SliceState{
				{SliceID: 0, StartOffset: 0, EndOffset: 1000, Size: 1000, Status: SliceStatusRunning, MaxRetries: 3},
				{SliceID: 1, StartOffset: 1000, EndOffset: 2000, Size: 1000, Status: SliceStatusRunning, MaxRetries: 3},
				{SliceID: 2, StartOffset: 2000, EndOffset: 3000, Size: 1000, Status: SliceStatusRunning, MaxRetries: 3},
			},
			lastOne: SliceState{
				SliceID:     2,
				StartOffset: 2000,
				EndOffset:   3000,
				Size:        1000,
				Status:      SliceStatusRunning,
				MaxRetries:  3,
			},
			count:          3,
			expectedTotal:  3,
			expectedNewIDs: []int{0, 1, 2},
		},
		{
			name: "不同size的slice补充",
			initialSlices: []SliceState{
				{
					SliceID:     0,
					StartOffset: 0,
					EndOffset:   500,
					Size:        500,
					Status:      SliceStatusRunning,
					MaxRetries:  3,
				},
			},
			lastOne: SliceState{
				SliceID:     0,
				StartOffset: 0,
				EndOffset:   500,
				Size:        500,
				Status:      SliceStatusRunning,
				MaxRetries:  3,
			},
			count:          3,
			expectedTotal:  3,
			expectedNewIDs: []int{0, 1, 2},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rtState := &RTState{
				SliceStates: tt.initialSlices,
			}

			// 执行makeUpSlices
			result := rtState.makeUpSlices(tt.lastOne, tt.count)

			// 验证总数量
			assert.Equal(t, tt.expectedTotal, len(result), "slice总数量应该正确")
			assert.Equal(t, tt.expectedTotal, len(rtState.SliceStates), "RTState中的slice数量应该正确")

			// 验证slice ID的连续性
			for i, expectedID := range tt.expectedNewIDs {
				assert.Equal(t, expectedID, result[i].SliceID, "slice ID应该连续")
			}

			// 验证新创建的slice的属性
			initialCount := len(tt.initialSlices)
			if initialCount == 0 {
				initialCount = 0
			}

			for i := initialCount; i < len(result); i++ {
				slice := result[i]
				expectedStartOffset := tt.lastOne.EndOffset + int64(i-initialCount)*tt.lastOne.Size
				expectedEndOffset := expectedStartOffset + tt.lastOne.Size

				assert.Equal(t, SliceStatusRunning, slice.Status, "新slice状态应该是running")
				assert.Equal(t, expectedStartOffset, slice.StartOffset, "新slice的StartOffset应该正确")
				assert.Equal(t, expectedEndOffset, slice.EndOffset, "新slice的EndOffset应该正确")
				assert.Equal(t, tt.lastOne.Size, slice.Size, "新slice的Size应该与lastOne相同")
				assert.Equal(t, tt.lastOne.MaxRetries, slice.MaxRetries, "新slice的MaxRetries应该与lastOne相同")
				assert.Equal(t, 0, slice.RetryCount, "新slice的RetryCount应该为0")
				assert.Equal(t, "", slice.ErrorMsg, "新slice的ErrorMsg应该为空")
			}
		})
	}
}

func TestRTState_makeUpSlices_OffsetContinuity(t *testing.T) {
	// 测试offset的连续性
	rtState := &RTState{}

	lastOne := SliceState{
		SliceID:     0,
		StartOffset: 1000,
		EndOffset:   2000,
		Size:        1000,
		Status:      SliceStatusRunning,
		MaxRetries:  3,
	}

	result := rtState.makeUpSlices(lastOne, 3)

	// 验证offset的连续性
	expectedOffsets := []struct {
		start, end int64
	}{
		{2000, 3000}, // 从lastOne.EndOffset开始
		{3000, 4000},
		{4000, 5000},
	}

	for i, expected := range expectedOffsets {
		assert.Equal(t, expected.start, result[i].StartOffset, "slice %d的StartOffset应该正确", i)
		assert.Equal(t, expected.end, result[i].EndOffset, "slice %d的EndOffset应该正确", i)
	}
}

func TestRTState_makeUpSlices_EdgeCases(t *testing.T) {
	t.Run("count为0时不创建slice", func(t *testing.T) {
		rtState := &RTState{}
		lastOne := SliceState{Size: 1000, EndOffset: 1000, MaxRetries: 3}

		result := rtState.makeUpSlices(lastOne, 0)

		assert.Equal(t, 0, len(result), "count为0时不应该创建slice")
	})

	t.Run("count为负数时不创建slice", func(t *testing.T) {
		rtState := &RTState{}
		lastOne := SliceState{Size: 1000, EndOffset: 1000, MaxRetries: 3}

		result := rtState.makeUpSlices(lastOne, -1)

		assert.Equal(t, 0, len(result), "count为负数时不应该创建slice")
	})

	t.Run("已有slice数量等于count时不创建新slice", func(t *testing.T) {
		rtState := &RTState{
			SliceStates: []SliceState{
				{SliceID: 0, Size: 1000, Status: SliceStatusRunning},
				{SliceID: 1, Size: 1000, Status: SliceStatusRunning},
			},
		}
		lastOne := SliceState{Size: 1000, EndOffset: 2000, MaxRetries: 3}

		result := rtState.makeUpSlices(lastOne, 2)

		assert.Equal(t, 2, len(result), "已有slice数量等于count时不应该创建新slice")
	})
}

// 测试你提到的具体场景：max_slice为3，有1个失败，只需要makeUp 2个
func TestRTState_PickSlices_RealWorldScenario(t *testing.T) {
	tests := []struct {
		name               string
		initialSlices      []SliceState
		maxRunningCount    int
		maxFailedCount     int
		expectedSliceCount int
		expectedRunningIDs []int
		expectedError      bool
	}{
		{
			name: "max_slice=3, 有1个失败slice, 需要makeUp 2个",
			initialSlices: []SliceState{
				{
					SliceID:     0,
					StartOffset: 0,
					EndOffset:   1000,
					Size:        1000,
					Status:      SliceStatusFailed,
					RetryCount:  1,
					MaxRetries:  3,
					ErrorMsg:    "network timeout",
				},
			},
			maxRunningCount:    3,
			maxFailedCount:     10,
			expectedSliceCount: 3,
			expectedRunningIDs: []int{0, 1, 2}, // 失败的slice会被重试，加上2个新的
			expectedError:      false,
		},
		{
			name: "max_slice=3, 有2个running slice, 需要makeUp 1个",
			initialSlices: []SliceState{
				{
					SliceID:     0,
					StartOffset: 0,
					EndOffset:   1000,
					Size:        1000,
					Status:      SliceStatusRunning,
					MaxRetries:  3,
				},
				{
					SliceID:     1,
					StartOffset: 1000,
					EndOffset:   2000,
					Size:        1000,
					Status:      SliceStatusRunning,
					MaxRetries:  3,
				},
			},
			maxRunningCount:    3,
			maxFailedCount:     10,
			expectedSliceCount: 3,
			expectedRunningIDs: []int{0, 1, 2},
			expectedError:      false,
		},
		{
			name: "max_slice=3, 有1个running + 1个failed, 需要makeUp 1个",
			initialSlices: []SliceState{
				{
					SliceID:     0,
					StartOffset: 0,
					EndOffset:   1000,
					Size:        1000,
					Status:      SliceStatusRunning,
					MaxRetries:  3,
				},
				{
					SliceID:     1,
					StartOffset: 1000,
					EndOffset:   2000,
					Size:        1000,
					Status:      SliceStatusFailed,
					RetryCount:  1,
					MaxRetries:  3,
					ErrorMsg:    "connection refused",
				},
			},
			maxRunningCount:    3,
			maxFailedCount:     10,
			expectedSliceCount: 3,
			expectedRunningIDs: []int{0, 1, 2},
			expectedError:      false,
		},
		{
			name: "失败slice超过阈值，应该返回错误",
			initialSlices: []SliceState{
				{SliceID: 0, Status: SliceStatusFailed, RetryCount: 1, MaxRetries: 3},
				{SliceID: 1, Status: SliceStatusFailed, RetryCount: 2, MaxRetries: 3},
				{SliceID: 2, Status: SliceStatusFailed, RetryCount: 3, MaxRetries: 3}, // 达到最大重试次数
			},
			maxRunningCount:    3,
			maxFailedCount:     1, // 只允许1个失败，但有2个可重试的失败slice
			expectedSliceCount: 0,
			expectedError:      true,
		},
		{
			name: "已有3个running slice，不需要makeUp",
			initialSlices: []SliceState{
				{SliceID: 0, StartOffset: 0, EndOffset: 1000, Size: 1000, Status: SliceStatusRunning, MaxRetries: 3},
				{SliceID: 1, StartOffset: 1000, EndOffset: 2000, Size: 1000, Status: SliceStatusRunning, MaxRetries: 3},
				{SliceID: 2, StartOffset: 2000, EndOffset: 3000, Size: 1000, Status: SliceStatusRunning, MaxRetries: 3},
			},
			maxRunningCount:    3,
			maxFailedCount:     10,
			expectedSliceCount: 3,
			expectedRunningIDs: []int{0, 1, 2},
			expectedError:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rtState := &RTState{
				SliceStates: tt.initialSlices,
			}

			result, err := rtState.PickSlices(tt.maxRunningCount, tt.maxFailedCount)

			if tt.expectedError {
				assert.Error(t, err, "应该返回错误")
				return
			}

			assert.NoError(t, err, "不应该返回错误")
			assert.Equal(t, tt.expectedSliceCount, len(result), "slice数量应该正确")

			// 验证slice ID的正确性
			actualIDs := make([]int, len(result))
			for i, slice := range result {
				actualIDs[i] = slice.SliceID
			}
			assert.Equal(t, tt.expectedRunningIDs, actualIDs, "slice ID应该正确")

			// 验证新创建的slice的状态
			for i := len(tt.initialSlices); i < len(result); i++ {
				slice := result[i]
				assert.Equal(t, SliceStatusRunning, slice.Status, "新创建的slice状态应该是running")
				assert.Equal(t, 0, slice.RetryCount, "新创建的slice重试次数应该为0")
				assert.Equal(t, "", slice.ErrorMsg, "新创建的slice错误信息应该为空")
			}
		})
	}
}
