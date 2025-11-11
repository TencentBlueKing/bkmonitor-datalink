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
		// int 类型转换测试
		{
			name:     "小整数转换为int",
			input:    "12345",
			expected: int(12345),
		},
		{
			name:     "负数转换为int",
			input:    "-12345",
			expected: int(-12345),
		},
		{
			name:     "零值转换为int",
			input:    "0",
			expected: int(0),
		},
		{
			name:     "int最大值边界",
			input:    "2147483647",
			expected: int(2147483647),
		},
		{
			name:     "int最小值边界",
			input:    "-2147483648",
			expected: int(-2147483648),
		},
		{
			name:     "超出32位int范围但仍为int类型", // int 类型转换测试（在64位系统上，cast可以处理更大的整数）
			input:    "2147483648",
			expected: int(2147483648),
		},
		{
			name:     "大正整数仍为int类型（64位系统）",
			input:    "4294967295",
			expected: int(4294967295),
		},
		{
			name:     "非常大的正整数仍为int类型（64位系统）",
			input:    "5149358700871317076",
			expected: int(5149358700871317076),
		},
		{
			name:     "超过uint64最大值转为float64",
			input:    "18446744073709551616",
			expected: float64(18446744073709551616),
		},

		{
			name:     "超出32位int范围的负数仍为int类型",
			input:    "-2147483649",
			expected: int(-2147483649),
		},
		{
			name:     "大负数仍为int类型（64位系统）",
			input:    "-9223372036854775808",
			expected: int(-9223372036854775808),
		},

		{
			name:     "超出32位uint范围的正整数仍为int类型",
			input:    "4294967296",
			expected: int(4294967296),
		},
		{
			name:     "接近uint64最大值仍为int类型",
			input:    "18446744073709551615",
			expected: uint(18446744073709551615),
		},

		{
			name:     "浮点数转换为float64",
			input:    "123.45",
			expected: float64(123.45),
		},
		{
			name:     "科学计数法转换为float64",
			input:    "1.23e10",
			expected: float64(1.23e10),
		},
		{
			name:     "负浮点数转换为float64",
			input:    "-123.45",
			expected: float64(-123.45),
		},
		{
			name:     "整数形式的浮点数转换为float64",
			input:    "123.0",
			expected: float64(123.0),
		},

		{
			name:     "无效数字转为字符串",
			input:    "abc123",
			expected: "abc123",
		},
		{
			name:     "空字符串保持为字符串",
			input:    "",
			expected: "",
		},
		{
			name:     "只有小数点转为字符串",
			input:    ".",
			expected: ".",
		},
		{
			name:     "混合字符转为字符串",
			input:    "123abc",
			expected: "123abc",
		},
		{
			name:     "非常大的数值转为float64",
			input:    "999999999999999999999999999999",
			expected: float64(999999999999999999999999999999),
		},
		{
			name:     "带前导零的整数（八进制）",
			input:    "000123",
			expected: int(83), // 八进制 123 = 十进制 83
		},
		{
			name:     "带加号的整数",
			input:    "+123",
			expected: int(123),
		},
		{
			name:     "带前导零的负数（八进制）",
			input:    "-00123",
			expected: int(-83), // 八进制 123 = 十进制 83
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			num := stdJson.Number(tt.input)
			result := ProcessNumber(num)
			assert.Equal(t, tt.expected, result,
				"Input: %s, Expected type: %T, Got type: %T",
				tt.input, tt.expected, result)
		})
	}
}
