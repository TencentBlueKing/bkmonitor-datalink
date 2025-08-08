// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package querystring_parser

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
)

func Test_parseExprToKeyValue(t *testing.T) {
	type args struct {
		expr Expr
		kv   map[string][]function.LabelMapValue
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{
			name: "test parseExprToKeyValue",
			args: args{
				expr: &MatchExpr{
					Field: "XEmDv",
					Value: "IZZpypR2E",
				},
				kv: map[string][]function.LabelMapValue{
					"XEmDv": {
						{
							Operator: "eq",
							Value:    "IZZpypR2E",
						},
					},
				},
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		labelMap := make(map[string][]function.LabelMapValue)
		labelCheck := make(map[string]struct{})
		addLabel := func(key string, operator string, values ...string) {
			if len(values) == 0 {
				return
			}

			for _, value := range values {
				checkKey := key + ":" + value + ":" + operator
				if _, ok := labelCheck[checkKey]; !ok {
					labelCheck[checkKey] = struct{}{}
					labelMap[key] = append(labelMap[key], function.LabelMapValue{
						Value:    value,
						Operator: operator,
					})
				}
			}
		}
		t.Run(tt.name, func(t *testing.T) {
			if err := parseExprToKeyValue(tt.args.expr, addLabel); (err != nil) != tt.wantErr {
				t.Errorf("parseExprToKeyValue() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !assert.Equal(t, tt.args.kv, labelMap) {
				t.Errorf("parseExprToKeyValue() = %v, want %v", labelMap, tt.args.kv)
			}
		})
	}
}

func TestLabelMap(t *testing.T) {
	testCases := []struct {
		name        string
		queryString string
		expected    map[string][]function.LabelMapValue
		expectedErr error
	}{
		{
			name:        "空 QueryString",
			queryString: "",
			expected:    map[string][]function.LabelMapValue{},
		},
		{
			name:        "通配符 QueryString",
			queryString: "*",
			expected:    map[string][]function.LabelMapValue{},
		},
		{
			name:        "简单字段匹配",
			queryString: "level:error",
			expected: map[string][]function.LabelMapValue{
				"level": {
					{
						Operator: "eq",
						Value:    "error",
					},
				},
			},
		},
		{
			name:        "带空格的字段匹配",
			queryString: "status: success",
			expected: map[string][]function.LabelMapValue{
				"status": {
					{
						Operator: "eq",
						Value:    "success",
					},
				},
			},
		},
		{
			name:        "带引号的值",
			queryString: `message:"error occurred"`,
			expected: map[string][]function.LabelMapValue{
				"message": {
					{
						Operator: "eq",
						Value:    "error occurred",
					},
				},
			},
		},
		{
			name:        "通配符匹配",
			queryString: "service:web*",
			expected: map[string][]function.LabelMapValue{
				"service": {
					{
						Operator: "contains",
						Value:    "web*",
					},
				},
			},
		},
		{
			name:        "全字段匹配（无字段名）",
			queryString: "error",
			expected: map[string][]function.LabelMapValue{
				"": {
					{
						Operator: "eq",
						Value:    "error",
					},
				},
			},
		},
		{
			name:        "AND 表达式",
			queryString: "level:error AND service:web",
			expected: map[string][]function.LabelMapValue{
				"level": {
					{
						Operator: "eq",
						Value:    "error",
					},
				},
				"service": {
					{
						Operator: "eq",
						Value:    "web",
					},
				},
			},
		},
		{
			name:        "OR 表达式",
			queryString: "level:error OR level:warning",
			expected: map[string][]function.LabelMapValue{
				"level": {
					{
						Operator: "eq",
						Value:    "error",
					},
					{
						Operator: "eq",
						Value:    "warning",
					},
				},
			},
		},
		{
			name:        "NOT 表达式",
			queryString: "NOT level:debug",
			expected:    map[string][]function.LabelMapValue{},
		},
		{
			name:        "复杂嵌套表达式",
			queryString: "(level:error OR level:warning) AND service:web",
			expected: map[string][]function.LabelMapValue{
				"level": {
					{
						Operator: "eq",
						Value:    "error",
					},
					{
						Operator: "eq",
						Value:    "warning",
					},
				},
				"service": {
					{
						Operator: "eq",
						Value:    "web",
					},
				},
			},
		},
		{
			name:        "嵌套字段名",
			queryString: "user.name:john AND resource.k8s.pod:web-pod",
			expected: map[string][]function.LabelMapValue{
				"user.name": {
					{
						Operator: "eq",
						Value:    "john",
					},
				},
				"resource.k8s.pod": {
					{
						Operator: "eq",
						Value:    "web-pod",
					},
				},
			},
		},
		{
			name:        "简单 URL 匹配",
			queryString: "url:example.com",
			expected: map[string][]function.LabelMapValue{
				"url": {
					{
						Operator: "eq",
						Value:    "example.com",
					},
				},
			},
		},
		{
			name:        "特殊字符在值中",
			queryString: `url: "https:\/\/example.com\/api?param=value"`,
			expected: map[string][]function.LabelMapValue{
				"url": {
					{
						Operator: "contains",
						Value:    `https://example.com/api?param=value`,
					},
				},
			},
		},
		{
			name:        "数值范围查询（不应提取标签）",
			queryString: "timestamp:[1234567890 TO 1234567900]",
			expected:    map[string][]function.LabelMapValue{},
		},
		{
			name:        "混合查询（字段匹配 + 数值范围）",
			queryString: "level:error AND timestamp:[1234567890 TO 1234567900]",
			expected: map[string][]function.LabelMapValue{
				"level": {
					{
						Operator: "eq",
						Value:    "error",
					},
				},
			},
		},
		{
			name:        "重复字段不同值",
			queryString: "level:error AND level:warning",
			expected: map[string][]function.LabelMapValue{
				"level": {
					{
						Operator: "eq",
						Value:    "error",
					},
					{
						Operator: "eq",
						Value:    "warning",
					},
				},
			},
		},
		{
			name:        "重复字段相同值（去重）",
			queryString: "level:error OR level:error",
			expected: map[string][]function.LabelMapValue{
				"level": {
					{
						Operator: "eq",
						Value:    "error",
					},
				},
			},
		},
		{
			name:        "多个不同字段",
			queryString: "level:error AND service:web AND component:database",
			expected: map[string][]function.LabelMapValue{
				"level": {
					{
						Operator: "eq",
						Value:    "error",
					},
				},
				"service": {
					{
						Operator: "eq",
						Value:    "web",
					},
				},
				"component": {
					{
						Operator: "eq",
						Value:    "database",
					},
				},
			},
		},
		{
			name:        "带通配符的复杂查询",
			queryString: "service:web* AND (level:error OR level:warning)",
			expected: map[string][]function.LabelMapValue{
				"service": {
					{
						Operator: "contains",
						Value:    "web*",
					},
				},
				"level": {
					{
						Operator: "eq",
						Value:    "error",
					},
					{
						Operator: "eq",
						Value:    "warning",
					},
				},
			},
		},
		{
			name:        "无效的 QueryString（解析失败）",
			queryString: "level:error AND (",
			expected:    map[string][]function.LabelMapValue{},
			expectedErr: fmt.Errorf("syntax error: unexpected $end"),
		},
		{
			name:        "多 and or 组合高亮",
			queryString: `loglevel: ("TRACE" OR "DEBUG") AND log: ("friendsvr" AND ("game_app" OR "testOr"))`,
			expected: map[string][]function.LabelMapValue{
				"loglevel": {
					{
						Operator: "eq",
						Value:    "TRACE",
					},
					{
						Operator: "eq",
						Value:    "DEBUG",
					},
				},
				"log": {
					{
						Operator: "eq",
						Value:    "friendsvr",
					},
					{
						Operator: "eq",
						Value:    "game_app",
					},
					{
						Operator: "eq",
						Value:    "testOr",
					},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			labelMap := make(map[string][]function.LabelMapValue)
			labelCheck := make(map[string]struct{})
			addLabel := func(key string, operator string, values ...string) {
				if len(values) == 0 {
					return
				}

				for _, value := range values {
					checkKey := key + ":" + value + ":" + operator
					if _, ok := labelCheck[checkKey]; !ok {
						labelCheck[checkKey] = struct{}{}
						labelMap[key] = append(labelMap[key], function.LabelMapValue{
							Value:    value,
							Operator: operator,
						})
					}
				}
			}
			err := LabelMap(tc.queryString, addLabel)
			if tc.expectedErr != nil {
				assert.NotNil(t, err, "expected an error but got nil")
				assert.EqualError(t, err, tc.expectedErr.Error(), "error message should match expected")
			} else {
				assert.Equal(t, tc.expected, labelMap, "queryStringLabelMap result should match expected")
			}
		})
	}
}
