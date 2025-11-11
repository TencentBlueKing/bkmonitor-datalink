// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package precision

import (
	stdJson "encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcessNumber(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected any
	}{
		{
			name:     "小整数保持int64",
			input:    "12345",
			expected: int64(12345),
		},
		{
			name:     "超过JS安全范围的整数转为字符串",
			input:    "5149358700871317076",
			expected: "5149358700871317076",
		},
		{
			name:     "超过int64最大值转为字符串",
			input:    "16719627195793378612",
			expected: "16719627195793378612",
		},
		{
			name:     "浮点数返回字符串保持精度",
			input:    "123.45",
			expected: "123.45",
		},
		{
			name:     "时间戳在安全范围内保持int64",
			input:    "1762164624000",
			expected: int64(1762164624000),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			num := stdJson.Number(tt.input)
			result := ProcessNumber(num)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func BenchmarkProcessNumber(b *testing.B) {
	num := stdJson.Number("16719627195793378612")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ProcessNumber(num)
	}
}