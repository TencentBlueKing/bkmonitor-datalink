// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris_parser

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestParseWithFieldAlias(t *testing.T) {
	testCases := []struct {
		name     string
		q        string
		expected string
		err      error
	}{
		{
			name: "test-1",
			q: `select pod_namespace, 
count(*) AS log_count 
from t_table 
where log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal' GROUP BY serverIp LIMIT 1000`,
			expected: "select __ext.io_kubernetes_pod_namespace , count ( * ) AS log_count from t_table where log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal' GROUP BY test_server_ip LIMIT 1000 ",
		},
		{
			name:     "test-2",
			q:        `show TABLES`,
			expected: `show TABLES `,
		},
		{
			name: "test-3",
			q: `SELECT
  JSON_EXTRACT_STRING (__ext, '$.io_kubernetes_pod_namespace') as ns,
  split_part (log, '|', 3) as ct,
  count(*)
WHERE
 log MATCH_ALL 'Reliable RPC called out of limit'
group by
  ns,
  ct
LIMIT
  1000`,
			expected: `SELECT JSON_EXTRACT_STRING ( __ext , '$.io_kubernetes_pod_namespace' ) as ns , split_part ( log , '|' , 3 ) as ct , count ( * ) WHERE log MATCH_ALL 'Reliable RPC called out of limit' group by ns , ct LIMIT 1000 `,
		},
		{
			name:     "test-4",
			q:        "SELECT pod_namespace AS ns, split_part (log, '|', 3) AS ct, count(*) FROM `table` WHERE log MATCH_ALL 'Reliable RPC called out of limit' group by ns, ct LIMIT 1000",
			expected: "SELECT __ext.io_kubernetes_pod_namespace AS ns , split_part ( log , '|' , 3 ) AS ct , count ( * ) FROM `table` WHERE log MATCH_ALL 'Reliable RPC called out of limit' group by ns , ct LIMIT 1000 ",
		},
		{
			name: "test-5",
			q: `SELECT
  JSON_EXTRACT_STRING(__ext, '$.io_kubernetes_pod_namespace') as ns,
  split_part (log, '|', 3) as ct,
  count(*)
WHERE
 log MATCH_ALL 'Reliable RPC called out of limit'
group by
  ns,
  ct
LIMIT
  1000`,
			expected: `SELECT JSON_EXTRACT_STRING ( __ext , '$.io_kubernetes_pod_namespace' ) as ns , split_part ( log , '|' , 3 ) as ct , count ( * ) WHERE log MATCH_ALL 'Reliable RPC called out of limit' group by ns , ct LIMIT 1000 `,
		},
		{
			name: "test-6",
			q: `SELECT
  serverIp,
  COUNT(*) AS log_count
WHERE
  log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal'
GROUP BY
  serverIp
LIMIT
  1000
`,
			expected: `SELECT test_server_ip , COUNT ( * ) AS log_count WHERE log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal' GROUP BY test_server_ip LIMIT 1000 `,
		},
		{
			name:     "test-7",
			q:        `select field_1, field_2 where log match_phrase 'test' group by dim_1, dim_2`,
			expected: `select field_1 , field_2 where log match_phrase 'test' group by dim_1 , dim_2 `,
		},
		{
			name: "test-8",
			q:    `test error sql`,
			err:  fmt.Errorf("sql: test error sql, parse error: test"),
		},
		{
			name: "test-9",
			q:    `select_1 * from_1 where 1=1'`,
			err:  fmt.Errorf("sql: select_1 * from_1 where 1=1', parse error: select_1"),
		},
	}

	mock.Init()
	fieldAlias := map[string]string{
		"pod_namespace": "__ext.io_kubernetes_pod_namespace",
		"serverIp":      "test_server_ip",
	}
	fieldMap := map[string]string{
		"__ext.io_kubernetes_pod_namespace": "string",
	}

	ctx := context.Background()
	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			// antlr4 and visitor
			visitor := ParseDorisSQL(ctx, c.q, fieldMap, fieldAlias)

			sql := visitor.GetModifiedSQL()
			assert.NotNil(t, visitor)

			if c.err != nil {
				assert.Equal(t, c.err, visitor.Err)
				return
			}

			assert.Nil(t, visitor.Err)
			assert.NotEmpty(t, sql)
			assert.Equal(t, c.expected, sql)
			return
		})
	}
}
