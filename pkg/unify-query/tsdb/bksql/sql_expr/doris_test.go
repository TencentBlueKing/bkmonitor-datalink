// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sql_expr_test

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

func TestDorisSQLExpr_ParserQueryString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
		err   string
	}{
		{
			name:  "simple match",
			input: "name:test",
			want:  "`name` = 'test'",
		},
		{
			name:  "one word",
			input: "test",
			want:  "`log` = 'test'",
			//err:   "doris 不支持全字段检索: test",
		},
		{
			name:  "complex nested query",
			input: "(a:1 AND (b:2 OR c:3)) OR NOT d:4",
			want:  "(`a` = '1' AND (`b` = '2' OR `c` = '3') OR NOT (`d` = '4'))",
		},
		{
			name:  "invalid syntax",
			input: "name:test AND OR",
			err:   "syntax error: unexpected tOR",
		},
		{
			name:  "empty input",
			input: "",
		},
		{
			name:  "OR expression with multiple terms",
			input: "a:1 OR b:2 OR c:3",
			want:  "(`a` = '1' OR (`b` = '2' OR `c` = '3'))",
		},
		{
			name:  "mixed AND/OR with proper precedence",
			input: "a:1 AND b:2 OR c:3",
			want:  "`a` = '1' AND (`b` = '2' OR `c` = '3')",
		},
		{
			name:  "exact match with quotes",
			input: "name:\"exact match\"",
			want:  "`name` = 'exact match'",
		},
		{
			name:  "numeric equality",
			input: "age:25",
			want:  "`age` = '25'",
		},
		{
			name:  "date range query",
			input: "timestamp:[2023-01-01 TO 2023-12-31]",
			want:  "( timestamp >= '2023-01-01' AND timestamp <= '2023-12-31' )",
			err:   "syntax error: unexpected tSTRING, expecting tPHRASE or tNUMBER or tSTAR or tMINUS",
		},
		{
			name:  "invalid field name",
			input: "123field:value",
			want:  "`123field` = 'value'",
		},
		{
			name:  "text filter",
			input: "text:value",
			want:  "`text` MATCH_PHRASE_PREFIX 'value'",
		},
		{
			name:  "object field",
			input: "__ext.container_name: value",
			want:  "CAST(__ext[\"container_name\"] AS STRING) = 'value'",
		},
		{
			name:  "start",
			input: "a: >100",
			want:  "`a` > 100",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := sql_expr.NewSQLExpr(sql_expr.Doris).WithFieldsMap(map[string]string{
				"text": sql_expr.DorisTypeText,
			}).ParserQueryString(tt.input)
			if err != nil {
				assert.Equal(t, err.Error(), tt.err)
				return
			}

			assert.Equal(t, tt.want, got)
		})
	}
}

// TestDorisSQLExpr_ParserAllConditions 单元测试
func TestDorisSQLExpr_ParserAllConditions(t *testing.T) {
	tests := []struct {
		name      string
		condition metadata.AllConditions
		want      string
		wantErr   error
	}{
		{
			name: "doris test object field condition",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "object.field",
						Value:         []string{"What's UP"},
						Operator:      metadata.ConditionEqual,
					},
					{
						DimensionName: "tag",
						Value:         []string{"test"},
						Operator:      metadata.ConditionNotEqual,
					},
				},
			},
			want: "CAST(object[\"field\"] AS STRING) MATCH_PHRASE_PREFIX 'What''s UP' AND `tag` != 'test'",
		},
		{
			name: "doris t8est text field wildcard",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "object.field",
						Value:         []string{"partial"},
						Operator:      metadata.ConditionContains,
						IsWildcard:    true,
					},
				},
			},
			want: "CAST(object[\"field\"] AS STRING) LIKE '%partial%'",
		},
		{
			name: "doris test OR condition",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "status",
						Value:         []string{"running"},
						Operator:      metadata.ConditionEqual,
					},
				},
				{
					{
						DimensionName: "code",
						Value:         []string{"500"},
						Operator:      metadata.ConditionEqual,
					},
				},
			},
			want: "(`status` = 'running' OR `code` = '500')",
		},
		{
			name: "doris test numeric field without cast",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "cpu_usage",
						Value:         []string{"80"},
						Operator:      metadata.ConditionGt,
					},
				},
			},
			want: "`cpu_usage` > 80",
		},
		{
			name: "test IN operator",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "env",
						Value:         []string{"prod", "test"},
						Operator:      metadata.ConditionContains,
					},
				},
			},
			want: "`env` IN ('prod', 'test')",
		},
		{
			name: "test IN operator with wildcard",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "env",
						Value:         []string{"prod", "test"},
						Operator:      metadata.ConditionContains,
						IsWildcard:    true,
					},
				},
			},
			want: "(`env` LIKE '%prod%' OR `env` LIKE '%test%')",
		},
		{
			name: "doris test empty value",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "host",
						Value:         []string{},
						Operator:      metadata.ConditionEqual,
					},
				},
			},
			want: "",
		},
		{
			name: "doris test invalid operator",
			condition: metadata.AllConditions{
				{
					{
						DimensionName: "time",
						Value:         []string{"2023"},
						Operator:      "unknown",
					},
				},
			},
			wantErr: fmt.Errorf("unknown operator unknown"),
		},
	}

	e := sql_expr.NewSQLExpr(sql_expr.Doris).WithFieldsMap(map[string]string{
		"object.field": sql_expr.DorisTypeText,
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := e.ParserAllConditions(tt.condition)

			if err != nil {
				assert.Equal(t, tt.wantErr, err)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
