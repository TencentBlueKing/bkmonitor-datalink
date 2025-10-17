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
			sql:   "`name` = 'test'",
			dsl:   `{"term":{"name":"test"}}`,
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
			sql: "`log` MATCH_PHRASE 'quick' AND `log` MATCH_PHRASE 'fox' AND `log` NOT MATCH_PHRASE 'news' OR `log` MATCH_PHRASE 'brown' AND `log` MATCH_PHRASE 'fox' AND `log` NOT MATCH_PHRASE 'news'",
		},
		"new-2": {
			q:   `quick -news`,
			es:  `{"bool":{"must":{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"news"}}}},"should":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"quick"}}}}`,
			sql: "`log` MATCH_PHRASE 'quick' AND `log` NOT MATCH_PHRASE 'news'",
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

			es:  `{"wildcard":{"path":{"value":"/proz/logds/ds-5910974792526317*"}}}`,
			sql: "`path` LIKE '/proz/logds/ds-5910974792526317%'",
		},
		"test-1": {
			q: "\"32221112\" AND path: \"/data/home/user00/log/zonesvr*\"",

			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"32221112\""}},{"wildcard":{"path":{"value":"/data/home/user00/log/zonesvr*"}}}]}}`,
			sql: "`log` MATCH_PHRASE '32221112' AND `path` LIKE '/data/home/user00/log/zonesvr%'",
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
			es:  `{"bool":{"must":[{"bool":{"should":[{"wildcard":{"loglevel":{"value":"*TRACE*"}}},{"wildcard":{"loglevel":{"value":"*DEBUG*"}}},{"wildcard":{"loglevel":{"value":"*INFO*"}}},{"wildcard":{"loglevel":{"value":"*WARN*"}}},{"wildcard":{"loglevel":{"value":"*ERROR*"}}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"bool":{"should":[{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testOr"}}}]}},{"match_phrase":{"log":{"query":"testAnd"}}}],"should":{"match_phrase":{"log":{"query":"test111"}}}}}]}}`,
			sql: "(`loglevel` LIKE '%TRACE%' OR `loglevel` LIKE '%DEBUG%' OR `loglevel` LIKE '%INFO%' OR `loglevel` LIKE '%WARN%' OR `loglevel` LIKE '%ERROR%') AND (`log` MATCH_PHRASE 'friendsvr' AND (`log` MATCH_PHRASE 'game_app' OR `log` MATCH_PHRASE 'testOr') AND `log` MATCH_PHRASE 'testAnd' OR `log` MATCH_PHRASE 'test111')",
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
		// 并不支持 _exists_ 语法糖,不存在于词法文件中
		//"field_query_exists": {
		//	q:   `_exists_:author`,
		//	n:   &ConditionNode{field: &StringNode{Value: "_exists_"}, op: OpMatch, value: &StringNode{Value: "author"}},
		//	es:  `{"exists":{"field":"author"}}`,
		//	sql: "`author` IS NOT NULL",
		//},
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
			sql: "`log` MATCH_PHRASE 'term1' AND `log` NOT MATCH_PHRASE 'term2'",
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
			sql: "`log` MATCH_PHRASE 'error' AND `status` != 'active'",
		},
		"case_insensitive_not_mixed": {
			q:   `log:error Not status:active`,
			es:  `{"bool":{"must":{"bool":{"must_not":{"term":{"status":"active"}}}},"should":{"match_phrase":{"log":{"query":"error"}}}}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` != 'active'",
		},
		"case_insensitive_mixed_complex": {
			q:   `(log:error AND status:active) or (level:warn Not type:system)`,
			es:  `{"bool":{"should":[{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}},{"bool":{"must":{"bool":{"must_not":{"term":{"type":"system"}}}},"should":{"term":{"level":"warn"}}}}]}}`,
			sql: "(`log` MATCH_PHRASE 'error' AND `status` = 'active') OR (`level` = 'warn' AND `type` != 'system')",
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
