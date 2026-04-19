// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

var fieldEncodeFunc = func(s string) string {
	fs := strings.Split(s, ".")
	if len(fs) == 1 {
		s = fmt.Sprintf("`%s`", s)
		return s
	}

	var (
		suffixFields strings.Builder
		// 协议自定义是 map 结构
		sep string
	)

	mapFieldSet := set.New[string]([]string{"resource", "attributes"}...)
	for index, f := range fs {
		// 第一个补充开头
		if index == 0 {
			sep = `['`
		} else if index == len(fs)-1 {
			// 最后一个不需要补充
			sep = `']`
		}

		suffixFields.WriteString(f + sep)
		// 用户上报的分隔符为 .
		if mapFieldSet.Existed(f) {
			sep = "."
		} else if sep != "." {
			sep = "']['"
		}
	}

	s = fmt.Sprintf(`CAST(%s AS %s)`, suffixFields.String(), "STRING")
	return s
}

func TestDorisSQLExpr_ParserQueryString(t *testing.T) {
	tests := []struct {
		name  string
		input string
		sql   string
		dsl   string
		err   string
	}{
		{
			name:  "simple match",
			input: "name:test",
			sql:   "`name` = 'test'",
			dsl:   `{"term":{"name":"test"}}`,
		},
		{
			name:  "one word",
			input: "test",
			sql:   "`log` MATCH_PHRASE 'test'",
			dsl:   `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test"}}`,
		},
		{
			name:  "complex nested query",
			input: "(a:1 AND (b:2 OR c:3)) OR NOT d:4",
			sql:   "(`a` = '1' AND (`b` = '2' OR `c` = '3')) OR `d` != '4'",
			dsl:   `{"bool":{"should":[{"bool":{"must":[{"term":{"a":"1"}},{"bool":{"should":[{"term":{"b":"2"}},{"term":{"c":"3"}}]}}]}},{"bool":{"must_not":{"term":{"d":"4"}}}}]}}`,
		},
		{
			name:  "invalid syntax",
			input: "name:test AND OR",
			sql:   "`name` = 'test' AND `log` MATCH_PHRASE 'OR'",
			dsl:   `{"bool":{"must":[{"term":{"name":"test"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"OR"}}]}}`,
		},
		{
			name:  "empty input",
			input: "",
			dsl:   "",
		},
		{
			name:  "OR expression with multiple terms",
			input: "(a:1 OR b:2) AND c:3",
			sql:   "(`a` = '1' OR `b` = '2') AND `c` = '3'",
			dsl:   `{"bool":{"must":[{"bool":{"should":[{"term":{"a":"1"}},{"term":{"b":"2"}}]}},{"term":{"c":"3"}}]}}`,
		},
		{
			name:  "mixed AND/OR with proper precedence",
			input: "a:1 AND b:2 OR c:3",
			sql:   "`a` = '1' AND `b` = '2' OR `c` = '3'",
			dsl:   `{"bool":{"must":[{"term":{"a":"1"}},{"term":{"b":"2"}}],"should":{"term":{"c":"3"}}}}`,
		},
		{
			name:  "mixed AND/OR with proper precedence -1",
			input: "a:1 AND (b:2 OR c:3)",
			sql:   "`a` = '1' AND (`b` = '2' OR `c` = '3')",
			dsl:   `{"bool":{"must":[{"term":{"a":"1"}},{"bool":{"should":[{"term":{"b":"2"}},{"term":{"c":"3"}}]}}]}}`,
		},
		{
			name:  "exact match with quotes",
			input: "name:\"exact match\"",
			sql:   "`name` = 'exact match'",
			dsl:   `{"term":{"name":"exact match"}}`,
		},
		{
			name:  "numeric equality",
			input: "age:25",
			sql:   "`age` = '25'",
			dsl:   `{"term":{"age":"25"}}`,
		},
		{
			name:  "date range query",
			input: "timestamp:[2023-01-01 TO 2023-12-31]",
			sql:   "`timestamp` >= '2023-01-01' AND `timestamp` <= '2023-12-31'",
			dsl:   `{"range":{"timestamp":{"from":"2023-01-01","include_lower":true,"include_upper":true,"to":"2023-12-31"}}}`,
		},
		{
			name:  "date range query - 1",
			input: "count:[1 TO 10}",
			sql:   "`count` >= '1' AND `count` < '10'",
			dsl:   `{"range":{"count":{"from":1,"include_lower":true,"include_upper":false,"to":10}}}`,
		},
		{
			name:  "date range query - 2",
			input: "count:{10 TO *]",
			sql:   "`count` > '10'",
			dsl:   `{"range":{"count":{"from":10,"include_lower":false,"include_upper":true,"to":null}}}`,
		},
		{
			name:  "invalid field name",
			input: "123field:value",
			sql:   "`123field` = 'value'",
			dsl:   `{"term":{"123field":"value"}}`,
		},
		{
			name:  "text filter",
			input: "text:value",
			sql:   "`text` = 'value'",
			dsl:   `{"term":{"text":"value"}}`,
		},
		{
			name:  "object field",
			input: "__ext.container_name: value",
			sql:   "CAST(__ext['container_name'] AS STRING) = 'value'",
			dsl:   `{"term":{"__ext.container_name":"value"}}`,
		},
		{
			name:  "object field and alias",
			input: "container_name: value",
			sql:   "CAST(__ext['container_name'] AS STRING) = 'value'",
			dsl:   `{"term":{"__ext.container_name":"value"}}`,
		},
		{
			name:  "start",
			input: "a: >100",
			sql:   "`a` > '100'",
			dsl:   `{"range":{"a":{"from":100,"include_lower":false,"include_upper":true,"to":null}}}`,
		},
		{
			name:  "+level:info AND -iterationIndex:[4 TO 8] NOT iterationIndex:9",
			input: "+level:info AND -iterationIndex:[4 TO 8] NOT iterationIndex:9",
			sql:   "`iterationIndex` >= '4' AND `iterationIndex` <= '8' AND `level` = 'info' AND `iterationIndex` != '9'",
			dsl:   `{"bool":{"must":[{"term":{"level":"info"}},{"bool":{"must_not":{"range":{"iterationIndex":{"from":4,"include_lower":true,"include_upper":true,"to":8}}}}},{"bool":{"must_not":{"term":{"iterationIndex":"9"}}}}]}}`,
		},
		{
			name:  "nested query",
			input: "event.name: test",
			sql:   "CAST(event['name'] AS STRING) = 'test'",
			dsl:   `{"nested":{"path":"event","query":{"term":{"event.name":"test"}}}}`,
		},
		{
			name:  "test-1",
			input: `__ext.io_kubernetes_pod_namespace: "gfp-online-livepy-b" AND is_data_valid: "1" AND born_dist_to_recipient: <400 AND recipient_exists: "1" AND bot_dead_reason: (*killed_by_unknown* OR *bot_without_injury*) AND approach_succ: "0" AND approach_curr_recipient_frame: >200 AND in_fight_aggressive_ratio: "0"`,
			sql:   "CAST(__ext['io_kubernetes_pod_namespace'] AS STRING) = 'gfp-online-livepy-b' AND `is_data_valid` = '1' AND `born_dist_to_recipient` < '400' AND `recipient_exists` = '1' AND (`bot_dead_reason` LIKE '%killed_by_unknown%' OR `bot_dead_reason` LIKE '%bot_without_injury%') AND `approach_succ` = '0' AND `approach_curr_recipient_frame` > '200' AND `in_fight_aggressive_ratio` = '0'",
			dsl:   `{"bool":{"must":[{"term":{"__ext.io_kubernetes_pod_namespace":"gfp-online-livepy-b"}},{"term":{"is_data_valid":"1"}},{"range":{"born_dist_to_recipient":{"from":null,"include_lower":true,"include_upper":false,"to":400}}},{"term":{"recipient_exists":"1"}},{"bool":{"should":[{"wildcard":{"bot_dead_reason":{"value":"*killed_by_unknown*"}}},{"wildcard":{"bot_dead_reason":{"value":"*bot_without_injury*"}}}]}},{"term":{"approach_succ":"0"}},{"range":{"approach_curr_recipient_frame":{"from":200,"include_lower":false,"include_upper":true,"to":null}}},{"term":{"in_fight_aggressive_ratio":"0"}}]}}`,
		},
		{
			name:  "not + or",
			input: `NOT log: ("a" OR "b")`,
			dsl:   `{"bool":{"must_not":{"bool":{"should":[{"match_phrase":{"log":{"query":"a"}}},{"match_phrase":{"log":{"query":"b"}}}]}}}}`,
			sql:   "NOT (`log` MATCH_PHRASE 'a' OR `log` MATCH_PHRASE 'b')",
		},
		{
			name:  "gantanhao",
			input: `!"WorldAttrPool free num (15) less than"`,
			dsl:   `{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"WorldAttrPool free num (15) less than\""}}}}`,
			sql:   "`log` NOT MATCH_PHRASE 'WorldAttrPool free num (15) less than'",
		},
		{
			name:  "wildcard on analyzed field should lowercase",
			input: "log: *TSpiderCreateTableException*",
			sql:   "`log` LIKE '%TSpiderCreateTableException%'",
			dsl:   `{"wildcard":{"log":{"value":"*tspidercreatetableexception*"}}}`,
		},
		{
			name:  "wildcard on non-analyzed field keeps case",
			input: "status: *Active*",
			sql:   "`status` LIKE '%Active%'",
			dsl:   `{"wildcard":{"status":{"value":"*Active*"}}}`,
		},
	}

	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	fieldsMap := metadata.FieldsMap{
		"log": {
			IsAnalyzed: true,
			FieldType:  "text",
		},
		"__ext.container_name": {
			AliasName: "container_name",
			FieldType: "text",
		},
		"author": {
			IsAnalyzed: true,
		},
		"event.name": {
			AliasName:   "event_name",
			OriginField: "event",
			FieldType:   "text",
		},
		"event": {
			FieldType: "nested",
		},
	}
	aliasMap := make(map[string]string)
	for k, o := range fieldsMap {
		if o.AliasName == "" {
			continue
		}
		aliasMap[o.AliasName] = k
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)

			// 解析 sql
			node := ParseLuceneWithVisitor(ctx, tt.input, Option{
				FieldsMap:       fieldsMap,
				FieldEncodeFunc: fieldEncodeFunc,
			})
			assert.Nil(t, node.Error())
			assert.Equal(t, tt.sql, node.String())

			// 解析  dsl
			node = ParseLuceneWithVisitor(ctx, tt.input, Option{
				FieldsMap: fieldsMap,
			})
			source := MergeQuery(node.DSL())

			if source != nil {
				s, err := source.Source()
				assert.Nil(t, err)

				dsl, _ := json.Marshal(s)
				assert.Equal(t, tt.dsl, string(dsl))
			} else {
				assert.Equal(t, tt.dsl, "")
			}
		})
	}
}

