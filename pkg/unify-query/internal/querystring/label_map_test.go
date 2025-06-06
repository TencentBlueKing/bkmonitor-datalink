// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package querystring

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_LabelMap(t *testing.T) {
	testCases := []struct {
		name        string
		queryString string
		expected    map[string][]string
		expectedErr error
	}{
		{
			name:        "空 QueryString",
			queryString: "",
			expected:    nil,
		},
		{
			name:        "通配符 QueryString",
			queryString: "*",
			expected:    nil,
		},
		{
			name:        "简单字段匹配",
			queryString: "level:error",
			expected: map[string][]string{
				"level": {"error"},
			},
		},
		{
			name:        "带空格的字段匹配",
			queryString: "status: success",
			expected: map[string][]string{
				"status": {"success"},
			},
		},
		{
			name:        "带引号的值",
			queryString: `message:"error occurred"`,
			expected: map[string][]string{
				"message": {"error occurred"},
			},
		},
		{
			name:        "通配符匹配",
			queryString: "service:web*",
			expected: map[string][]string{
				"service": {"web*"},
			},
		},
		{
			name:        "默认字段匹配（无字段名）",
			queryString: "error",
			expected: map[string][]string{
				"log": {"error"},
			},
		},
		{
			name:        "AND 表达式",
			queryString: "level:error AND service:web",
			expected: map[string][]string{
				"level":   {"error"},
				"service": {"web"},
			},
		},
		{
			name:        "OR 表达式",
			queryString: "level:error OR level:warning",
			expected: map[string][]string{
				"level": {"error", "warning"},
			},
		},
		{
			name:        "NOT 表达式",
			queryString: "NOT level:debug",
			expected: map[string][]string{
				"level": {"debug"},
			},
		},
		{
			name:        "复杂嵌套表达式",
			queryString: "(level:error OR level:warning) AND service:web",
			expected: map[string][]string{
				"level":   {"error", "warning"},
				"service": {"web"},
			},
		},
		{
			name:        "嵌套字段名",
			queryString: "user.name:john AND resource.k8s.pod:web-pod",
			expected: map[string][]string{
				"user.name":        {"john"},
				"resource.k8s.pod": {"web-pod"},
			},
		},
		{
			name:        "简单 URL 匹配",
			queryString: "url:example.com",
			expected: map[string][]string{
				"url": {"example.com"},
			},
		},
		{
			name:        "特殊字符在值中",
			queryString: `url:"https://example.com/api?param=value"`,
			expected: map[string][]string{
				"log": {"api?param=value\""},
			},
		},
		{
			name:        "数值范围查询（不应提取标签）",
			queryString: "timestamp:[1234567890 TO 1234567900]",
			expected:    map[string][]string{},
		},
		{
			name:        "混合查询（字段匹配 + 数值范围）",
			queryString: "level:error AND timestamp:[1234567890 TO 1234567900]",
			expected: map[string][]string{
				"level": {"error"},
			},
		},
		{
			name:        "重复字段不同值",
			queryString: "level:error AND level:warning",
			expected: map[string][]string{
				"level": {"error", "warning"},
			},
		},
		{
			name:        "重复字段相同值（去重）",
			queryString: "level:error OR level:error",
			expected: map[string][]string{
				"level": {"error"},
			},
		},
		{
			name:        "多个不同字段",
			queryString: "level:error AND service:web AND component:database",
			expected: map[string][]string{
				"level":     {"error"},
				"service":   {"web"},
				"component": {"database"},
			},
		},
		{
			name:        "带通配符的复杂查询",
			queryString: "service:web* AND (level:error OR level:warning)",
			expected: map[string][]string{
				"service": {"web*"},
				"level":   {"error", "warning"},
			},
		},
		{
			name:        "无效的 QueryString（解析失败）",
			queryString: "level:error AND (",
			expected:    map[string][]string{},
			expectedErr: fmt.Errorf("syntax error: unexpected $end"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := LabelMap(tc.queryString)
			if tc.expectedErr != nil {
				assert.NotNil(t, err, "expected an error but got nil")
				assert.EqualError(t, err, tc.expectedErr.Error(), "error message should match expected")
			} else {
				assert.Equal(t, tc.expected, result, "queryStringLabelMap result should match expected")
			}
		})
	}
}
