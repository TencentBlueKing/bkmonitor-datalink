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

func TestParseDorisSQLWithListener(t *testing.T) {
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
			sql: `SELECT __ext.io_kubernetes_pod_namespace AS pod_namespace, count(*) AS log_count FROM t_table WHERE log MATCH_PHRASE 'Error' OR test_server_ip MATCH_PHRASE 'Fatal' GROUP BY test_server_ip ORDER BY __ext.io_kubernetes_pod_namespace LIMIT 1000`,
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
			sql: `SELECT test_server_ip AS serverIp, COUNT(*) AS log_count WHERE log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal' GROUP BY test_server_ip LIMIT 1000`,
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
			sql:  `SELECT __ext.io_kubernetes_pod_namespace AS pod_namespace, count(*) AS _value FROM pod_namespace WHERE city LIKE '%c%' AND __ext.io_kubernetes_pod_namespace != 'pod_namespace_1' OR ( __ext.io_kubernetes_pod_namespace = '5' OR a > 4 ) GROUP BY test_server_ip, abc ORDER BY time LIMIT 1000 OFFSET 999`,
		},
		{
			name: "test-10-1",
			q:    `select pod_namespace AS ns, count(*) as _value from pod_namespace where city LIKE '%c%' and pod_namespace != 'pod_namespace_1' or (pod_namespace='5' or a > 4) group by serverIp, abc order by time limit 1000 offset 999`,
			sql:  `SELECT __ext.io_kubernetes_pod_namespace AS ns, count(*) AS _value FROM pod_namespace WHERE city LIKE '%c%' AND __ext.io_kubernetes_pod_namespace != 'pod_namespace_1' OR ( __ext.io_kubernetes_pod_namespace = '5' OR a > 4 ) GROUP BY test_server_ip, abc ORDER BY time LIMIT 1000 OFFSET 999`,
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
		{
			name: "test-18",
			q:    "SELECT a,b",
			sql:  "SELECT a, b",
		},
		{
			name: "test-18",
			q:    "SELECT a.b.c,b.a WHERE a.b.c != 'test' or b.a != 'test' group by a.b, b.a order by a.b",
			sql:  "SELECT a.b.c, b.a WHERE a.b.c != 'test' OR b.a != 'test' GROUP BY a.b, b.a ORDER BY a.b",
		},
		{
			name: "test-19",
			q:    "SELECT * WHERE name IN ('test', 'test-1') ORDER BY time ASC, name desc limit 1000",
			sql:  "SELECT * WHERE name IN ('test','test-1') ORDER BY time ASC, name DESC LIMIT 1000",
		},
		{
			name: "test-20",
			q:    "SELECT * WHERE name IN ('test', 'test-1') ORDER BY time desc, name limit 1000",
			sql:  "SELECT * WHERE name IN ('test','test-1') ORDER BY time DESC, name LIMIT 1000",
		},
		// listener 模式的实现不支持表达式计算解析，改用 visitor 模式
		//{
		//	name: "test-21",
		//	q:    "select ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)) OVER(), 2) as ct, CAST(__ext['cluster']['extra.name_space'] AS TEXT) AS ns, COUNT() / (SELECT COUNT()) AS pct",
		//	sql:  "",
		//},
		{
			name: "test-22",
			q:    "SELECT namespace, workload, COUNT() GROUP BY namespace, workload",
			sql:  "SELECT namespace, workload, COUNT() GROUP BY namespace, workload",
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
				DimensionTransform: func(s string) (string, string) {
					if _, ok := fieldAlias[s]; ok {
						return fieldAlias[s], s
					}
					return s, ""
				},
			}
			listener := ParseDorisSQLWithListener(ctx, c.q, opt)
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

func TestParseDorisSQLWithVisitor(t *testing.T) {
	testCases := []struct {
		name string
		q    string

		sql    string
		limit  int
		err    error
		offset int
	}{
		// 用法验证
		{
			name: "DS-单帧RPC超限-Func名字聚合",
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
			name: "错误日志上报IP分布",
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
			sql: "SELECT test_server_ip AS serverIp, COUNT(*) AS log_count WHERE log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal' GROUP BY test_server_ip LIMIT 1000",
		},
		{
			name: "Core日志 (近6个小时)",
			q:    `select path, count(*) as cnt group by path order by cnt desc limit 1000`,
			sql:  "SELECT path, count(*) AS cnt GROUP BY path ORDER BY cnt DESC LIMIT 1000",
		},
		{
			name: "DS定时执行STAT",
			q:    `select CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) as ns, count(*) as log_count group by ns limit 1000`,
			sql:  "SELECT CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) AS ns, count(*) AS log_count GROUP BY ns LIMIT 1000",
		},
		{
			name: "DS所有地图加载的LevelNb统计",
			q: `SELECT CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) as ns, split_part(split_part(log,'=',2), ' ', 1) as ct, split_part(split_part(log,'=',13),' ',1) as LvlNb
group by ns,ct,LvlNb order by LvlNb desc
LIMIT 10000`,
			sql: `SELECT CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) AS ns, split_part(split_part(log, '=', 2), ' ', 1) AS ct, split_part(split_part(log, '=', 13), ' ', 1) AS LvlNb GROUP BY ns, ct, LvlNb ORDER BY LvlNb DESC LIMIT 10000`,
		},
		{
			name: "DS流量统计",
			q:    `select minute1, sum(cast(substring_index(substring_index(log, '[', -1), ']', 1) as bigint)) group by minute1 limit 10000`,
			sql:  `SELECT minute1, sum(CAST(substring_index(substring_index(log, '[', -1), ']', 1) AS bigint)) GROUP BY minute1 LIMIT 10000`,
		},
		{
			name: "ensure统计",
			q:    `select array_join(array_slice(split_by_string(log, ':'), 4, cardinality(split_by_string(log, ':')) - 3), ':') as cat, count(*) group by cat limit 10000`,
			sql:  `SELECT array_join(array_slice(split_by_string(log, ':'), 4, cardinality(split_by_string(log, ':')) - 3), ':') AS cat, count(*) GROUP BY cat LIMIT 10000`,
		},
		{
			name: "RPC发送Bunch大包统计",
			q: `select
  CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) as ns,
  split_part (
    split_part (
      split_part (log, 'Object:', 2),
      'Func:',
      1
    ),
    ':',
    1
  ) as Obj,
  split_part (
    split_part (
      split_part (log, 'Object:', 2),
      'Func:',
      2
    ),
    'BunchNum:',
    1
  ) as FuncName,
  max(
    cast(
      split_part (
        split_part (
          split_part (
            split_part (log, 'Object:', 2),
            'Func:',
            2
          ),
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
			name: "函数调用STAT",
			q: `select
  CAST(__ext ['io_kubernetes_pod_namespace'] AS TEXT) as ns,
  count(*) as log_count
group by
  ns limit 1000`,
			sql: `SELECT CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) AS ns, count(*) AS log_count GROUP BY ns LIMIT 1000`,
		},
		{
			name: "建筑数量1",
			q:    `SELECT CAST(regexp_extract(log, 'FPzPieceActorData ([0-9]+)', 1) AS bigint) AS count, log ORDER BY count desc limit 10000`,
			sql:  `SELECT CAST(regexp_extract(log, 'FPzPieceActorData ([0-9]+)', 1) AS bigint) AS count, log ORDER BY count DESC LIMIT 10000`,
		},
		{
			name: "查找ENSURE-按ns/image/内存聚类",
			q: `select
  CAST(__ext ['io_kubernetes_pod_namespace'] AS TEXT) as ns,
  substr (CAST(__ext ['container_image'] AS TEXT), 20) as imn,
  substr (log, 53) as ct,
  count(*)
group by
  ns,
  imn,
  ct`,
			sql: `SELECT CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) AS ns, substr(CAST(__ext['container_image'] AS TEXT), 20) AS imn, substr(log, 53) AS ct, count(*) GROUP BY ns, imn, ct`,
		},
		{
			name: "查找GUID Duplicated-按照ns/类别聚合",
			q: `select
  CAST(__ext ['io_kubernetes_pod_namespace'] AS TEXT) as ns,
  split_part (split_part (log, 'name=', 3), '_', 1) as ct,
  count(*)
group by
  ns,
  ct`,
			sql: "SELECT CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) AS ns, split_part(split_part(log, 'name=', 3), '_', 1) AS ct, count(*) GROUP BY ns, ct",
		},
		{
			name: "真DS-单帧RPC超限-Func名字聚合",
			q:    `select CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) as ns, split_part(log,'|',3) as ct,count(*) group by ns,ct`,
			sql:  `SELECT CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) AS ns, split_part(log, '|', 3) AS ct, count(*) GROUP BY ns, ct`,
		},
		// CASE WHEN
		{
			name: "长tick统计",
			q: `SELECT
  floor(
    CAST(
      regexp_extract(log, 'FrameTime=([0-9.]+)ms', 1) AS DOUBLE
    ) / 100
  ) * 100 AS tick,
  COUNT(
    CASE WHEN 
      CAST(
        regexp_extract(log, 'FrameTime=([0-9.]+)ms', 1) AS DOUBLE
      ) > 200 
    THEN 1 ELSE NULL END
  ) AS cnt
