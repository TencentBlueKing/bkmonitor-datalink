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
		name string
		q    string

		sql string
		err error
	}{
		{
			name: "test-1",
			q: `select pod_namespace, 
count(*) AS log_count 
from t_table 
where log MATCH_PHRASE 'Error' OR serverIp MATCH_PHRASE 'Fatal' GROUP BY serverIp order by pod_namespace LIMIT 1000`,
			sql: `SELECT __ext.io_kubernetes_pod_namespace, count(*) AS log_count FROM t_table WHERE log MATCH_PHRASE 'Error' OR test_server_ip MATCH_PHRASE 'Fatal' GROUP BY test_server_ip ORDER BY __ext.io_kubernetes_pod_namespace LIMIT 1000`,
		},
		{
			name: "test-2",
			q:    `show TABLES`,
			err:  fmt.Errorf("SQL 解析失败：show TABLES"),
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
			sql: "SELECT JSON_EXTRACT_STRING(__ext, '$.io_kubernetes_pod_namespace') AS ns, split_part(log, '|', 3) AS ct, count(*) WHERE log MATCH_ALL 'Reliable RPC called out of limit' GROUP BY ns, ct LIMIT 1000",
		},
		{
			name: "test-4",
			q:    "SELECT pod_namespace AS ns, split_part (log, '|', 3) AS ct, count(*) FROM `table` WHERE log MATCH_ALL 'Reliable RPC called out of limit' group by ns, ct LIMIT 1000",
			sql:  "SELECT __ext.io_kubernetes_pod_namespace AS ns, split_part(log, '|', 3) AS ct, count(*) FROM `table` WHERE log MATCH_ALL 'Reliable RPC called out of limit' GROUP BY ns, ct LIMIT 1000",
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
			sql: "SELECT JSON_EXTRACT_STRING(__ext, '$.io_kubernetes_pod_namespace') AS ns, split_part(log, '|', 3) AS ct, count(*) WHERE log MATCH_ALL 'Reliable RPC called out of limit' GROUP BY ns, ct LIMIT 1000",
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
			sql: `SELECT test_server_ip, COUNT(*) AS log_count WHERE log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal' GROUP BY test_server_ip LIMIT 1000`,
		},
		{
			name: "test-7",
			q:    `select field_1, field_2 where log match_phrase 'test' group by dim_1, dim_2`,
			sql:  `SELECT field_1, field_2 WHERE log match_phrase 'test' GROUP BY dim_1, dim_2`,
		},
		{
			name: "test-8",
			q:    `test error sql`,
			err:  fmt.Errorf("SQL 解析失败：test error sql"),
		},
		{
			name: "test-9",
			q:    `select_1 * from_1 where 1=1'`,
			err:  fmt.Errorf("SQL 解析失败：select_1 * from_1 where 1=1'"),
		},
		{
			name: "test-10",
			q:    `select pod_namespace, count(*) as _value from pod_namespace where city LIKE '%c%' and pod_namespace != 'pod_namespace_1' or (pod_namespace='5' or a > 4) group by serverIp, abc order by time limit 1000 offset 999`,
			sql:  `SELECT __ext.io_kubernetes_pod_namespace, count(*) AS _value FROM pod_namespace WHERE city LIKE '%c%' AND __ext.io_kubernetes_pod_namespace != 'pod_namespace_1' OR ( __ext.io_kubernetes_pod_namespace = '5' OR a > 4 ) GROUP BY test_server_ip, abc ORDER BY time LIMIT 1000 OFFSET 999`,
		},
		{
			name: "test-11",
			q:    `select * from t where (t match_phrase_prefix '%gg%')`,
			sql:  `SELECT * FROM t WHERE ( t match_phrase_prefix '%gg%' )`,
		},
		{
			name: "test-12",
			q:    `select * from t where (t match_phrase_prefix '%gg%' or t match_phrase '%gg%') and t != 'test'`,
			sql:  `SELECT * FROM t WHERE ( t match_phrase_prefix '%gg%' OR t match_phrase '%gg%' ) AND t != 'test'`,
		},
		{
			name: "test-13",
			q:    `select * from my_db where dim_1 = 'val_1' and (dim_2 = 'val_2' or dim_3 = 'val_3')`,
			sql:  `SELECT * FROM my_db WHERE dim_1 = 'val_1' AND ( dim_2 = 'val_2' OR dim_3 = 'val_3' )`,
		},
		{
			name: "test-14",
			q:    `select * from my_db where ((dim_1 = 'val_1' or dim_4 > 1) and (dim_2 = 'val_2' or (dim_3 = 'val_3' or t > 1)))`,
			sql:  `SELECT * FROM my_db WHERE ( ( dim_1 = 'val_1' OR dim_4 > 1 ) AND ( dim_2 = 'val_2' OR ( dim_3 = 'val_3' OR t > 1 ) ) )`,
		},
		{
			name: "test-15",
			q: `select
  CAST(__ext ['io_kubernetes_pod_namespace'] AS TEXT) as ns,
  split_part (
    split_part (split_part (log, 'Object:', 2), 'Func:', 1),
    ':',
    1
  ) as Obj,
  split_part (
    split_part (split_part (log, 'Object:', 2), 'Func:', 2),
    'BunchNum:',
    1
  ) as FuncName,
  max(
    cast(
      split_part (
        split_part (
          split_part (split_part (log, 'Object:', 2), 'Func:', 2),
          'BunchNum:',
          2
        ),
        ' exceed',
        1
      ) as bigint
    )
  ) as BNum
group by
  ns,
  Obj,
  FuncName limit 10000`,
			sql: `SELECT CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) AS ns, split_part(split_part(split_part(log, 'Object:', 2), 'Func:', 1), ':', 1) AS Obj, split_part(split_part(split_part(log, 'Object:', 2), 'Func:', 2), 'BunchNum:', 1) AS FuncName, max(CAST(split_part(split_part(split_part(split_part(log, 'Object:', 2), 'Func:', 2), 'BunchNum:', 2), ' exceed', 1) AS bigint)) AS BNum GROUP BY ns, Obj, FuncName LIMIT 10000`,
		},
		{
			name: "test-17",
			q: `SELECT max(
    cast(
      split_part (
        split_part (
          split_part (split_part (log, 'Object:', 2), 'Func:', 2),
          'BunchNum:',
          2
        ),
        ' exceed',
        1
      ) as bigint
    )
  ) as BNum`,
			sql: `SELECT max(CAST(split_part(split_part(split_part(split_part(log, 'Object:', 2), 'Func:', 2), 'BunchNum:', 2), ' exceed', 1) AS bigint)) AS BNum`,
		},
		{
			name: "test-16",
			q:    `SELECT * WHERE CAST(__ext['io_kubernetes_pod_namespace']['extra.name'] AS TEXT) != 'test' AND abc != 'test'`,
			sql:  `SELECT * WHERE CAST(__ext['io_kubernetes_pod_namespace']['extra.name'] AS TEXT) != 'test' AND abc != 'test'`,
		},
		{
			name: "test-17",
			q:    "SELECT * WHERE name IN ('test', 'test-1')",
			sql:  "SELECT * WHERE name IN ('test','test-1')",
		},
	}

	mock.Init()
	fieldAlias := map[string]string{
		"pod_namespace": "__ext.io_kubernetes_pod_namespace",
		"serverIp":      "test_server_ip",
	}

	ctx := context.Background()
	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			// antlr4 and visitor
			opt := DorisListenerOption{
				DimensionTransform: func(s string) string {
					if _, ok := fieldAlias[s]; ok {
						return fieldAlias[s]
					}
					return s
				},
			}
			listener := ParseDorisSQL(ctx, c.q, opt)
			expected, err := listener.SQL()
			if c.err != nil {
				assert.Equal(t, c.err, err)
			} else {
				assert.Nil(t, err)
				assert.NotEmpty(t, expected)
				assert.Equal(t, c.sql, expected)
			}
		})
	}
}
