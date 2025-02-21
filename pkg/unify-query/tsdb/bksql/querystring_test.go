// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestQueryStringToSQL(t *testing.T) {
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
			err:   "doris 不支持全字段检索: test",
		},
		{
			name:  "complex nested query",
			input: "(a:1 AND (b:2 OR c:3)) OR NOT d:4",
			want:  "( ( `a` = '1' AND ( `b` = '2' OR `c` = '3' ) ) OR NOT ( `d` = '4' ) )",
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
			want:  "( `a` = '1' OR ( `b` = '2' OR `c` = '3' ) )",
		},
		{
			name:  "mixed AND/OR with proper precedence",
			input: "a:1 AND b:2 OR c:3",
			want:  "( `a` = '1' AND ( `b` = '2' OR `c` = '3' ) )",
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
			want:  "`text` LIKE '%value%'",
		},
		{
			name:  "object field",
			input: "__ext.container_name: value",
			want:  "CAST(__ext[\"container_name\"] AS STRING) = 'value'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NewDorisSQLExpr(tt.input).WithFieldsMap(map[string]string{
				"text": Text,
			}).Parser()
			if err != nil {
				assert.Equal(t, err.Error(), tt.err)
				return
			}

			assert.Equal(t, tt.want, got)
		})
	}
}