GROUP BY
  tick
ORDER BY
  cnt DESC 
LIMIT 10000;`,
			sql: "SELECT floor(CAST(regexp_extract(log, 'FrameTime=([0-9.]+)ms', 1) AS DOUBLE) / 100) * 100 AS tick, COUNT(CASE WHEN CAST(regexp_extract(log, 'FrameTime=([0-9.]+)ms', 1) AS DOUBLE) > 200 THEN 1 ELSE NULL END) AS cnt GROUP BY tick ORDER BY cnt DESC LIMIT 10000",
		},

		// 自定义验证
		{
			name: "CASE WHEN 语法",
			q: `SELECT COUNT(
    CASE WHEN 
      CAST(
        regexp_extract(log, 'FrameTime=([0-9.]+)ms', 1) AS DOUBLE
      ) > 200 
    THEN 1 ELSE NULL END
  ) AS cnt`,
			sql: "SELECT COUNT(CASE WHEN CAST(regexp_extract(log, 'FrameTime=([0-9.]+)ms', 1) AS DOUBLE) > 200 THEN 1 ELSE NULL END) AS cnt",
		},
		{
			name: "函数参数嵌套表达式",
			q:    `select array_join(array_slice(split_by_string(log, ':'), 4, cardinality(split_by_string(log, ':'))), ':') as cat`,
			sql:  `SELECT array_join(array_slice(split_by_string(log, ':'), 4, cardinality(split_by_string(log, ':'))), ':') AS cat`,
		},
		{
			name: "test-2",
			q:    `SELECT DISTINCT(regexp_extract(log, 'openid:(\\d+)', 1)) AS id LIMIT 100000`,
			sql:  `SELECT DISTINCT regexp_extract(log, 'openid:(\\d+)', 1) AS id LIMIT 100000`,
		},
		{
			name: "test-1",
			q: `select pod_namespace, 