var OpMatch = &StringNode{Value: "="}

func TestLuceneParser(t *testing.T) {
	testCases := map[string]struct {
		q   string
		es  string
		sql string
	}{
		"正常查询": {
			q:   `test`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test"}}`,
			sql: "`log` MATCH_PHRASE 'test'",
		},
		"负数查询": {
			q:   `-test`,
			es:  `{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test"}}}}`,
			sql: "`log` NOT MATCH_PHRASE 'test'",
		},
		"负数查询多条件": {
			q:   `-test AND good`,
			es:  `{"bool":{"must":[{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test"}}}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"good"}}]}}`,
			sql: "`log` MATCH_PHRASE 'good' AND `log` NOT MATCH_PHRASE 'test'",
		},
		"通配符匹配": {
			q:   `qu?ck bro*`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"qu?ck"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"bro*"}}]}}`,
			sql: "`log` LIKE 'qu_ck' OR `log` LIKE 'bro%'",
		},
		"无条件正则匹配": {
			q:   `/joh?n(ath[oa]n)/`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"/joh?n(ath[oa]n)/"}}`,
			sql: "`log` REGEXP 'joh?n(ath[oa]n)'",
		},
		"正则匹配": {
			q:   `name: /joh?n(ath[oa]n)/`,
			es:  `{"regexp":{"name":{"value":"joh?n(ath[oa]n)"}}}`,
			sql: "`name` REGEXP 'joh?n(ath[oa]n)'",
		},
		"范围匹配，左闭右开": {
			q:   `count:[1 TO 5}`,
			es:  `{"range":{"count":{"from":1,"include_lower":true,"include_upper":false,"to":5}}}`,
			sql: "`count` >= '1' AND `count` < '5'",
		},
		"范围匹配": {
			q:   `count:[1 TO 5]`,
			es:  `{"range":{"count":{"from":1,"include_lower":true,"include_upper":true,"to":5}}}`,
			sql: "`count` >= '1' AND `count` <= '5'",
		},
		"范围匹配（无下限） - 1": {
			q:   `count:{* TO 10]`,
			es:  `{"range":{"count":{"from":null,"include_lower":false,"include_upper":true,"to":10}}}`,
			sql: "`count` <= '10'",
		},
		"范围匹配（无下限）": {
			q: `count:[* TO 10]`,

			es:  `{"range":{"count":{"from":null,"include_lower":true,"include_upper":true,"to":10}}}`,
			sql: "`count` <= '10'",
		},
		"范围匹配（无上限）": {
			q: `count:[10 TO *]`,

			es:  `{"range":{"count":{"from":10,"include_lower":true,"include_upper":true,"to":null}}}`,
			sql: "`count` >= '10'",
		},
		"范围匹配（无上限）- 1": {
			q:   `count:[10 TO *}`,
			es:  `{"range":{"count":{"from":10,"include_lower":true,"include_upper":false,"to":null}}}`,
			sql: "`count` >= '10'",
		},
		"字段匹配": {
			q:   `status:active`,
			es:  `{"term":{"status":"active"}}`,
			sql: "`status` = 'active'",
		},
		"字段匹配 + 括号": {
			q:   `status:(active)`,
			es:  `{"term":{"status":"active"}}`,
			sql: "(`status` = 'active')",
		},
		"多条件组合，括号调整优先级": {
			q:   `author:"John Smith" AND (age:20 OR status:active)`,
			es:  `{"bool":{"must":[{"match_phrase":{"author":{"query":"John Smith"}}},{"bool":{"should":[{"term":{"age":"20"}},{"term":{"status":"active"}}]}}]}}`,
			sql: "`author` MATCH_PHRASE 'John Smith' AND (`age` = '20' OR `status` = 'active')",
		},
		"多条件组合，and 和 or 的优先级": {
			q:   `(author:"John Smith" AND age:20) OR status:active`,
			es:  `{"bool":{"should":[{"bool":{"must":[{"match_phrase":{"author":{"query":"John Smith"}}},{"term":{"age":"20"}}]}},{"term":{"status":"active"}}]}}`,
			sql: "(`author` MATCH_PHRASE 'John Smith' AND `age` = '20') OR `status` = 'active'",
		},
		"嵌套逻辑表达式": {
			q:   `a:1 AND (b:2 OR c:3)`,
			es:  `{"bool":{"must":[{"term":{"a":"1"}},{"bool":{"should":[{"term":{"b":"2"}},{"term":{"c":"3"}}]}}]}}`,
			sql: "`a` = '1' AND (`b` = '2' OR `c` = '3')",
		},
		"嵌套逻辑表达式 - 2": {
			q:   `a:1 OR b:2 OR (c:3 OR d:4)`,
			es:  `{"bool":{"should":[{"term":{"a":"1"}},{"term":{"b":"2"}},{"bool":{"should":[{"term":{"c":"3"}},{"term":{"d":"4"}}]}}]}}`,
			sql: "`a` = '1' OR `b` = '2' OR (`c` = '3' OR `d` = '4')",
		},
		"嵌套逻辑表达式 - 3": {
			q:   `a:1 OR (b:2 OR c:3) OR d:4`,
			es:  `{"bool":{"should":[{"term":{"a":"1"}},{"bool":{"should":[{"term":{"b":"2"}},{"term":{"c":"3"}}]}},{"term":{"d":"4"}}]}}`,
			sql: "`a` = '1' OR (`b` = '2' OR `c` = '3') OR `d` = '4'",
		},
		"嵌套逻辑表达式 - 4": {
			q:   `a:1 OR (b:2 OR c:3) AND d:4`,
			es:  `{"bool":{"must":{"term":{"d":"4"}},"should":[{"term":{"a":"1"}},{"bool":{"should":[{"term":{"b":"2"}},{"term":{"c":"3"}}]}}]}}`,
			sql: "`a` = '1' OR (`b` = '2' OR `c` = '3') AND `d` = '4'",
		},
		"new-1": {
			q:   `quick brown +fox -news`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"fox"}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"news"}}}}],"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"quick"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"brown"}}]}}`,
			sql: "`log` MATCH_PHRASE 'quick' AND `log` MATCH_PHRASE 'fox' AND `log` NOT MATCH_PHRASE 'news' OR `log` MATCH_PHRASE 'brown' AND `log` MATCH_PHRASE 'fox' AND `log` NOT MATCH_PHRASE 'news' OR `log` MATCH_PHRASE 'fox' AND `log` NOT MATCH_PHRASE 'news'",
		},
		"new-2": {
			q:   `quick -news`,
			es:  `{"bool":{"must":{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"news"}}}},"should":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"quick"}}}}`,
			sql: "`log` MATCH_PHRASE 'quick' AND `log` NOT MATCH_PHRASE 'news' OR `log` NOT MATCH_PHRASE 'news'",
		},
		"模糊匹配": {
			q: `quick brown fox`,

			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"quick"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"brown"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"fox"}}]}}`,
			sql: "`log` MATCH_PHRASE 'quick' OR `log` MATCH_PHRASE 'brown' OR `log` MATCH_PHRASE 'fox'",
		},
		"单个条件精确匹配": {
			q: `log: "ERROR MSG"`,

			es:  `{"match_phrase":{"log":{"query":"ERROR MSG"}}}`,
			sql: "`log` MATCH_PHRASE 'ERROR MSG'",
		},
		"match and time range with quote": {
			q: "message: test\\ node AND datetime: [\"2020-01-01T00:00:00\" TO \"2020-12-31T00:00:00\"]",

			es:  `{"bool":{"must":[{"match_phrase":{"message":{"query":"test\\ node"}}},{"range":{"datetime":{"from":"2020-01-01T00:00:00","include_lower":true,"include_upper":true,"to":"2020-12-31T00:00:00"}}}]}}`,
			sql: "`message` MATCH_PHRASE 'test\\ node' AND `datetime` >= '2020-01-01T00:00:00' AND `datetime` <= '2020-12-31T00:00:00'",
		},
		"match and time range": {
			q: "message: test\\ node AND datetime: [2020-01-01T00:00:00 TO 2020-12-31T00:00:00]",

			es:  `{"bool":{"must":[{"match_phrase":{"message":{"query":"test\\ node"}}},{"range":{"datetime":{"from":"2020-01-01T00:00:00","include_lower":true,"include_upper":true,"to":"2020-12-31T00:00:00"}}}]}}`,
			sql: "`message` MATCH_PHRASE 'test\\ node' AND `datetime` >= '2020-01-01T00:00:00' AND `datetime` <= '2020-12-31T00:00:00'",
		},
		"mixed or / and": {
			q: "a:1 OR (b:2 AND c:4)",

			es:  `{"bool":{"should":[{"term":{"a":"1"}},{"bool":{"must":[{"term":{"b":"2"}},{"term":{"c":"4"}}]}}]}}`,
			sql: "`a` = '1' OR (`b` = '2' AND `c` = '4')",
		},
		"start without tCOLON": {
			q: "a > 100",

			es:  `{"range":{"a":{"from":100,"include_lower":false,"include_upper":true,"to":null}}}`,
			sql: "`a` > '100'",
		},
		"end without tCOLON": {
			q: "a < 100",

			es:  `{"range":{"a":{"from":null,"include_lower":true,"include_upper":false,"to":100}}}`,
			sql: "`a` < '100'",
		},
		"start and eq without tCOLON": {
			q: "a >= 100",

			es:  `{"range":{"a":{"from":100,"include_lower":true,"include_upper":true,"to":null}}}`,
			sql: "`a` >= '100'",
		},
		"end and eq without tCOLON": {
			q: "a <= 100",

			es:  `{"range":{"a":{"from":null,"include_lower":true,"include_upper":true,"to":100}}}`,
			sql: "`a` <= '100'",
		},
		"start": {
			q: "a>100",

			es:  `{"range":{"a":{"from":100,"include_lower":false,"include_upper":true,"to":null}}}`,
			sql: "`a` > '100'",
		},
		"one word left star": {
			q: "*test",

			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"*test"}}`,
			sql: "`log` LIKE '%test'",
		},
		"one word right star": {
			q: "test*",

			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test*"}}`,
			sql: "`log` LIKE 'test%'",
		},
		"one word double star": {
			q: "*test*",

			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"*test*"}}`,
			sql: "`log` LIKE '%test%'",
		},
		"one int double star": {
			q:   "*123*",
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"*123*"}}`,
			sql: "`log` LIKE '%123%'",
		},
		"key node with star": {
			q: "events.attributes.message.detail: *66036*",

			es:  `{"wildcard":{"events.attributes.message.detail":{"value":"*66036*"}}}`,
			sql: "CAST(events['attributes']['message.detail'] AS STRING) LIKE '%66036%'",
		},
		"node like regex": {
			q:   `"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log" AND level: "error" AND "2_bklog.bkunify_query"`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log\""}},{"term":{"level":"error"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"2_bklog.bkunify_query\""}}]}}`,
			sql: "`log` MATCH_PHRASE '/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log' AND `level` = 'error' AND `log` MATCH_PHRASE '2_bklog.bkunify_query'",
		},
		"转义符号支持": {
			q:   `reading \"remove\"`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"reading"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\\\"remove\\\""}}]}}`,
			sql: "`log` MATCH_PHRASE 'reading' OR `log` MATCH_PHRASE '\\\"remove\\\"'",
		},
		"双引号转义符号支持": {
			q:   `"(reading \"remove\")"`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"(reading \\\"remove\\\")\""}}`,
			sql: "`log` MATCH_PHRASE '(reading \"remove\")'",
		},
		"test": {
			q: `path: "/proz/logds/ds-5910974792526317*"`,

			es:  `{"term":{"path":"/proz/logds/ds-5910974792526317*"}}`,
			sql: "`path` = '/proz/logds/ds-5910974792526317*'",
		},
		"test-1": {
			q: "\"32221112\" AND path: \"/data/home/user00/log/zonesvr*\"",

			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"32221112\""}},{"term":{"path":"/data/home/user00/log/zonesvr*"}}]}}`,
			sql: "`log` MATCH_PHRASE '32221112' AND `path` = '/data/home/user00/log/zonesvr*'",
		},
		"test - 2": {
			q:   `log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			es:  `{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"bool":{"should":[{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testOr"}}}]}},{"match_phrase":{"log":{"query":"testAnd"}}}],"should":{"match_phrase":{"log":{"query":"test111"}}}}}`,
			sql: "(`log` MATCH_PHRASE 'friendsvr' AND (`log` MATCH_PHRASE 'game_app' OR `log` MATCH_PHRASE 'testOr') AND `log` MATCH_PHRASE 'testAnd' OR `log` MATCH_PHRASE 'test111')",
		},
		"test - Many Brack ": {
			q:   `(loglevel: ("TRACE" OR "DEBUG" OR "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")) AND "test111"`,
			es:  `{"bool":{"must":[{"bool":{"must":[{"bool":{"should":[{"term":{"loglevel":"TRACE"}},{"term":{"loglevel":"DEBUG"}},{"term":{"loglevel":"INFO "}},{"term":{"loglevel":"WARN "}},{"term":{"loglevel":"ERROR"}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"bool":{"should":[{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testOr"}}}]}},{"match_phrase":{"log":{"query":"testAnd"}}}],"should":{"match_phrase":{"log":{"query":"test111"}}}}}]}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"test111\""}}]}}`,
			sql: "((`loglevel` = 'TRACE' OR `loglevel` = 'DEBUG' OR `loglevel` = 'INFO ' OR `loglevel` = 'WARN ' OR `loglevel` = 'ERROR') AND (`log` MATCH_PHRASE 'friendsvr' AND (`log` MATCH_PHRASE 'game_app' OR `log` MATCH_PHRASE 'testOr') AND `log` MATCH_PHRASE 'testAnd' OR `log` MATCH_PHRASE 'test111')) AND `log` MATCH_PHRASE 'test111'",
		},
		"test - many tPHRASE ": {
			q:   `loglevel: ("*TRACE*" OR "*DEBUG*" OR "*INFO*" OR "*WARN*" OR "*ERROR*") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			es:  `{"bool":{"must":[{"bool":{"should":[{"term":{"loglevel":"*TRACE*"}},{"term":{"loglevel":"*DEBUG*"}},{"term":{"loglevel":"*INFO*"}},{"term":{"loglevel":"*WARN*"}},{"term":{"loglevel":"*ERROR*"}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"bool":{"should":[{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testOr"}}}]}},{"match_phrase":{"log":{"query":"testAnd"}}}],"should":{"match_phrase":{"log":{"query":"test111"}}}}}]}}`,
			sql: "(`loglevel` = '*TRACE*' OR `loglevel` = '*DEBUG*' OR `loglevel` = '*INFO*' OR `loglevel` = '*WARN*' OR `loglevel` = '*ERROR*') AND (`log` MATCH_PHRASE 'friendsvr' AND (`log` MATCH_PHRASE 'game_app' OR `log` MATCH_PHRASE 'testOr') AND `log` MATCH_PHRASE 'testAnd' OR `log` MATCH_PHRASE 'test111')",
		},
		"test - Single Bracket And  ": {
			q:   `loglevel: ("TRACE" AND "111" AND "DEBUG" AND "INFO" OR "SIMON" OR "222" AND "333" )`,
			es:  `{"bool":{"must":[{"term":{"loglevel":"TRACE"}},{"term":{"loglevel":"111"}},{"term":{"loglevel":"DEBUG"}},{"term":{"loglevel":"INFO"}},{"term":{"loglevel":"333"}}],"should":[{"term":{"loglevel":"SIMON"}},{"term":{"loglevel":"222"}}]}}`,
			sql: "(`loglevel` = 'TRACE' AND `loglevel` = '111' AND `loglevel` = 'DEBUG' AND `loglevel` = 'INFO' OR `loglevel` = 'SIMON' OR `loglevel` = '222' AND `loglevel` = '333')",
		},
		"test - Self Bracket ": {
			q:   `loglevel: ("TRACE" OR ("DEBUG") OR ("INFO ") OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111") AND`,
			es:  `{"bool":{"must":[{"bool":{"should":[{"term":{"loglevel":"TRACE"}},{"term":{"loglevel":"DEBUG"}},{"term":{"loglevel":"INFO "}},{"term":{"loglevel":"WARN "}},{"term":{"loglevel":"ERROR"}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"bool":{"should":[{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testOr"}}}]}},{"match_phrase":{"log":{"query":"testAnd"}}}],"should":{"match_phrase":{"log":{"query":"test111"}}}}}]}}`,
			sql: "(`loglevel` = 'TRACE' OR (`loglevel` = 'DEBUG') OR (`loglevel` = 'INFO ') OR `loglevel` = 'WARN ' OR `loglevel` = 'ERROR') AND (`log` MATCH_PHRASE 'friendsvr' AND (`log` MATCH_PHRASE 'game_app' OR `log` MATCH_PHRASE 'testOr') AND `log` MATCH_PHRASE 'testAnd' OR `log` MATCH_PHRASE 'test111')",
		},
		// =================================================================
		// Test Suite: basic_syntax from antlr4_lucene_test_cases.json
		// =================================================================
		"simple_term": {
			q:   `term`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}}`,
			sql: "`log` MATCH_PHRASE 'term'",
		},
		"english_term": {
			q:   `hello`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello"}}`,
			sql: "`log` MATCH_PHRASE 'hello'",
		},
		"chinese_term": {
			q:   `中国`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"中国"}}`,
			sql: "`log` MATCH_PHRASE '中国'",
		},
		"accented_term": {
			q:   `café`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"café"}}`,
			sql: "`log` MATCH_PHRASE 'café'",
		},
		"basic_field_query": {
			q:   `status:Value`,
			es:  `{"term":{"status":"Value"}}`,
			sql: "`status` = 'Value'",
		},
		"- and +": {
			q:   `-sleep +46`,
			es:  `{"bool":{"must":[{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"sleep"}}}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"46"}}]}}`,
			sql: "`log` NOT MATCH_PHRASE 'sleep' AND `log` MATCH_PHRASE '46'",
		},
		"field_query_exists": {
			q:   `_exists_:author`,
			es:  `{"exists":{"field":"author"}}`,
			sql: "`author` IS NOT NULL",
		},
		"field_query_exists_not": {
			q:   `NOT _exists_:author`,
			es:  `{"bool":{"must_not":{"exists":{"field":"author"}}}}`,
			sql: "`author` IS NULL",
		},
		"field_query_exists_or": {
			q:   `_exists_: Dsa OR _exists_: Allocate`,
			es:  `{"bool":{"should":[{"exists":{"field":"Dsa"}},{"exists":{"field":"Allocate"}}]}}`,
			sql: "`Dsa` IS NOT NULL OR `Allocate` IS NOT NULL",
		},
		"field_query_exists_alias": {
			q:   `_exists_: container_name`,
			es:  `{"exists":{"field":"__ext.container_name"}}`,
			sql: "CAST(__ext['container_name'] AS STRING) IS NOT NULL",
		},
		"basic_phrase_query": {
			q:   `"hello world"`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"hello world\""}}`,
			sql: "`log` MATCH_PHRASE 'hello world'",
		},
		"field_phrase_query": {
			q:   `author:"phrase Value"`,
			es:  `{"match_phrase":{"author":{"query":"phrase Value"}}}`,
			sql: "`author` MATCH_PHRASE 'phrase Value'",
		},
		"proximity_query": {
			q:   `"hello world"~5`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"hello world\"~5"}}`,
			sql: "`log` MATCH_PHRASE 'hello world'",
		},
		"boolean_AND": {
			q:   `term1 AND term2`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "`log` MATCH_PHRASE 'term1' AND `log` MATCH_PHRASE 'term2'",
		},
		"boolean_OR": {
			q:   `term1 OR term2`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "`log` MATCH_PHRASE 'term1' OR `log` MATCH_PHRASE 'term2'",
		},
		"boolean_NOT": {
			q:   `term1 NOT term2`,
			es:  `{"bool":{"must":{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}}},"should":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}}}}`,
			sql: "`log` MATCH_PHRASE 'term1' AND `log` NOT MATCH_PHRASE 'term2' OR `log` NOT MATCH_PHRASE 'term2'",
		},
		"boolean_required_prohibited": {
			q:   `+required -prohibited`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"required"}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"prohibited"}}}}]}}`,
			sql: "`log` MATCH_PHRASE 'required' AND `log` NOT MATCH_PHRASE 'prohibited'",
		},
		"boolean_double_ampersand": {
			q:   `term1 && term2`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "`log` MATCH_PHRASE 'term1' AND `log` MATCH_PHRASE 'term2'",
		},
		"boolean_double_pipe": {
			q:   `term1 || term2`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "`log` MATCH_PHRASE 'term1' OR `log` MATCH_PHRASE 'term2'",
		},
		"wildcard_suffix": {
			q:   `test*`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test*"}}`,
			sql: "`log` LIKE 'test%'",
		},
		"wildcard_prefix": {
			q:   `*test`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"*test"}}`,
			sql: "`log` LIKE '%test'",
		},
		"wildcard_infix": {
			q:   `te*st`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"te*st"}}`,
			sql: "`log` LIKE 'te%st'",
		},
		"wildcard_single_char": {
			q:   `t?st`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"t?st"}}`,
			sql: "`log` LIKE 't_st'",
		},
		"wildcard_field": {
			q:   `path:test*`,
			es:  `{"wildcard":{"path":{"value":"test*"}}}`,
			sql: "`path` LIKE 'test%'",
		},
		"regex_basic": {
			q:   `/test.*/`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"/test.*/"}}`,
			sql: "`log` REGEXP 'test.*'",
		},
		"regex_field": {
			q:   `log:/patt.*n/`,
			es:  `{"regexp":{"log":{"value":"patt.*n"}}}`,
			sql: "`log` REGEXP 'patt.*n'",
		},
		"fuzzy_and_field": {
			q:   `log: test~`,
			es:  `{"fuzzy":{"log":{"fuzziness":"AUTO","value":"test"}}}`,
			sql: "`log` MATCH_PHRASE 'test'",
		},
		"fuzzy_default": {
			q:   `test~`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test~"}}`,
			sql: "`log` MATCH_PHRASE 'test'",
		},
		"fuzzy_with_distance_and_field": {
			q:   `log:test~1`,
			es:  `{"fuzzy":{"log":{"fuzziness":"1","value":"test"}}}`,
			sql: "`log` MATCH_PHRASE 'test'",
		},
		"fuzzy_with_distance": {
			q:   `test~1`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test~1"}}`,
			sql: "`log` MATCH_PHRASE 'test'",
		},
		"range_inclusive": {
			q:   `count:[1 TO 10]`,
			es:  `{"range":{"count":{"from":1,"include_lower":true,"include_upper":true,"to":10}}}`,
			sql: "`count` >= '1' AND `count` <= '10'",
		},
		"range_exclusive": {
			q:   `age:{18 TO 30}`,
			es:  `{"range":{"age":{"from":18,"include_lower":false,"include_upper":false,"to":30}}}`,
			sql: "`age` > '18' AND `age` < '30'",
		},
		"range_unbounded_lower": {
			q:   `count:[* TO 100]`,
			es:  `{"range":{"count":{"from":null,"include_lower":true,"include_upper":true,"to":100}}}`,
			sql: "`count` <= '100'",
		},
		"range_unbounded_upper": {
			q:   `count:[10 TO *]`,
			es:  `{"range":{"count":{"from":10,"include_lower":true,"include_upper":true,"to":null}}}`,
			sql: "`count` >= '10'",
		},
		"range_date": {
			q:   `datetime:[2021-01-01 TO 2021-12-31]`,
			es:  `{"range":{"datetime":{"from":"2021-01-01","include_lower":true,"include_upper":true,"to":"2021-12-31"}}}`,
			sql: "`datetime` >= '2021-01-01' AND `datetime` <= '2021-12-31'",
		},
		"boost_integer": {
			q:   `term^2`,
			es:  `{"query_string":{"analyze_wildcard":true,"boost":2,"fields":["*","__*"],"lenient":true,"query":"term"}}`,
			sql: "`log` MATCH_PHRASE 'term'",
		},
		"boost_float": {
			q:   `"phrase query"^3.5`,
			es:  `{"query_string":{"analyze_wildcard":true,"boost":3.5,"fields":["*","__*"],"lenient":true,"query":"\"phrase query\""}}`,
			sql: "`log` MATCH_PHRASE 'phrase query'",
		},
		"grouping_basic": {
			q:   `(term1 OR term2)`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'term1' OR `log` MATCH_PHRASE 'term2')",
		},
		"grouping_field": {
			q:   `author:(value1 OR value2)`,
			es:  `{"bool":{"should":[{"match_phrase":{"author":{"query":"value1"}}},{"match_phrase":{"author":{"query":"value2"}}}]}}`,
			sql: "(`author` MATCH_PHRASE 'value1' OR `author` MATCH_PHRASE 'value2')",
		},
		"grouping_with_boost": {
			q:   `(term1 AND term2)^2`,
			es:  `{"bool":{"boost":2,"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'term1' AND `log` MATCH_PHRASE 'term2')",
		},

		// =================================================================
		// Test Suite: edge_cases from antlr4_lucene_test_cases.json
		// =================================================================
		"escape_colon": {
			q:   `"hello:world"`, // 这里的':'不是一个用来分隔“字段名”和“值”的符号。表示“hello:world”是一个整体的搜索词
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"hello:world\""}}`,
			sql: "`log` MATCH_PHRASE 'hello:world'",
		},
		"escape_parentheses": {
			q:   `hello\(world\)`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello\\(world\\)"}}`,
			sql: "`log` MATCH_PHRASE 'hello\\(world\\)'",
		},
		"escape_star": {
			q:   `hello\*world`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello\\*world"}}`,
			sql: "`log` MATCH_PHRASE 'hello\\*world'",
		},
		"whitespace_multiple_spaces": {
			q:   `  hello  world  `,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"world"}}]}}`,
			sql: "`log` MATCH_PHRASE 'hello' OR `log` MATCH_PHRASE 'world'",
		},
		"numeric_integer": {
			q:   `123`, // 默认字段log是text类型,需要用match_phrase
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"123"}}`,
			sql: "`log` MATCH_PHRASE '123'",
		},
		"numeric_float": {
			q:   `12.34`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"12.34"}}`,
			sql: "`log` MATCH_PHRASE '12.34'",
		},
		"numeric_negative": {
			q:   `-123`,
			es:  `{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"123"}}}}`,
			sql: "`log` NOT MATCH_PHRASE '123'",
		},
		"unicode_russian": {
			q:   `Москва`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"Москва"}}`,
			sql: "`log` MATCH_PHRASE 'Москва'",
		},
		"unicode_japanese": {
			q:   `日本語`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"日本語"}}`,
			sql: "`log` MATCH_PHRASE '日本語'",
		},
		// TODO: special_match_all_docs test temporarily commented out
		// "special_match_all_docs": {
		// 	q:   `*:*`,
		// 	n:   &ConditionNode{field: &StringNode{Value: "*"}, op: OpMatch, value: &StringNode{Value: "*"}},
		// 	es:  `{"match_all":{}}`,
		// 	sql: "1 = 1",
		// },
		"special_empty_phrase": {
			q:   `""`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"\""}}`,
			sql: "`log` MATCH_PHRASE ''",
		},
		"text 字段空字符串转 exists": {
			q:   `log:""`,
			es:  `{"exists":{"field":"log"}}`,
			sql: "`log` IS NOT NULL",
		},
		"text 字段空字符串 AND 其他条件": {
			q:   `log:"" AND level:error`,
			es:  `{"bool":{"must":[{"exists":{"field":"log"}},{"term":{"level":"error"}}]}}`,
			sql: "`log` IS NOT NULL AND `level` = 'error'",
		},

		// =================================================================
		// Test Suite: complex_combinations from antlr4_lucene_test_cases.json
		// =================================================================
		"complex_nested_boolean": { // message是需要分词搜索的字段,用match_phrase; loglevel和status是精确值匹配的字段,用term
			q:  `(loglevel:java OR loglevel:python) AND (message:tutorial OR message:guide) AND NOT status:deprecated`,
			es: `{"bool":{"must":[{"bool":{"should":[{"term":{"loglevel":"java"}},{"term":{"loglevel":"python"}}]}},{"bool":{"should":[{"match_phrase":{"message":{"query":"tutorial"}}},{"match_phrase":{"message":{"query":"guide"}}}]}},{"bool":{"must_not":{"term":{"status":"deprecated"}}}}]}}`,
			// 在doris下如果是text类型
			sql: "(`loglevel` = 'java' OR `loglevel` = 'python') AND (`message` MATCH_PHRASE 'tutorial' OR `message` MATCH_PHRASE 'guide') AND `status` != 'deprecated'",
		},
		"complex_mixed_operators": {
			q:   `+required +(optional1 OR optional2) -excluded`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"required"}},{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"optional1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"optional2"}}]}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"excluded"}}}}]}}`,
			sql: "`log` MATCH_PHRASE 'required' AND (`log` MATCH_PHRASE 'optional1' OR `log` MATCH_PHRASE 'optional2') AND `log` NOT MATCH_PHRASE 'excluded'",
		},
		"complex_scoring_nested_boost": {
			// artificial intelligence需要被识别为phrase
			q: `(author:"machine learning"^3 OR message:"artificial intelligence"^2)^0.5`,
			// boost参数应该在ES查询结构中正确处理
			es:  `{"bool":{"boost":0.5,"should":[{"match_phrase":{"author":{"boost":3,"query":"machine learning"}}},{"match_phrase":{"message":{"boost":2,"query":"artificial intelligence"}}}]}}`,
			sql: "(`author` MATCH_PHRASE 'machine learning' OR `message` MATCH_PHRASE 'artificial intelligence')",
		},
		"complex_mixed_types": {
			q:   `author:john~ AND count:[* TO 100] AND (status:urgent OR loglevel:high^2)`,
			es:  `{"bool":{"must":[{"fuzzy":{"author":{"fuzziness":"AUTO","value":"john"}}},{"range":{"count":{"from":null,"include_lower":true,"include_upper":true,"to":100}}},{"bool":{"should":[{"term":{"status":"urgent"}},{"term":{"loglevel":{"boost":2,"value":"high"}}}]}}]}}`,
			sql: "`author` MATCH_PHRASE 'john' AND `count` <= '100' AND (`status` = 'urgent' OR `loglevel` = 'high')",
		},

		// =================================================================
		// Test Suite: lucene_extracted from antlr4_lucene_test_cases.json
		// =================================================================
		"lucene_extracted_simple_term_foo": {
			q:   `foo`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"foo"}}`,
			sql: "`log` MATCH_PHRASE 'foo'",
		},
		"lucene_extracted_boolean_plus": {
			q:   `+one +two`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"one"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"two"}}]}}`,
			sql: "`log` MATCH_PHRASE 'one' AND `log` MATCH_PHRASE 'two'",
		},
		"lucene_extracted_boost_fuzzy": {
			q:   `one~0.8 two^2`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"one~0.8"}},{"query_string":{"analyze_wildcard":true,"boost":2,"fields":["*","__*"],"lenient":true,"query":"two"}}]}}`,
			sql: "`log` MATCH_PHRASE 'one' OR `log` MATCH_PHRASE 'two'",
		},
		"lucene_extracted_wildcard_multi": {
			q:   `one* two*`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"one*"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"two*"}}]}}`,
			sql: "`log` LIKE 'one%' OR `log` LIKE 'two%'",
		},
		"lucene_extracted_boolean_precedence": {
			q:   `c OR (a AND b)`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"c"}},{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}]}}]}}`,
			sql: "`log` MATCH_PHRASE 'c' OR (`log` MATCH_PHRASE 'a' AND `log` MATCH_PHRASE 'b')",
		},
		"lucene_extracted_field_numeric": {
			q:   `log:1`,
			es:  `{"match_phrase":{"log":{"query":"1"}}}`,
			sql: "`log` MATCH_PHRASE '1'",
		},
		"lucene_extracted_range_int": {
			q:   `age:[1 TO 3]`,
			es:  `{"range":{"age":{"from":1,"include_lower":true,"include_upper":true,"to":3}}}`,
			sql: "`age` >= '1' AND `age` <= '3'",
		},
		"lucene_extracted_range_float": {
			q:   `price:[1.5 TO 3.6]`,
			es:  `{"range":{"price":{"from":1.5,"include_lower":true,"include_upper":true,"to":3.6}}}`,
			sql: "`price` >= '1.5' AND `price` <= '3.6'",
		},

		// =================================================================
		// Test Suite: eof_operator_support - 测试末尾操作符支持
		// =================================================================
		"eof_operator_and_basic": {
			q: `log:error AND`,
			// 预期：应该将末尾的AND忽略，只保留log:error部分
			es:  `{"match_phrase":{"log":{"query":"error"}}}`,
			sql: "`log` MATCH_PHRASE 'error'",
		},
		"eof_operator_or_basic": {
			q: `status:active OR`,
			// 预期：应该将末尾的OR忽略，只保留status:active部分
			es:  `{"term":{"status":"active"}}`,
			sql: "`status` = 'active'",
		},
		"eof_operator_and_complex": {
			q: `log:error and status:active AND`,
			// 预期：应该将末尾的AND忽略，保留前面的正常表达式
			es:  `{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` = 'active'",
		},

		// =================================================================
		// Test Suite: case_insensitive_operators - 测试大小写不敏感操作符
		// =================================================================
		"case_insensitive_and_lowercase": {
			q:   `log:error and status:active`,
			es:  `{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` = 'active'",
		},
		"case_insensitive_and_mixed": {
			q:   `log:error And status:active`,
			es:  `{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` = 'active'",
		},
		"case_insensitive_and_variations": {
			q:   `log:error aNd status:active anD level:info`,
			es:  `{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}},{"term":{"level":"info"}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` = 'active' AND `level` = 'info'",
		},
		"case_insensitive_or_lowercase": {
			q:   `log:error or status:active`,
			es:  `{"bool":{"should":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' OR `status` = 'active'",
		},
		"case_insensitive_or_mixed": {
			q:   `log:error Or status:active oR level:info`,
			es:  `{"bool":{"should":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}},{"term":{"level":"info"}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' OR `status` = 'active' OR `level` = 'info'",
		},
		"case_insensitive_not_lowercase": {
			q:   `log:error not status:active`,
			es:  `{"bool":{"must":{"bool":{"must_not":{"term":{"status":"active"}}}},"should":{"match_phrase":{"log":{"query":"error"}}}}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` != 'active' OR `status` != 'active'",
		},
		"case_insensitive_not_mixed": {
			q:   `log:error Not status:active`,
			es:  `{"bool":{"must":{"bool":{"must_not":{"term":{"status":"active"}}}},"should":{"match_phrase":{"log":{"query":"error"}}}}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` != 'active' OR `status` != 'active'",
		},
		"case_insensitive_mixed_complex": {
			q:   `(log:error AND status:active) or (level:warn Not type:system)`,
			es:  `{"bool":{"should":[{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}},{"bool":{"must":{"bool":{"must_not":{"term":{"type":"system"}}}},"should":{"term":{"level":"warn"}}}}]}}`,
			sql: "(`log` MATCH_PHRASE 'error' AND `status` = 'active') OR (`level` = 'warn' AND `type` != 'system' OR `type` != 'system')",
		},

		// =================================================================
		// Test Suite: escape_sequences - 转义字符补充测试
		// =================================================================
		"escape_question_mark": {
			q:   `hello\?world`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello\\?world"}}`,
			sql: "`log` MATCH_PHRASE 'hello\\?world'",
		},
		"escape_plus_sign": {
			q:   `hello\+world`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello\\+world"}}`,
			sql: "`log` MATCH_PHRASE 'hello\\+world'",
		},
		"escape_minus_sign": {
			q:   `hello\-world`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello\\-world"}}`,
			sql: "`log` MATCH_PHRASE 'hello\\-world'",
		},
		"escape_double_quote": {
			q:   `hello\"world`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello\\\"world"}}`,
			sql: "`log` MATCH_PHRASE 'hello\\\"world'",
		},
		"escape_double_backslash": {
			q:   `hello\\world`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello\\\\world"}}`,
			sql: "`log` MATCH_PHRASE 'hello\\\\world'",
		},

		// =================================================================
		// Test Suite: special_wildcard - 特殊通配符测试
		// =================================================================
		// 注意：单独的 * 和 ? 无法被解析器正确处理，会返回空结果
		// "special_single_wildcard_star": {
		// 	q:   `*`,
		// 	es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"*"}}`,
		// 	sql: "`log` LIKE '%'",
		// },
		// "special_single_wildcard_question": {
		// 	q:   `?`,
		// 	es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"?"}}`,
		// 	sql: "`log` LIKE '_'",
		// },

		// =================================================================
		// Test Suite: whitespace_handling - 空白符处理补充测试
		// =================================================================
		"whitespace_tab_separator": {
			q:   "hello\tworld",
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"world"}}]}}`,
			sql: "`log` MATCH_PHRASE 'hello' OR `log` MATCH_PHRASE 'world'",
		},
		"whitespace_newline_separator": {
			q:   "hello\nworld",
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"world"}}]}}`,
			sql: "`log` MATCH_PHRASE 'hello' OR `log` MATCH_PHRASE 'world'",
		},
		"whitespace_normal_spaces": {
			q:   `hello world`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"world"}}]}}`,
			sql: "`log` MATCH_PHRASE 'hello' OR `log` MATCH_PHRASE 'world'",
		},

		// =================================================================
		// Test Suite: lucene_extracted_priority1 - Lucene官方高优先级测试
		// =================================================================
		"lucene_multi_term_foo_foobar": {
			q:   `foo foobar`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"foo"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"foobar"}}]}}`,
			sql: "`log` MATCH_PHRASE 'foo' OR `log` MATCH_PHRASE 'foobar'",
		},
		"lucene_multi_foo": {
			q:   `multi foo`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"multi"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"foo"}}]}}`,
			sql: "`log` MATCH_PHRASE 'multi' OR `log` MATCH_PHRASE 'foo'",
		},
		"lucene_foo_multi": {
			q:   `foo multi`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"foo"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"multi"}}]}}`,
			sql: "`log` MATCH_PHRASE 'foo' OR `log` MATCH_PHRASE 'multi'",
		},
		"lucene_multi_multi": {
			q:   `multi multi`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"multi"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"multi"}}]}}`,
			sql: "`log` MATCH_PHRASE 'multi' OR `log` MATCH_PHRASE 'multi'",
		},
		"lucene_operator_minus_with_spaces": {
			q:   `a - b`,
			es:  `{"bool":{"must":{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}}},"should":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}}}}`,
			sql: "`log` MATCH_PHRASE 'a' AND `log` NOT MATCH_PHRASE 'b' OR `log` NOT MATCH_PHRASE 'b'",
		},
		"lucene_operator_plus_with_spaces": {
			q:   `a + b`,
			es:  `{"bool":{"must":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}},"should":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}}}}`,
			sql: "`log` MATCH_PHRASE 'a' AND `log` MATCH_PHRASE 'b' OR `log` MATCH_PHRASE 'b'",
		},
		// 注意: a ! b 中的 ! 会被当作普通文本处理，解析为 a OR b
		"lucene_operator_exclamation_with_spaces": {
			q:   `a ! b`,
			es:  `{"bool":{"must":{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}}},"should":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}}}}`,
			sql: "`log` MATCH_PHRASE 'a' AND `log` NOT MATCH_PHRASE 'b' OR `log` NOT MATCH_PHRASE 'b'",
		},
		// 注意: +guinea pig 会被解析为 pig AND guinea OR guinea
		"lucene_guinea_pig_plus": {
			q:   `+guinea pig`,
			es:  `{"bool":{"must":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"guinea"}},"should":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"pig"}}}}`,
			sql: "`log` MATCH_PHRASE 'pig' AND `log` MATCH_PHRASE 'guinea' OR `log` MATCH_PHRASE 'guinea'",
		},
		"lucene_guinea_pig_minus": {
			q:   `-guinea pig`,
			es:  `{"bool":{"must":{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"guinea"}}}},"should":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"pig"}}}}`,
			sql: "`log` MATCH_PHRASE 'pig' AND `log` NOT MATCH_PHRASE 'guinea' OR `log` NOT MATCH_PHRASE 'guinea'",
		},
		// 注意: !guinea 会被解析器处理为 guinea (感叹号被忽略)
		"lucene_guinea_pig_exclamation": {
			q:   `!guinea pig`,
			es:  `{"bool":{"must":{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"guinea"}}}},"should":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"pig"}}}}`,
			sql: "`log` MATCH_PHRASE 'pig' AND `log` NOT MATCH_PHRASE 'guinea' OR `log` NOT MATCH_PHRASE 'guinea'",
		},
		"lucene_guinea_wildcard_star": {
			q:   `guinea* pig`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"guinea*"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"pig"}}]}}`,
			sql: "`log` LIKE 'guinea%' OR `log` MATCH_PHRASE 'pig'",
		},
		"lucene_guinea_wildcard_question": {
			q:   `guinea? pig`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"guinea?"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"pig"}}]}}`,
			sql: "`log` LIKE 'guinea_' OR `log` MATCH_PHRASE 'pig'",
		},
		"lucene_guinea_fuzzy": {
			q:   `guinea~2 pig`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"guinea~2"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"pig"}}]}}`,
			sql: "`log` MATCH_PHRASE 'guinea' OR `log` MATCH_PHRASE 'pig'",
		},
		"lucene_guinea_boost": {
			q:   `guinea^2 pig`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"boost":2,"fields":["*","__*"],"lenient":true,"query":"guinea"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"pig"}}]}}`,
			sql: "`log` MATCH_PHRASE 'guinea' OR `log` MATCH_PHRASE 'pig'",
		},
		"lucene_term_phrase_term": {
			q:   `term phrase term`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"phrase"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}}]}}`,
			sql: "`log` MATCH_PHRASE 'term' OR `log` MATCH_PHRASE 'phrase' OR `log` MATCH_PHRASE 'term'",
		},
		"lucene_unicode_umlaut": {
			q:   `ümlaut`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"ümlaut"}}`,
			sql: "`log` MATCH_PHRASE 'ümlaut'",
		},
		"lucene_unicode_turm": {
			q:   `türm term term`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"türm"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}}]}}`,
			sql: "`log` MATCH_PHRASE 'türm' OR `log` MATCH_PHRASE 'term' OR `log` MATCH_PHRASE 'term'",
		},

		// =================================================================
		// Test Suite: numeric_edge_cases - 数值边界测试
		// =================================================================
		"numeric_scientific_notation": {
			q:   `1e10`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"1e10"}}`,
			sql: "`log` MATCH_PHRASE '1e10'",
		},

		// =================================================================
		// Test Suite: unicode_extended - Unicode扩展测试
		// =================================================================
		"unicode_arabic": {
			q:   `العربية`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"العربية"}}`,
			sql: "`log` MATCH_PHRASE 'العربية'",
		},
		"unicode_chinese_query": {
			q:   `中文查询`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"中文查询"}}`,
			sql: "`log` MATCH_PHRASE '中文查询'",
		},
		"unicode_french_accents": {
			q:   `café résumé`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"café"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"résumé"}}]}}`,
			sql: "`log` MATCH_PHRASE 'café' OR `log` MATCH_PHRASE 'résumé'",
		},
		"unicode_cjk_ideographic_space": {
			q:   "term\u3000term\u3000term",
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}}]}}`,
			sql: "`log` MATCH_PHRASE 'term' OR `log` MATCH_PHRASE 'term' OR `log` MATCH_PHRASE 'term'",
		},

		// =================================================================
		// Test Suite: field_special_cases - 字段特殊情况测试
		// =================================================================
		"field_equals_operator": {
			q:   `field=a`,
			es:  `{"term":{"field":"a"}}`,
			sql: "`field` = 'a'",
		},
		// 注意: foo:\ 是无效的语法，会导致解析错误
		// "field_backslash_boundary": {
		// 	q:   `foo:\`,
		// 	es:  `{"match_phrase":{"foo":{"query":"\\"}}}`,
		// 	sql: "`foo` MATCH_PHRASE '\\'",
		// },

		// =================================================================
		// Test Suite: range_numeric_types - 范围查询数值类型测试
		// =================================================================
		"range_integer_field": {
			q:   `intField:[1 TO 3]`,
			es:  `{"range":{"intField":{"from":1,"include_lower":true,"include_upper":true,"to":3}}}`,
			sql: "`intField` >= '1' AND `intField` <= '3'",
		},
		"range_integer_field_single": {
			q:   `intField:1`,
			es:  `{"term":{"intField":"1"}}`,
			sql: "`intField` = '1'",
		},
		"range_long_field": {
			q:   `longField:[1 TO 3]`,
			es:  `{"range":{"longField":{"from":1,"include_lower":true,"include_upper":true,"to":3}}}`,
			sql: "`longField` >= '1' AND `longField` <= '3'",
		},
		"range_float_field": {
			q:   `floatField:[1.5 TO 3.6]`,
			es:  `{"range":{"floatField":{"from":1.5,"include_lower":true,"include_upper":true,"to":3.6}}}`,
			sql: "`floatField` >= '1.5' AND `floatField` <= '3.6'",
		},
		"range_double_field": {
			q:   `doubleField:[1.5 TO 3.6]`,
			es:  `{"range":{"doubleField":{"from":1.5,"include_lower":true,"include_upper":true,"to":3.6}}}`,
			sql: "`doubleField` >= '1.5' AND `doubleField` <= '3.6'",
		},

		// =================================================================
		// Test Suite: complex_field_combinations - 复杂字段组合测试
		// =================================================================
		"complex_multi_field_combination": {
			q: `title:"hello world" AND content:programming AND author:john`,
			// 注意: title 字段使用 term (因为是短语查询), author 使用 match_phrase (因为在 fieldsMap 中标记为 IsAnalyzed)
			es:  `{"bool":{"must":[{"term":{"title":"hello world"}},{"term":{"content":"programming"}},{"match_phrase":{"author":{"query":"john"}}}]}}`,
			sql: "`title` = 'hello world' AND `content` = 'programming' AND `author` MATCH_PHRASE 'john'",
		},
		"complex_multi_field_grouping": {
			q:   `title:(java OR python) AND tags:(tutorial AND beginner)`,
			es:  `{"bool":{"must":[{"bool":{"should":[{"term":{"title":"java"}},{"term":{"title":"python"}}]}},{"bool":{"must":[{"term":{"tags":"tutorial"}},{"term":{"tags":"beginner"}}]}}]}}`,
			sql: "(`title` = 'java' OR `title` = 'python') AND (`tags` = 'tutorial' AND `tags` = 'beginner')",
		},
		"complex_multi_field_boost": {
			q:   `title:java^10 OR content:java^1 OR tags:java^5`,
			es:  `{"bool":{"should":[{"term":{"title":{"boost":10,"value":"java"}}},{"term":{"content":{"boost":1,"value":"java"}}},{"term":{"tags":{"boost":5,"value":"java"}}}]}}`,
			sql: "`title` = 'java' OR `content` = 'java' OR `tags` = 'java'",
		},

		// =================================================================
		// Test Suite: lucene_extracted_priority2 - Lucene官方中优先级测试
		// =================================================================
		"lucene_boolean_and_parentheses": {
			q:   `(a AND b)`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'a' AND `log` MATCH_PHRASE 'b')",
		},
		"lucene_boolean_and_not": {
			q:   `a AND NOT b`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}}}]}}`,
			sql: "`log` MATCH_PHRASE 'a' AND `log` NOT MATCH_PHRASE 'b'",
		},
		"lucene_boolean_and_minus": {
			q:   `a AND -b`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}}}]}}`,
			sql: "`log` MATCH_PHRASE 'a' AND `log` NOT MATCH_PHRASE 'b'",
		},
		"lucene_field_body_text": {
			q:   `body:text`,
			es:  `{"term":{"body":"text"}}`,
			sql: "`body` = 'text'",
		},
		"lucene_fuzzy_similarity": {
			q:   `test~0.8`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test~0.8"}}`,
			sql: "`log` MATCH_PHRASE 'test'",
		},
		"lucene_range_field_alpha": {
			q:   `field:[a TO z]`,
			es:  `{"range":{"field":{"from":"a","include_lower":true,"include_upper":true,"to":"z"}}}`,
			sql: "`field` >= 'a' AND `field` <= 'z'",
		},
		"lucene_boost_field_integer": {
			q:   `field:value^10`,
			es:  `{"term":{"field":{"boost":10,"value":"value"}}}`,
			sql: "`field` = 'value'",
		},
		"lucene_unicode_japanese": {
			q:   "用語\u3000用語\u3000用語",
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"用語"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"用語"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"用語"}}]}}`,
			sql: "`log` MATCH_PHRASE '用語' OR `log` MATCH_PHRASE '用語' OR `log` MATCH_PHRASE '用語'",
		},
		"lucene_regex_character_class": {
			q:   `/[a-z]+/`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"/[a-z]+/"}}`,
			sql: "`log` REGEXP '[a-z]+'",
		},
		"lucene_field_regex": {
			q:   `field:/pattern/`,
			es:  `{"regexp":{"field":{"value":"pattern"}}}`,
			sql: "`field` REGEXP 'pattern'",
		},

		// =================================================================
		// Test Suite: additional_edge_cases - 额外边界情况测试
		// =================================================================
		"edge_empty_field_value": {
			q:   `field:""`,
			es:  `{"term":{"field":""}}`,
			sql: "`field` = ''",
		},
		"edge_field_with_underscore": {
			q:   `_field:value`,
			es:  `{"term":{"_field":"value"}}`,
			sql: "`_field` = 'value'",
		},
		"edge_field_with_numbers": {
			q:   `field123:value`,
			es:  `{"term":{"field123":"value"}}`,
			sql: "`field123` = 'value'",
		},
		"edge_field_with_dots": {
			q:   `field.subfield:value`,
			es:  `{"term":{"field.subfield":"value"}}`,
			sql: "CAST(field['subfield'] AS STRING) = 'value'",
		},
		"edge_multiple_wildcards": {
			q:   `te*st*ing`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"te*st*ing"}}`,
			sql: "`log` LIKE 'te%st%ing'",
		},
		"edge_wildcard_with_question": {
			q:   `te?t*`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"te?t*"}}`,
			sql: "`log` LIKE 'te_t%'",
		},
		"edge_phrase_with_wildcard": {
			q:   `"hello wor*"`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"hello wor*\""}}`,
			sql: "`log` MATCH_PHRASE 'hello wor*'",
		},
		"edge_nested_parentheses": {
			q:   `((a OR b) AND (c OR d))`,
			es:  `{"bool":{"must":[{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}]}},{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"c"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"d"}}]}}]}}`,
			sql: "((`log` MATCH_PHRASE 'a' OR `log` MATCH_PHRASE 'b') AND (`log` MATCH_PHRASE 'c' OR `log` MATCH_PHRASE 'd'))",
		},
		"edge_deep_nested_parentheses": {
			q:   `(((a AND b) OR c) AND d)`,
			es:  `{"bool":{"must":[{"bool":{"should":[{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}]}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"c"}}]}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"d"}}]}}`,
			sql: "(((`log` MATCH_PHRASE 'a' AND `log` MATCH_PHRASE 'b') OR `log` MATCH_PHRASE 'c') AND `log` MATCH_PHRASE 'd')",
		},

		// =================================================================
		// Test Suite: range_query_variations - 范围查询变体测试
		// =================================================================
		"range_exclusive_both": {
			q:   `count:{10 TO 20}`,
			es:  `{"range":{"count":{"from":10,"include_lower":false,"include_upper":false,"to":20}}}`,
			sql: "`count` > '10' AND `count` < '20'",
		},
		"range_mixed_inclusive_exclusive_left": {
			q:   `count:[10 TO 20}`,
			es:  `{"range":{"count":{"from":10,"include_lower":true,"include_upper":false,"to":20}}}`,
			sql: "`count` >= '10' AND `count` < '20'",
		},
		"range_mixed_inclusive_exclusive_right": {
			q:   `count:{10 TO 20]`,
			es:  `{"range":{"count":{"from":10,"include_lower":false,"include_upper":true,"to":20}}}`,
			sql: "`count` > '10' AND `count` <= '20'",
		},
		"range_with_negative_numbers": {
			q:   `temperature:[-10 TO 30]`,
			es:  `{"range":{"temperature":{"from":-10,"include_lower":true,"include_upper":true,"to":30}}}`,
			sql: "`temperature` >= '-10' AND `temperature` <= '30'",
		},
		"range_with_decimals": {
			q:   `price:[9.99 TO 99.99]`,
			es:  `{"range":{"price":{"from":9.99,"include_lower":true,"include_upper":true,"to":99.99}}}`,
			sql: "`price` >= '9.99' AND `price` <= '99.99'",
		},
		"range_timestamp": {
			q:   `timestamp:[2024-01-01T00:00:00 TO 2024-12-31T23:59:59]`,
			es:  `{"range":{"timestamp":{"from":"2024-01-01T00:00:00","include_lower":true,"include_upper":true,"to":"2024-12-31T23:59:59"}}}`,
			sql: "`timestamp` >= '2024-01-01T00:00:00' AND `timestamp` <= '2024-12-31T23:59:59'",
		},

		// =================================================================
		// Test Suite: boolean_operator_combinations - 布尔操作符组合测试
		// =================================================================
		"boolean_triple_and": {
			q:   `a AND b AND c`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"c"}}]}}`,
			sql: "`log` MATCH_PHRASE 'a' AND `log` MATCH_PHRASE 'b' AND `log` MATCH_PHRASE 'c'",
		},
		"boolean_triple_or": {
			q:   `a OR b OR c`,
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"c"}}]}}`,
			sql: "`log` MATCH_PHRASE 'a' OR `log` MATCH_PHRASE 'b' OR `log` MATCH_PHRASE 'c'",
		},
		"boolean_mixed_and_or": {
			q:   `a OR b AND c`,
			es:  `{"bool":{"must":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"c"}},"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}]}}`,
			sql: "`log` MATCH_PHRASE 'a' OR `log` MATCH_PHRASE 'b' AND `log` MATCH_PHRASE 'c'",
		},
		"boolean_all_required": {
			q:   `+a +b +c`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"c"}}]}}`,
			sql: "`log` MATCH_PHRASE 'a' AND `log` MATCH_PHRASE 'b' AND `log` MATCH_PHRASE 'c'",
		},
		"boolean_all_prohibited": {
			q:   `-a -b -c`,
			es:  `{"bool":{"must":[{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}}}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"c"}}}}]}}`,
			sql: "`log` NOT MATCH_PHRASE 'a' AND `log` NOT MATCH_PHRASE 'b' AND `log` NOT MATCH_PHRASE 'c'",
		},
		"boolean_mixed_required_prohibited": {
			q:   `+required1 +required2 -prohibited1 -prohibited2`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"required1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"required2"}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"prohibited1"}}}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"prohibited2"}}}}]}}`,
			sql: "`log` MATCH_PHRASE 'required1' AND `log` MATCH_PHRASE 'required2' AND `log` NOT MATCH_PHRASE 'prohibited1' AND `log` NOT MATCH_PHRASE 'prohibited2'",
		},

		// =================================================================
		// Test Suite: field_query_variations - 字段查询变体测试
		// =================================================================
		"field_query_with_hyphen": {
			q:   `field-name:value`,
			es:  `{"term":{"field-name":"value"}}`,
			sql: "`field-name` = 'value'",
		},
		"field_query_with_colon_in_value": {
			q:   `url:"http://example.com"`,
			es:  `{"term":{"url":"http://example.com"}}`,
			sql: "`url` = 'http://example.com'",
		},
		"field_query_with_slash_in_value": {
			q:   `path:"/var/log/app.log"`,
			es:  `{"term":{"path":"/var/log/app.log"}}`,
			sql: "`path` = '/var/log/app.log'",
		},
		"field_multiple_values_or": {
			q:   `status:200 OR status:201 OR status:204`,
			es:  `{"bool":{"should":[{"term":{"status":"200"}},{"term":{"status":"201"}},{"term":{"status":"204"}}]}}`,
			sql: "`status` = '200' OR `status` = '201' OR `status` = '204'",
		},
		"field_range_and_term": {
			q:   `age:[18 TO 65] AND status:active`,
			es:  `{"bool":{"must":[{"range":{"age":{"from":18,"include_lower":true,"include_upper":true,"to":65}}},{"term":{"status":"active"}}]}}`,
			sql: "`age` >= '18' AND `age` <= '65' AND `status` = 'active'",
		},
		"range_with_multiple_not": {
			q:   `status:[500 TO 600] NOT status:501 AND NOT sIdeToken AND NOT "dify-api"`,
			es:  `{"bool":{"must":[{"bool":{"must_not":{"term":{"status":"501"}}}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"sIdeToken"}}}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"dify-api\""}}}},{"range":{"status":{"from":500,"include_lower":true,"include_upper":true,"to":600}}}]}}`,
			sql: "`status` >= '500' AND `status` <= '600' AND `log` NOT MATCH_PHRASE 'sIdeToken' AND `log` NOT MATCH_PHRASE 'dify-api' AND `status` != '501' OR `log` NOT MATCH_PHRASE 'sIdeToken' AND `log` NOT MATCH_PHRASE 'dify-api' AND `status` != '501'",
		},
		"field_value_with_multiple_not": {
			q:   `log:error NOT status:active NOT "ECONNRESET" NOT "endsWith"`,
			es:  `{"bool":{"must":[{"bool":{"must_not":{"term":{"status":"active"}}}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"ECONNRESET\""}}}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"endsWith\""}}}},{"match_phrase":{"log":{"query":"error"}}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` != 'active' AND `log` NOT MATCH_PHRASE 'ECONNRESET' AND `log` NOT MATCH_PHRASE 'endsWith' OR `status` != 'active' AND `log` NOT MATCH_PHRASE 'ECONNRESET' AND `log` NOT MATCH_PHRASE 'endsWith'",
		},

		// =================================================================
		// Test Suite: boost_query_variations - 权重查询变体测试
		// =================================================================
		"boost_phrase_integer": {
			q:   `"hello world"^5`,
			es:  `{"query_string":{"analyze_wildcard":true,"boost":5,"fields":["*","__*"],"lenient":true,"query":"\"hello world\""}}`,
			sql: "`log` MATCH_PHRASE 'hello world'",
		},
		"boost_wildcard_query": {
			q:   `test*^2`,
			es:  `{"query_string":{"analyze_wildcard":true,"boost":2,"fields":["*","__*"],"lenient":true,"query":"test*"}}`,
			sql: "`log` LIKE 'test%'",
		},
		"boost_range_query": {
			q:   `count:[1 TO 10]^3`,
			es:  `{"range":{"count":{"boost":3,"from":1,"include_lower":true,"include_upper":true,"to":10}}}`,
			sql: "`count` >= '1' AND `count` <= '10'",
		},
		"boost_nested_groups": {
			q:   `(a OR b)^2 AND (c OR d)^3`,
			es:  `{"bool":{"must":[{"bool":{"boost":2,"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}]}},{"bool":{"boost":3,"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"c"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"d"}}]}}]}}`,
			sql: "(`log` MATCH_PHRASE 'a' OR `log` MATCH_PHRASE 'b') AND (`log` MATCH_PHRASE 'c' OR `log` MATCH_PHRASE 'd')",
		},

		// =================================================================
		// Test Suite: wildcard_advanced - 高级通配符测试
		// =================================================================
		"wildcard_multiple_stars": {
			q:   `*test*data*`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"*test*data*"}}`,
			sql: "`log` LIKE '%test%data%'",
		},
		"wildcard_multiple_questions": {
			q:   `t??t`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"t??t"}}`,
			sql: "`log` LIKE 't__t'",
		},
		"wildcard_mixed_star_question": {
			q:   `te?t*ing`,
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"te?t*ing"}}`,
			sql: "`log` LIKE 'te_t%ing'",
		},
		"wildcard_field_with_star": {
			q:   `name:john*`,
			es:  `{"wildcard":{"name":{"value":"john*"}}}`,
			sql: "`name` LIKE 'john%'",
		},
		"wildcard_field_with_question": {
			q:   `name:j?hn`,
			es:  `{"wildcard":{"name":{"value":"j?hn"}}}`,
			sql: "`name` LIKE 'j_hn'",
		},
		"force_comble": {
			q:   `+(foo bar) +(baz boo)`,
			es:  `{"bool":{"must":[{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"foo"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"bar"}}]}},{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"baz"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"boo"}}]}}]}}`,
			sql: "(`log` MATCH_PHRASE 'foo' OR `log` MATCH_PHRASE 'bar') AND (`log` MATCH_PHRASE 'baz' OR `log` MATCH_PHRASE 'boo')",
		},
		"phrase_boost_v2": {
			q:   `(term)^2.0`,
			es:  `{"bool":{"boost":2,"must":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}}}}`,
			sql: "(`log` MATCH_PHRASE 'term')", // 已知doris不处理boost
		},
		"phrase_boost_v3": {
			q:   `(germ term)^2.0`,
			es:  `{"bool":{"boost":2,"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"germ"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'germ' OR `log` MATCH_PHRASE 'term')",
		},
		"phrase_boost_v4": {
			q:   `term^2.0`,
			es:  `{"query_string":{"analyze_wildcard":true,"boost":2,"fields":["*","__*"],"lenient":true,"query":"term"}}`,
			sql: "`log` MATCH_PHRASE 'term'",
		},
		"phrase_boost_v5": {
			q:   `term AND "\"phrase phrase\""`,
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"\\\"phrase phrase\\\"\""}}]}}`,
			sql: "`log` MATCH_PHRASE 'term' AND `log` MATCH_PHRASE 'phrase phrase'",
		},
		"like_boost": {
			q:   `term*^2`,
			es:  `{"query_string":{"analyze_wildcard":true,"boost":2,"fields":["*","__*"],"lenient":true,"query":"term*"}}`,
			sql: "`log` LIKE 'term%'",
		},
		"force_or": {
			q:   `term +(stop) term`,
			es:  `{"bool":{"must":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"stop"}},"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}}]}}`,
			sql: "`log` MATCH_PHRASE 'term' AND (`log` MATCH_PHRASE 'stop') OR `log` MATCH_PHRASE 'term' AND (`log` MATCH_PHRASE 'stop') OR (`log` MATCH_PHRASE 'stop')",
		},
		"a\\-b:c": {
			q:   `a\-b:c`,
			es:  `{"term":{"a\\-b":"c"}}`,
			sql: "`a\\-b` = 'c'",
		},
		"a:b+?c": {
			q:   `a:b+?c`,
			es:  `{"wildcard":{"a":{"value":"b+?c"}}}`,
			sql: "`a` LIKE 'b+_c'",
		},

		// =================================================================
		// Test Suite: quoted_special_chars - 引号内特殊字符不应被识别为通配符
		// =================================================================
		"quoted_question_mark_not_wildcard": {
			q:   `request_uri:"/scm/api/proxy?serviceName=test"`,
			es:  `{"term":{"request_uri":"/scm/api/proxy?serviceName=test"}}`,
			sql: "`request_uri` = '/scm/api/proxy?serviceName=test'",
		},
		"negation_quoted_question_mark": {
			q:   `!request_uri:"/scm/api/proxy?serviceName=test"`,
			es:  `{"bool":{"must_not":{"term":{"request_uri":"/scm/api/proxy?serviceName=test"}}}}`,
			sql: "`request_uri` != '/scm/api/proxy?serviceName=test'",
		},
		"quoted_star_as_literal": {
			q:   `field:"value*with*stars"`,
			es:  `{"term":{"field":"value*with*stars"}}`,
			sql: "`field` = 'value*with*stars'",
		},
		"quoted_question_mark_analyzed_field": {
			q:   `log:"/path?query=1"`,
			es:  `{"match_phrase":{"log":{"query":"/path?query=1"}}}`,
			sql: "`log` MATCH_PHRASE '/path?query=1'",
		},
	}

	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	fieldsMap := metadata.FieldsMap{
		"log": {
			IsAnalyzed: true,
			FieldType:  "text",
		},
		"author": {
			IsAnalyzed: true,
		},
		"message": {
			IsAnalyzed: true,
		},
		"__ext.container_name": {
			AliasName: "container_name",
			FieldType: "text",
		},
	}
	aliasMap := make(map[string]string)
	for k, o := range fieldsMap {
		if o.AliasName == "" {
			continue
		}
		aliasMap[o.AliasName] = k
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			node := ParseLuceneWithVisitor(ctx, c.q, Option{
				FieldsMap:       fieldsMap,
				FieldEncodeFunc: fieldEncodeFunc,
			})
			assert.Nil(t, node.Error())

			sql := node.String()
			assert.Equal(t, c.sql, sql)

			node = ParseLuceneWithVisitor(ctx, c.q, Option{
				FieldsMap: fieldsMap,
			})
			assert.Nil(t, node.Error())
			dsl := MergeQuery(node.DSL())
			dslActual, _ := queryToJSON(dsl)
			assert.Equal(t, c.es, dslActual)
		})
	}
}

func queryToJSON(query elastic.Query) (string, error) {
	if query == nil {
		return "null", nil
	}
	src, err := query.Source()
	if err != nil {
		return "", err
	}
	jsonBytes, err := json.Marshal(src)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}

func TestConvertSingleQuotes(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"basic", `'hello world'`, `"hello world"`},
		{"internal_double_quote_escaped", `'abc"def'`, `"abc\"def"`},
		{"escaped_single_quote_unescaped", `'abc\'def'`, `"abc'def"`},
		{"double_quote_preserved", `"already double"`, `"already double"`},
		{"field_value", `field: 'value'`, `field: "value"`},
		{"mixed_quotes", `log: 'error' AND msg: "ok"`, `log: "error" AND msg: "ok"`},
		{"no_quotes", `plain query`, `plain query`},
		{"empty_single_quote", `''`, `""`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertSingleQuotes(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSingleQuoteAdaptation(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	fieldsMap := metadata.FieldsMap{
		"log": {
			IsAnalyzed: true,
			FieldType:  "text",
		},
		"message": {
			IsAnalyzed: true,
		},
	}

	testCases := map[string]struct {
		input string
		sql   string
		es    string
	}{
		"simple_single_quote": {
			input: `log: 'hello world'`,
			sql:   "`log` MATCH_PHRASE 'hello world'",
			es:    `{"match_phrase":{"log":{"query":"hello world"}}}`,
		},
		"single_quote_with_and": {
			input: `log: 'error and warning'`,
			sql:   "`log` MATCH_PHRASE 'error and warning'",
			es:    `{"match_phrase":{"log":{"query":"error and warning"}}}`,
		},
		"single_quote_field_value": {
			input: `status: 'active'`,
			sql:   "`status` = 'active'",
			es:    `{"term":{"status":"active"}}`,
		},
		"single_quote_with_escaped_single": {
			input: `log: 'it\'s working'`,
			sql:   "`log` MATCH_PHRASE 'it's working'",
			es:    `{"match_phrase":{"log":{"query":"it's working"}}}`,
		},
		"mixed_quotes": {
			input: `log: 'error' AND message: "warning"`,
			sql:   "`log` MATCH_PHRASE 'error' AND `message` MATCH_PHRASE 'warning'",
			es:    `{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"match_phrase":{"message":{"query":"warning"}}}]}}`,
		},
		"double_quote_preserved": {
			input: `log: "hello world"`,
			sql:   "`log` MATCH_PHRASE 'hello world'",
			es:    `{"match_phrase":{"log":{"query":"hello world"}}}`,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			node := ParseLuceneWithVisitor(ctx, tc.input, Option{
				FieldsMap:       fieldsMap,
				FieldEncodeFunc: fieldEncodeFunc,
			})
			assert.Nil(t, node.Error(), "should parse without error for input: %s", tc.input)
			assert.Equal(t, tc.sql, node.String(), "SQL mismatch for input: %s", tc.input)

			node = ParseLuceneWithVisitor(ctx, tc.input, Option{FieldsMap: fieldsMap})
			dsl := MergeQuery(node.DSL())
			dslJSON, _ := queryToJSON(dsl)
			assert.Equal(t, tc.es, dslJSON, "ES DSL mismatch for input: %s", tc.input)
		})
	}
}

func TestKeywordAsFieldValue(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	fieldsMap := metadata.FieldsMap{
		"log": {
			IsAnalyzed: true,
			FieldType:  "text",
		},
		"message": {
			IsAnalyzed: true,
		},
	}

	testCases := map[string]struct {
		input string
		sql   string
		es    string
	}{
		"and_as_field_value": {
			input: "log: and",
			sql:   "`log` MATCH_PHRASE 'and'",
			es:    `{"match_phrase":{"log":{"query":"and"}}}`,
		},
		"or_as_field_value": {
			input: "status: or",
			sql:   "`status` = 'or'",
			es:    `{"term":{"status":"or"}}`,
		},
		"not_as_field_value": {
			input: "status: not",
			sql:   "`status` = 'not'",
			es:    `{"term":{"status":"not"}}`,
		},
		"and_as_value_and_operator": {
			input: "log: and and status: or",
			sql:   "`log` MATCH_PHRASE 'and' AND `status` = 'or'",
			es:    `{"bool":{"must":[{"match_phrase":{"log":{"query":"and"}}},{"term":{"status":"or"}}]}}`,
		},
		"uppercase_AND_as_value": {
			input: "status: AND",
			sql:   "`status` = 'AND'",
			es:    `{"term":{"status":"AND"}}`,
		},
		"uppercase_OR_as_value": {
			input: "status: OR",
			sql:   "`status` = 'OR'",
			es:    `{"term":{"status":"OR"}}`,
		},
		"uppercase_NOT_as_value": {
			input: "status: NOT",
			sql:   "`status` = 'NOT'",
			es:    `{"term":{"status":"NOT"}}`,
		},
		"mixed_keyword_values_and_operators": {
			input: "log: NOT AND status: OR",
			sql:   "`log` MATCH_PHRASE 'NOT' AND `status` = 'OR'",
			es:    `{"bool":{"must":[{"match_phrase":{"log":{"query":"NOT"}}},{"term":{"status":"OR"}}]}}`,
		},
		"and_operator_still_works": {
			input: "A and B",
			sql:   "`log` MATCH_PHRASE 'A' AND `log` MATCH_PHRASE 'B'",
			es:    `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"A"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"B"}}]}}`,
		},
		"not_modifier_still_works": {
			input: "NOT active",
			sql:   "`log` NOT MATCH_PHRASE 'active'",
			es:    `{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"active"}}}}`,
		},
		"bang_not_still_works": {
			input: `!status:active`,
			sql:   "`status` != 'active'",
			es:    `{"bool":{"must_not":{"term":{"status":"active"}}}}`,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			node := ParseLuceneWithVisitor(ctx, tc.input, Option{
				FieldsMap:       fieldsMap,
				FieldEncodeFunc: fieldEncodeFunc,
			})
			t.Logf("Input:      %q", tc.input)
			t.Logf("Error:      %v", node.Error())
			t.Logf("Actual SQL: %s", node.String())
			t.Logf("Expect SQL: %s", tc.sql)
			assert.Nil(t, node.Error(), "should parse without error for input: %s", tc.input)
			assert.Equal(t, tc.sql, node.String(), "SQL mismatch for input: %s", tc.input)

			node = ParseLuceneWithVisitor(ctx, tc.input, Option{FieldsMap: fieldsMap})
			dsl := MergeQuery(node.DSL())
			dslJSON, _ := queryToJSON(dsl)
			t.Logf("Actual DSL: %s", dslJSON)
			t.Logf("Expect DSL: %s", tc.es)
			assert.Equal(t, tc.es, dslJSON, "ES DSL mismatch for input: %s", tc.input)
		})
	}
}