count(*) AS log_count 
from t_table 
where log MATCH_PHRASE 'Error' OR serverIp MATCH_PHRASE 'Fatal' GROUP BY serverIp order by pod_namespace LIMIT 1000`,
			sql: `SELECT __ext.io_kubernetes_pod_namespace AS pod_namespace, count(*) AS log_count FROM t_table WHERE log MATCH_PHRASE 'Error' OR test_server_ip MATCH_PHRASE 'Fatal' GROUP BY test_server_ip ORDER BY __ext.io_kubernetes_pod_namespace LIMIT 1000`,
		},
		{
			name: "test-2",
			q:    `show TABLES`,
			err:  fmt.Errorf("parse doris sql (show TABLES) error: show TABLES"),
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
			sql: `SELECT test_server_ip AS serverIp, COUNT(*) AS log_count WHERE log MATCH_PHRASE 'Error' OR log MATCH_PHRASE 'Fatal' GROUP BY test_server_ip LIMIT 1000`,
		},
		{
			name: "test-7",
			q:    `select field_1, field_2 where log match_phrase 'test' group by dim_1, dim_2`,
			sql:  `SELECT field_1, field_2 WHERE log match_phrase 'test' GROUP BY dim_1, dim_2`,
		},
		{
			name: "test-8",
			q:    `test error sql`,
			err:  fmt.Errorf("parse doris sql (test error sql) error: test error sql"),
		},
		{
			name: "test-9",
			q:    `select_1 * from_1 where 1=1'`,
			err:  fmt.Errorf("parse doris sql (select_1 * from_1 where 1=1') error: select_1 * from_1 where 1 = 1 '"),
		},
		{
			name: "test-10",
			q:    `select pod_namespace, count(*) as _value from pod_namespace where city LIKE '%c%' and pod_namespace != 'pod_namespace_1' or (pod_namespace='5' or a > 4) group by serverIp, abc order by time limit 1000 offset 999`,
			sql:  `SELECT __ext.io_kubernetes_pod_namespace AS pod_namespace, count(*) AS _value FROM pod_namespace WHERE city LIKE '%c%' AND __ext.io_kubernetes_pod_namespace != 'pod_namespace_1' OR ( __ext.io_kubernetes_pod_namespace = '5' OR a > 4 ) GROUP BY test_server_ip, abc ORDER BY time LIMIT 1000 OFFSET 999`,
		},
		{
			name: "test-10-1",
			q:    `select pod_namespace AS ns, count(*) as _value from pod_namespace where city LIKE '%c%' and pod_namespace != 'pod_namespace_1' or (pod_namespace='5' or a > 4) group by serverIp, abc order by time limit 1000 offset 999`,
			sql:  `SELECT __ext.io_kubernetes_pod_namespace AS ns, count(*) AS _value FROM pod_namespace WHERE city LIKE '%c%' AND __ext.io_kubernetes_pod_namespace != 'pod_namespace_1' OR ( __ext.io_kubernetes_pod_namespace = '5' OR a > 4 ) GROUP BY test_server_ip, abc ORDER BY time LIMIT 1000 OFFSET 999`,
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
			sql:  "SELECT * WHERE name IN ('test', 'test-1')",
		},
		{
			name: "test-18",
			q:    "SELECT a,b",
			sql:  "SELECT a, b",
		},
		{
			name: "test-18",
			q:    "SELECT a.b.c,b.a WHERE a.b.c != 'test' or b.a != 'test' group by a.b, b.a order by a.b",
			sql:  "SELECT a.b.c, b.a WHERE a.b.c != 'test' OR b.a != 'test' GROUP BY a.b, b.a ORDER BY a.b",
		},
		{
			name: "test-19",
			q:    "SELECT * WHERE name = '1' and a > 2",
			sql:  "SELECT * WHERE name = '1' AND a > 2",
		},
		{
			name: "test-20",
			q:    "SELECT * WHERE name IN ('test', 'test-1') ORDER BY time desc, name limit 1000",
			sql:  "SELECT * WHERE name IN ('test', 'test-1') ORDER BY time DESC, name LIMIT 1000",
		},
		{
			name: "子查询验证",
			q:    "select ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)), 2) as ct, CAST(__ext['cluster']['extra.name_space'] AS TEXT) AS ns, COUNT() / (SELECT COUNT()) AS pct",
			sql:  "SELECT ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)), 2) AS ct, CAST(__ext['cluster']['extra.name_space'] AS TEXT) AS ns, COUNT() / (SELECT COUNT()) AS pct",
		},
		{
			name: "子查询验证 - 1",
			q:    "select ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)), 2) as ct, CAST(__ext['cluster']['extra.name_space'] AS TEXT) AS ns, COUNT() / (SELECT COUNT() where a > 1 limit 1) AS pct",
			sql:  "SELECT ROUND(COUNT(*) * 100.0 / SUM(COUNT(*)), 2) AS ct, CAST(__ext['cluster']['extra.name_space'] AS TEXT) AS ns, COUNT() / (SELECT COUNT() WHERE a > 1 LIMIT 1) AS pct",
		},
		{
			name: "查询值支持函数模式",
			q:    "SELECT * WHERE LOWER(log) REGEXP LOWER('LogPzRealm')",
			sql:  "SELECT * WHERE LOWER(log) REGEXP LOWER('LogPzRealm')",
		},
		{
			name: "反正则查询",
			q:    "SELECT * WHERE log NOT REGEXP 'Operation aborted.' ORDER BY dtEventTimeStamp DESC, gseIndex DESC, iterationIndex DESC LIMIT 100 OFFSET 0",
			sql:  "SELECT * WHERE log NOT REGEXP 'Operation aborted.' ORDER BY dtEventTimeStamp DESC, gseIndex DESC, iterationIndex DESC LIMIT 100 OFFSET 0",
		},
		{
			name: "test-22",
			q:    "SELECT namespace, workload as t1, COUNT()",
			sql:  "SELECT namespace, workload AS t1, COUNT()",
		},
		{
			name: "test-23",
			q:    "SELECT COUNT()",
			sql:  "SELECT COUNT()",
		},
		{
			name: "test-24",
			q:    "SELECT COUNT(*)",
			sql:  "SELECT COUNT(*)",
		},
		{
			name: "test-25",
			q:    "SELECT a.b.c",
			sql:  "SELECT a.b.c",
		},
		{
			name: "test-26",
			q:    "SELECT SUM(COUNT(*))",
			sql:  "SELECT SUM(COUNT(*))",
		},
		{
			name: "test-27",
			q:    "SELECT COUNT(test) AS nt limit 1000 offset 10",
			sql:  "SELECT COUNT(test) AS nt LIMIT 1000 OFFSET 10",
		},
		{
			name: "test-28",
			q:    "SELECT __ext.cluster.extra.name_space",
			sql:  "SELECT __ext.cluster.extra.name_space",
		},
		{
			name: "test-28",
			q:    "SELECT __ext['cluster']['extra.name_space']",
			sql:  "SELECT __ext['cluster']['extra.name_space']",
		},
		{
			name: "test-29",
			q:    `SELECT CAST(__ext['cluster']['extra.name_space'] as text)`,
			sql:  `SELECT CAST(__ext['cluster']['extra.name_space'] AS text)`,
		},
		{
			name: "test-30",
			q:    `SELECT COUNT(CAST(__ext['cluster']['extra.name_space'] AS TEXT)) AS nt, CAST(split_part(log, 'Object:', 2) AS TEXT) AS ns`,
			sql:  `SELECT COUNT(CAST(__ext['cluster']['extra.name_space'] AS TEXT)) AS nt, CAST(split_part(log, 'Object:', 2) AS TEXT) AS ns`,
		},
		{
			name: "test-31",
			q:    `select a- b`,
			sql:  `SELECT a - b`,
		},
		{
			name: "test-32",
			q:    `select count(a)/count(b)`,
			sql:  `SELECT count(a) / count(b)`,
		},
		{
			name: "test-33",
			q:    `select count(a)*100.0`,
			sql:  `SELECT count(a) * 100.0`,
		},
		{
			name: "test-34",
			q: `SELECT regexp_extract(log, 'FPzPieceActorData ([0-9]+)', 1) AS count, log 
ORDER BY cast(count AS bigint) desc, item asc limit 10000`,
			sql: `SELECT regexp_extract(log, 'FPzPieceActorData ([0-9]+)', 1) AS count, log ORDER BY CAST(count AS bigint) DESC, item ASC LIMIT 10000`,
		},
		{
			name: `test-35`,
			q:    `SELECT DEPLOYMENT AS t, aaa as t1 from my_bro`,
			sql:  `SELECT DEPLOYMENT AS t, aaa AS t1 FROM my_bro`,
		},
		{
			name: `test-36`,
			q: `select
  CAST(__ext ['io_kubernetes_pod_namespace'] AS TEXT) as ns,
  substr (CAST(__ext ['container_image'] AS TEXT), 20) as imn,
  substr (log, 53) as ct,
  count(*)
group by
  ns,
  imn,
  ct`,
			sql: `SELECT CAST(__ext['io_kubernetes_pod_namespace'] AS TEXT) AS ns, substr(CAST(__ext['container_image'] AS TEXT), 20) AS imn, substr(log, 53) AS ct, count(*) GROUP BY ns, imn, ct`,
		},
		{
			name: `test-37`,
			q:    `select count() where log like 'test*'`,
			sql:  `SELECT count() WHERE log like 'test*'`,
		},
		{
			name: `test-38`,
			q:    `select count() where (log like 'test*')`,
			sql:  `SELECT count() WHERE ( log like 'test*' )`,
		},
		{
			name: `test-39`,
			q:    `select pod_namespace where pod_namespace != ''`,
			sql:  `SELECT __ext.io_kubernetes_pod_namespace AS pod_namespace WHERE __ext.io_kubernetes_pod_namespace != ''`,
		},
		{
			name: `test-40`,
			q:    `select count(pod_namespace) where log like 'test*'`,
			sql:  `SELECT count(__ext.io_kubernetes_pod_namespace) WHERE log like 'test*'`,
		},
		{
			name: `test-41`,
			q:    `select DISTINCT(a),b`,
			sql:  `SELECT DISTINCT(a), b`,
		},
		{
			name: `test-42`,
			q:    `SELECT DISTINCT a`,
			sql:  `SELECT DISTINCT(a)`,
		},
		{
			name: `test-43`,
			q: `SELECT
  COUNT(
    DISTINCT (
      cast(
        regexp_extract (log, 'openid=(\\d+)', 1) AS bigint
      )
    )
  ) AS openid`,
			sql: `SELECT COUNT(DISTINCT(CAST(regexp_extract(log, 'openid=(\\d+)', 1) AS bigint))) AS openid`,
		},
		{
			name: `outer-limit`,
			q: `SELECT
  COUNT(
    DISTINCT (
      cast(
        regexp_extract (log, 'openid=(\\d+)', 1) AS bigint
      )
    )
  ) AS openid`,
			limit:  100,
			offset: 10,
			sql:    `SELECT COUNT(DISTINCT(CAST(regexp_extract(log, 'openid=(\\d+)', 1) AS bigint))) AS openid OFFSET 10 LIMIT 100`,
		},
		{
			name: `outer-limit`,
			q: `SELECT
  COUNT(
    DISTINCT (
      cast(
        regexp_extract (log, 'openid=(\\d+)', 1) AS bigint
      )
    )
  ) AS openid LIMIT 200`,
			limit:  100,
			offset: 10,
			sql:    `SELECT COUNT(DISTINCT(CAST(regexp_extract(log, 'openid=(\\d+)', 1) AS bigint))) AS openid OFFSET 10 LIMIT 200`, // 如果SQL中指定了Limit应该进行保留.并且选择更大的
		},
		{
			name: `outer-limit-bigger`,
			q: `SELECT
  COUNT(
    DISTINCT (
      cast(
        regexp_extract (log, 'openid=(\\d+)', 1) AS bigint
      )
    )
  ) AS openid LIMIT 200`,
			limit:  300,
			offset: 10,
			sql:    `SELECT COUNT(DISTINCT(CAST(regexp_extract(log, 'openid=(\\d+)', 1) AS bigint))) AS openid OFFSET 10 LIMIT 300`, // 如果传递进来的limit更大则进行覆盖
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
			opt := &Option{
				DimensionTransform: func(s string) (string, string) {
					if _, ok := fieldAlias[s]; ok {
						return fieldAlias[s], s
					}
					return s, ""
				},
				Limit:  c.limit,
				Offset: c.offset,
			}
			sql, err := ParseDorisSQLWithVisitor(ctx, c.q, opt)
			if c.err != nil {
				assert.Equal(t, c.err, err)
			} else {
				assert.Nil(t, err)
				assert.NotEmpty(t, sql)
				assert.Equal(t, c.sql, sql)
			}
		})
	}
}