func TestNotEqualOperator(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	fieldsMap := metadata.FieldsMap{
		"log": {
			IsAnalyzed: true,
			FieldType:  "text",
		},
		"message": {
			IsAnalyzed: true,
		},
	}

	testCases := map[string]struct {
		input string
		sql   string
		es    string
	}{
		"not_equal_number": {
			input: "status != 200",
			sql:   "`status` != '200'",
			es:    `{"bool":{"must_not":{"term":{"status":"200"}}}}`,
		},
		"not_equal_string": {
			input: "status != active",
			sql:   "`status` != 'active'",
			es:    `{"bool":{"must_not":{"term":{"status":"active"}}}}`,
		},
		"not_equal_analyzed_field": {
			input: "log != error",
			sql:   "`log` NOT MATCH_PHRASE 'error'",
			es:    `{"bool":{"must_not":{"match_phrase":{"log":{"query":"error"}}}}}`,
		},
		"not_equal_with_colon": {
			input: "status:!= 200",
			sql:   "`status` != '200'",
			es:    `{"bool":{"must_not":{"term":{"status":"200"}}}}`,
		},
		"not_equal_combined_and": {
			input: "status != 200 AND log: error",
			sql:   "`status` != '200' AND `log` MATCH_PHRASE 'error'",
			es:    `{"bool":{"must":[{"bool":{"must_not":{"term":{"status":"200"}}}},{"match_phrase":{"log":{"query":"error"}}}]}}`,
		},
		"not_equal_combined_or": {
			input: "status != 200 OR status != 500",
			sql:   "`status` != '200' OR `status` != '500'",
			es:    `{"bool":{"should":[{"bool":{"must_not":{"term":{"status":"200"}}}},{"bool":{"must_not":{"term":{"status":"500"}}}}]}}`,
		},
		"bang_not_still_works": {
			input: "!status:active",
			sql:   "`status` != 'active'",
			es:    `{"bool":{"must_not":{"term":{"status":"active"}}}}`,
		},
		"greater_than_still_works": {
			input: "status > 200",
			sql:   "`status` > '200'",
			es:    `{"range":{"status":{"from":200,"include_lower":false,"include_upper":true,"to":null}}}`,
		},
	}

	for name, tc := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			node := ParseLuceneWithVisitor(ctx, tc.input, Option{
				FieldsMap:       fieldsMap,
				FieldEncodeFunc: fieldEncodeFunc,
			})
			t.Logf("Input:      %q", tc.input)
			t.Logf("Error:      %v", node.Error())
			t.Logf("Actual SQL: %s", node.String())
			t.Logf("Expect SQL: %s", tc.sql)
			assert.Nil(t, node.Error(), "should parse without error for input: %s", tc.input)
			assert.Equal(t, tc.sql, node.String(), "SQL mismatch for input: %s", tc.input)

			node = ParseLuceneWithVisitor(ctx, tc.input, Option{FieldsMap: fieldsMap})
			dsl := MergeQuery(node.DSL())
			dslJSON, _ := queryToJSON(dsl)
			t.Logf("Actual DSL: %s", dslJSON)
			t.Logf("Expect DSL: %s", tc.es)
			assert.Equal(t, tc.es, dslJSON, "ES DSL mismatch for input: %s", tc.input)
		})
	}
}
