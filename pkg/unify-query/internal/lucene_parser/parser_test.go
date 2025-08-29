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
	"encoding/json"
	"testing"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/assert"
)

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

func TestParser(t *testing.T) {
	testCases := map[string]struct {
		q   string
		e   Expr
		es  string
		sql string
	}{
		"正常查询": {
			q: `test`,
			e: &MatchExpr{
				Value: &StringExpr{Value: `test`},
			},
			es:  `{"query_string":{"query":"test"}}`,
			sql: "`log` = 'test'",
		},
		"负数查询": {
			q: `-test`,
			e: &NotExpr{
				Expr: &MatchExpr{
					Value: &StringExpr{Value: `test`},
				},
			},
			es:  `{"bool":{"must_not":{"query_string":{"query":"test"}}}}`,
			sql: "NOT (`log` = 'test')",
		},
		"负数查询多条件": {
			q: `-test AND good`,
			e: &AndExpr{
				Left: &NotExpr{
					Expr: &MatchExpr{
						Value: &StringExpr{Value: `test`},
					},
				},
				Right: &MatchExpr{
					Value: &StringExpr{Value: `good`},
				},
			},
			es:  `{"bool":{"must":[{"bool":{"must_not":{"query_string":{"query":"test"}}}},{"query_string":{"query":"good"}}]}}`,
			sql: "NOT (`log` = 'test') AND `log` = 'good'",
		},
		"通配符匹配": {
			q: `qu?ck bro*`,
			e: &OrExpr{
				Left: &WildcardExpr{
					Value: &StringExpr{Value: "qu?ck"},
				},
				Right: &WildcardExpr{
					Value: &StringExpr{Value: "bro*"},
				},
			},
			es:  `{"bool":{"should":[{"query_string":{"query":"qu?ck"}},{"query_string":{"query":"bro*"}}]}}`,
			sql: "`log` LIKE '%qu_ck%' OR `log` LIKE 'bro%'",
		},
		"无条件正则匹配": {
			q: `/joh?n(ath[oa]n)/`,
			e: &RegexpExpr{
				Value: &StringExpr{Value: "joh?n(ath[oa]n)"},
			},
			es:  `{"query_string":{"query":"/joh?n(ath[oa]n)/"}}`,
			sql: "`log` REGEXP 'joh?n(ath[oa]n)'",
		},
		"正则匹配": {
			q: `name: /joh?n(ath[oa]n)/`,
			e: &RegexpExpr{
				Field: &StringExpr{Value: "name"},
				Value: &StringExpr{Value: "joh?n(ath[oa]n)"},
			},
			es:  `{"regexp":{"name":{"value":"joh?n(ath[oa]n)"}}}`,
			sql: "`name` REGEXP 'joh?n(ath[oa]n)'",
		},
		"范围匹配，左闭右开": {
			q: `count:[1 TO 5}`,
			e: &NumberRangeExpr{
				Field:        &StringExpr{Value: "count"},
				Start:        &StringExpr{Value: "1"},
				End:          &StringExpr{Value: "5"},
				IncludeStart: &BoolExpr{Value: true},
				IncludeEnd:   &BoolExpr{Value: false},
			},
			es:  `{"range":{"count":{"from":1,"include_lower":true,"include_upper":false,"to":5}}}`,
			sql: "`count` >= 1 AND `count` < 5",
		},
		"范围匹配": {
			q: `count:[1 TO 5]`,
			e: &NumberRangeExpr{
				Field:        &StringExpr{Value: "count"},
				Start:        &StringExpr{Value: "1"},
				End:          &StringExpr{Value: "5"},
				IncludeStart: &BoolExpr{Value: true},
				IncludeEnd:   &BoolExpr{Value: true},
			},
			es:  `{"range":{"count":{"from":1,"include_lower":true,"include_upper":true,"to":5}}}`,
			sql: "`count` >= 1 AND `count` <= 5",
		},
		"范围匹配（无下限） - 1": {
			q: `count:{* TO 10]`,
			e: &NumberRangeExpr{
				Field:        &StringExpr{Value: "count"},
				Start:        &StringExpr{Value: "*"},
				End:          &StringExpr{Value: "10"},
				IncludeStart: &BoolExpr{Value: false},
				IncludeEnd:   &BoolExpr{Value: true},
			},
			es:  `{"range":{"count":{"from":null,"include_lower":false,"include_upper":true,"to":10}}}`,
			sql: "`count` <= 10",
		},
		"范围匹配（无下限）": {
			q: `count:[* TO 10]`,
			e: &NumberRangeExpr{
				Field:        &StringExpr{Value: "count"},
				Start:        &StringExpr{Value: "*"},
				End:          &StringExpr{Value: "10"},
				IncludeStart: &BoolExpr{Value: true},
				IncludeEnd:   &BoolExpr{Value: true},
			},
			es:  `{"range":{"count":{"from":null,"include_lower":true,"include_upper":true,"to":10}}}`,
			sql: "`count` <= 10",
		},
		"范围匹配（无上限）": {
			q: `count:[10 TO *]`,
			e: &NumberRangeExpr{
				Field:        &StringExpr{Value: "count"},
				Start:        &StringExpr{Value: "10"},
				End:          &StringExpr{Value: "*"},
				IncludeStart: &BoolExpr{Value: true},
				IncludeEnd:   &BoolExpr{Value: true},
			},
			es:  `{"range":{"count":{"from":10,"include_lower":true,"include_upper":true,"to":null}}}`,
			sql: "`count` >= 10",
		},
		"范围匹配（无上限）- 1": {
			q: `count:[10 TO *}`,
			e: &NumberRangeExpr{
				Field:        &StringExpr{Value: "count"},
				Start:        &StringExpr{Value: "10"},
				End:          &StringExpr{Value: "*"},
				IncludeStart: &BoolExpr{Value: true},
				IncludeEnd:   &BoolExpr{Value: false},
			},
			es:  `{"range":{"count":{"from":10,"include_lower":true,"include_upper":false,"to":null}}}`,
			sql: "`count` >= 10",
		},
		"字段匹配": {
			q: `status:active`,
			e: &MatchExpr{
				Field: &StringExpr{Value: "status"},
				Value: &StringExpr{Value: "active"},
			},
			es:  `{"term":{"status":"active"}}`,
			sql: "`status` = 'active'",
		},
		"字段匹配 + 括号": {
			q: `status:(active)`,
			e: &GroupingExpr{
				Expr: &MatchExpr{
					Field: &StringExpr{Value: "status"},
					Value: &StringExpr{Value: "active"},
				},
			},
			es:  `{"term":{"status":"active"}}`,
			sql: "(`status` = 'active')",
		},
		"多条件组合，括号调整优先级": {
			q: `author:"John Smith" AND (age:20 OR status:active)`,
			e: &AndExpr{
				Left: &MatchExpr{
					Field:    &StringExpr{Value: "author"},
					Value:    &StringExpr{Value: "John Smith"},
					IsQuoted: true,
				},
				Right: &GroupingExpr{ // Wrap the OR expression
					Expr: &OrExpr{
						Left: &MatchExpr{
							Field: &StringExpr{Value: "age"},
							Value: &StringExpr{Value: "20"},
						},
						Right: &MatchExpr{
							Field: &StringExpr{Value: "status"},
							Value: &StringExpr{Value: "active"},
						},
					},
				},
			},
			es:  `{"bool":{"must":[{"match_phrase":{"author":{"query":"John Smith"}}},{"bool":{"should":[{"term":{"age":20}},{"term":{"status":"active"}}]}}]}}`,
			sql: "`author` = 'John Smith' AND (`age` = '20' OR `status` = 'active')",
		},
		"多条件组合，and 和 or 的优先级": {
			q: `(author:"John Smith" AND age:20) OR status:active`,
			e: &OrExpr{
				Left: &GroupingExpr{ // Wrap the AND expression
					Expr: &AndExpr{
						Left: &MatchExpr{
							Field:    &StringExpr{Value: "author"},
							Value:    &StringExpr{Value: "John Smith"},
							IsQuoted: true,
						},
						Right: &MatchExpr{
							Field: &StringExpr{Value: "age"},
							Value: &StringExpr{Value: "20"},
						},
					},
				},
				Right: &MatchExpr{
					Field: &StringExpr{Value: "status"},
					Value: &StringExpr{Value: "active"},
				},
			},
			es:  `{"bool":{"should":[{"bool":{"must":[{"match_phrase":{"author":{"query":"John Smith"}}},{"term":{"age":20}}]}},{"term":{"status":"active"}}]}}`,
			sql: "(`author` = 'John Smith' AND `age` = '20') OR `status` = 'active'",
		},
		"嵌套逻辑表达式": {
			q: `a:1 AND (b:2 OR c:3)`,
			e: &AndExpr{
				Left: &MatchExpr{
					Field: &StringExpr{Value: "a"},
					Value: &StringExpr{Value: "1"},
				},
				Right: &GroupingExpr{
					Expr: &OrExpr{
						Left: &MatchExpr{
							Field: &StringExpr{Value: "b"},
							Value: &StringExpr{Value: "2"},
						},
						Right: &MatchExpr{
							Field: &StringExpr{Value: "c"},
							Value: &StringExpr{Value: "3"},
						},
					},
				},
			},
			es:  `{"bool":{"must":[{"term":{"a":1}},{"bool":{"should":[{"term":{"b":2}},{"term":{"c":3}}]}}]}}`,
			sql: "`a` = '1' AND (`b` = '2' OR `c` = '3')",
		},
		"嵌套逻辑表达式 - 2": {
			q: `a:1 OR b:2 OR (c:3 OR d:4)`,
			e: &OrExpr{
				Left: &MatchExpr{
					Field: &StringExpr{Value: "a"},
					Value: &StringExpr{Value: "1"},
				},
				Right: &OrExpr{
					Left: &MatchExpr{
						Field: &StringExpr{Value: "b"},
						Value: &StringExpr{Value: "2"},
					},
					Right: &GroupingExpr{
						Expr: &OrExpr{
							Left: &MatchExpr{
								Field: &StringExpr{Value: "c"},
								Value: &StringExpr{Value: "3"},
							},
							Right: &MatchExpr{
								Field: &StringExpr{Value: "d"},
								Value: &StringExpr{Value: "4"},
							},
						},
					},
				},
			},
			es:  `{"bool":{"should":[{"term":{"a":1}},{"term":{"b":2}},{"bool":{"should":[{"term":{"c":3}},{"term":{"d":4}}]}}]}}`,
			sql: "`a` = '1' OR `b` = '2' OR (`c` = '3' OR `d` = '4')",
		},
		"嵌套逻辑表达式 - 3": {
			q: `a:1 OR (b:2 OR c:3) OR d:4`,
			e: &OrExpr{
				Left: &MatchExpr{
					Field: &StringExpr{Value: "a"},
					Value: &StringExpr{Value: "1"},
				},
				Right: &OrExpr{
					Left: &GroupingExpr{
						Expr: &OrExpr{
							Left: &MatchExpr{
								Field: &StringExpr{Value: "b"},
								Value: &StringExpr{Value: "2"},
							},
							Right: &MatchExpr{
								Field: &StringExpr{Value: "c"},
								Value: &StringExpr{Value: "3"},
							},
						},
					},
					Right: &MatchExpr{
						Field: &StringExpr{Value: "d"},
						Value: &StringExpr{Value: "4"},
					},
				},
			},
			es:  `{"bool":{"should":[{"term":{"a":1}},{"bool":{"should":[{"term":{"b":2}},{"term":{"c":3}}]}},{"term":{"d":4}}]}}`,
			sql: "`a` = '1' OR (`b` = '2' OR `c` = '3') OR `d` = '4'",
		},
		"嵌套逻辑表达式 - 4": {
			q: `a:1 OR (b:2 OR c:3) AND d:4`,
			e: &OrExpr{
				Left: &MatchExpr{
					Field: &StringExpr{Value: "a"},
					Value: &StringExpr{Value: "1"},
				},
				Right: &AndExpr{
					Left: &GroupingExpr{
						Expr: &OrExpr{
							Left: &MatchExpr{
								Field: &StringExpr{Value: "b"},
								Value: &StringExpr{Value: "2"},
							},
							Right: &MatchExpr{
								Field: &StringExpr{Value: "c"},
								Value: &StringExpr{Value: "3"},
							},
						},
					},
					Right: &MatchExpr{
						Field: &StringExpr{Value: "d"},
						Value: &StringExpr{Value: "4"},
					},
				},
			},
			es:  `{"bool":{"should":[{"term":{"a":1}},{"bool":{"must":[{"bool":{"should":[{"term":{"b":2}},{"term":{"c":3}}]}},{"term":{"d":4}}]}}]}}`,
			sql: "`a` = '1' OR ((`b` = '2' OR `c` = '3') AND `d` = '4')",
		},
		"new-1": {
			q: `quick brown +fox -news`,
			e: &AndExpr{
				Left: &OrExpr{
					Left: &OrExpr{
						Left: &AndExpr{
							Left: &MatchExpr{
								Value: &StringExpr{Value: "quick"},
							},
							Right: &MatchExpr{
								Value: &StringExpr{Value: "fox"},
							},
						},
						Right: &AndExpr{
							Left: &MatchExpr{
								Value: &StringExpr{Value: "brown"},
							},
							Right: &MatchExpr{
								Value: &StringExpr{Value: "fox"},
							},
						},
					},
					Right: &MatchExpr{
						Value: &StringExpr{Value: "fox"},
					},
				},
				Right: &NotExpr{
					Expr: &MatchExpr{
						Value: &StringExpr{Value: "news"},
					},
				},
			},
			es:  `{"bool":{"must":[{"bool":{"should":[{"bool":{"must":[{"query_string":{"query":"quick"}},{"query_string":{"query":"fox"}}]}},{"bool":{"must":[{"query_string":{"query":"brown"}},{"query_string":{"query":"fox"}}]}},{"query_string":{"query":"fox"}}]}},{"bool":{"must_not":{"query_string":{"query":"news"}}}}]}}`,
			sql: "((`log` = 'quick' AND `log` = 'fox') OR (`log` = 'brown' AND `log` = 'fox') OR `log` = 'fox') AND NOT (`log` = 'news')",
		},
		"模糊匹配": {
			q: `quick brown fox`,
			e: &OrExpr{
				Left: &OrExpr{
					Left: &MatchExpr{
						Value: &StringExpr{Value: "quick"},
					},
					Right: &MatchExpr{
						Value: &StringExpr{Value: "brown"},
					},
				},
				Right: &MatchExpr{
					Value: &StringExpr{Value: "fox"},
				},
			},
			es:  `{"bool":{"should":[{"query_string":{"query":"quick"}},{"query_string":{"query":"brown"}},{"query_string":{"query":"fox"}}]}}`,
			sql: "`log` = 'quick' OR `log` = 'brown' OR `log` = 'fox'",
		},
		"单个条件精确匹配": {
			q: `log: "ERROR MSG"`,
			e: &MatchExpr{
				Field:    &StringExpr{Value: "log"},
				Value:    &StringExpr{Value: "ERROR MSG"},
				IsQuoted: true,
			},
			es:  `{"match_phrase":{"log":{"query":"ERROR MSG"}}}`,
			sql: "`log` = 'ERROR MSG'",
		},
		"match and time range": {
			q: "message: test\\ node AND datetime: [\"2020-01-01T00:00:00\" TO \"2020-12-31T00:00:00\"]",
			e: &AndExpr{
				Left: &MatchExpr{
					Field: &StringExpr{Value: "message"},
					Value: &StringExpr{Value: "test node"},
				},
				Right: &TimeRangeExpr{
					Field:        &StringExpr{Value: "datetime"},
					Start:        &StringExpr{Value: "2020-01-01T00:00:00"},
					End:          &StringExpr{Value: "2020-12-31T00:00:00"},
					IncludeStart: &BoolExpr{Value: true},
					IncludeEnd:   &BoolExpr{Value: true},
				},
			},
			es: `{"bool":{"must":[{"match":{"message":{"query":"test node"}}},{"range":{"datetime":{"from":"2020-01-01T00:00:00","include_lower":true,"include_upper":true,"to":"2020-12-31T00:00:00"}}}]}}`,
		},
		"mixed or / and": {
			q: "a:1 OR (b:2 AND c:4)",
			e: &OrExpr{
				Left: &MatchExpr{
					Field: &StringExpr{Value: "a"},
					Value: &StringExpr{Value: "1"},
				},
				Right: &GroupingExpr{
					Expr: &AndExpr{
						Left: &MatchExpr{
							Field: &StringExpr{Value: "b"},
							Value: &StringExpr{Value: "2"},
						},
						Right: &MatchExpr{
							Field: &StringExpr{Value: "c"},
							Value: &StringExpr{Value: "4"},
						},
					},
				},
			},
			es:  `{"bool":{"should":[{"term":{"a":1}},{"bool":{"must":[{"term":{"b":2}},{"term":{"c":4}}]}}]}}`,
			sql: "`a` = '1' OR (`b` = '2' AND `c` = '4')",
		},
		"start without tCOLON": {
			q: "a > 100",
			e: &NumberRangeExpr{
				Field: &StringExpr{Value: "a"},
				Start: &StringExpr{Value: "100"},
			},
			es:  `{"range":{"a":{"from":100,"include_lower":false,"include_upper":true,"to":null}}}`,
			sql: "`a` > 100",
		},
		"end without tCOLON": {
			q: "a < 100",
			e: &NumberRangeExpr{
				Field: &StringExpr{Value: "a"},
				End:   &StringExpr{Value: "100"},
			},
			es:  `{"range":{"a":{"from":null,"include_lower":true,"include_upper":false,"to":100}}}`,
			sql: "`a` < 100",
		},
		"start and eq without tCOLON": {
			q: "a >= 100",
			e: &NumberRangeExpr{
				Field:        &StringExpr{Value: "a"},
				Start:        &StringExpr{Value: "100"},
				IncludeStart: &BoolExpr{Value: true},
			},
			es:  `{"range":{"a":{"from":100,"include_lower":true,"include_upper":true,"to":null}}}`,
			sql: "`a` >= 100",
		},
		"end and eq without tCOLON": {
			q: "a <= 100",
			e: &NumberRangeExpr{
				Field:      &StringExpr{Value: "a"},
				End:        &StringExpr{Value: "100"},
				IncludeEnd: &BoolExpr{Value: true},
			},
			es:  `{"range":{"a":{"from":null,"include_lower":true,"include_upper":true,"to":100}}}`,
			sql: "`a` <= 100",
		},
		"start": {
			q: "a>100",
			e: &NumberRangeExpr{
				Field: &StringExpr{Value: "a"},
				Start: &StringExpr{Value: "100"},
			},
			es:  `{"range":{"a":{"from":100,"include_lower":false,"include_upper":true,"to":null}}}`,
			sql: "`a` > 100",
		},
		"one word left star": {
			q: "*test",
			e: &WildcardExpr{
				Value: &StringExpr{Value: "*test"},
			},
			es:  `{"query_string":{"query":"*test"}}`,
			sql: "`log` LIKE '%test'",
		},
		"one word right star": {
			q: "test*",
			e: &WildcardExpr{
				Value: &StringExpr{Value: "test*"},
			},
			es:  `{"query_string":{"query":"test*"}}`,
			sql: "`log` LIKE 'test%'",
		},
		"one word double star": {
			q: "*test*",
			e: &WildcardExpr{
				Value: &StringExpr{Value: "*test*"},
			},
			es:  `{"query_string":{"query":"*test*"}}`,
			sql: "`log` LIKE '%test%'",
		},
		"one int double star": {
			q: "*123*",
			e: &WildcardExpr{
				Value: &StringExpr{Value: "*123*"},
			},
			es:  `{"query_string":{"query":"*123*"}}`,
			sql: "`log` LIKE '%123%'",
		},
		"key node with star": {
			q: "events.attributes.message.detail: *66036*",
			e: &WildcardExpr{
				Field: &StringExpr{Value: "events.attributes.message.detail"},
				Value: &StringExpr{Value: "*66036*"},
			},
			es:  `{"wildcard":{"events.attributes.message.detail":{"value":"*66036*"}}}`,
			sql: "`events.attributes.message.detail` LIKE '%66036%'",
		},
		"node like regex": {
			q: `"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log" AND level: "error" AND "2_bklog.bkunify_query"`,
			e: &AndExpr{
				Left: &AndExpr{
					Left: &MatchExpr{
						Value:    &StringExpr{Value: "/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log"},
						IsQuoted: true,
					},
					Right: &MatchExpr{
						Field:    &StringExpr{Value: "level"},
						Value:    &StringExpr{Value: "error"},
						IsQuoted: true,
					},
				},
				Right: &MatchExpr{
					Value:    &StringExpr{Value: "2_bklog.bkunify_query"},
					IsQuoted: true,
				},
			},
			es:  `{"bool":{"must":[{"query_string":{"query":"\"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log\""}},{"match_phrase":{"level":{"query":"error"}}},{"query_string":{"query":"\"2_bklog.bkunify_query\""}}]}}`,
			sql: "`log` = '/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log' AND `level` = 'error' AND `log` = '2_bklog.bkunify_query'",
		},
		"双引号转义符号支持": {
			q: `log: "(reading \\\"remove\\\")"`,
			e: &MatchExpr{
				Field:    &StringExpr{Value: "log"},
				Value:    &StringExpr{Value: `(reading \"remove\")`},
				IsQuoted: true,
			},
			es:  `{"match_phrase":{"log":{"query":"(reading \\\"remove\\\")"}}}`,
			sql: "`log` = '(reading \"remove\")'",
		},
		"test": {
			q: `path: "/proz/logds/ds-5910974792526317*"`,
			e: &WildcardExpr{
				Field: &StringExpr{Value: "path"},
				Value: &StringExpr{Value: "/proz/logds/ds-5910974792526317*"},
			},
			es:  `{"wildcard":{"path":{"value":"/proz/logds/ds-5910974792526317*"}}}`,
			sql: "`path` LIKE '/proz/logds/ds-5910974792526317%'",
		},
		"test-1": {
			q: "\"32221112\" AND path: \"/data/home/user00/log/zonesvr*\"",
			e: &AndExpr{
				Left: &MatchExpr{
					Value:    &StringExpr{Value: "32221112"},
					IsQuoted: true,
				},
				Right: &WildcardExpr{
					Field: &StringExpr{Value: "path"},
					Value: &StringExpr{Value: "/data/home/user00/log/zonesvr*"},
				},
			},
			es:  `{"bool":{"must":[{"query_string":{"query":"\"32221112\""}},{"wildcard":{"path":{"value":"/data/home/user00/log/zonesvr*"}}}]}}`,
			sql: "`log` = '32221112' AND `path` LIKE '/data/home/user00/log/zonesvr%'",
		},
		"test - Many Brack ": {
			q: `(loglevel: ("TRACE" OR "DEBUG" OR "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")) AND "test111"`,
			e: &AndExpr{
				Left: &GroupingExpr{
					Expr: &AndExpr{
						Left: &ConditionMatchExpr{
							Field: &StringExpr{Value: "loglevel"},
							Value: &ConditionExpr{
								Values: [][]Expr{{&StringExpr{Value: "TRACE"}}, {&StringExpr{Value: "DEBUG"}}, {&StringExpr{Value: "INFO "}}, {&StringExpr{Value: "WARN "}}, {&StringExpr{Value: "ERROR"}}},
							},
						},
						Right: &ConditionMatchExpr{
							Field: &StringExpr{Value: "log"},
							Value: &ConditionExpr{
								Values: [][]Expr{{&StringExpr{Value: "friendsvr"}, &StringExpr{Value: "game_app"}, &StringExpr{Value: "testAnd"}}, {&StringExpr{Value: "friendsvr"}, &StringExpr{Value: "testOr"}, &StringExpr{Value: "testAnd"}}, {&StringExpr{Value: "test111"}}},
							},
						},
					},
				},
				Right: &MatchExpr{
					Value:    &StringExpr{Value: "test111"},
					IsQuoted: true,
				},
			},
			es:  `{"bool":{"must":[{"bool":{"must":[{"terms":{"loglevel":["TRACE","DEBUG","INFO ","WARN ","ERROR"]}},{"bool":{"minimum_should_match":"1","should":[{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testAnd"}}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"match_phrase":{"log":{"query":"testOr"}}},{"match_phrase":{"log":{"query":"testAnd"}}}]}},{"match_phrase":{"log":{"query":"test111"}}}]}}]}},{"query_string":{"query":"\"test111\""}}]}}`,
			sql: "((`loglevel` LIKE '%TRACE%' OR `loglevel` LIKE '%DEBUG%' OR `loglevel` LIKE '%INFO %' OR `loglevel` LIKE '%WARN %' OR `loglevel` LIKE '%ERROR%') AND ((`log` LIKE '%friendsvr%' AND `log` LIKE '%game_app%' AND `log` LIKE '%testAnd%') OR (`log` LIKE '%friendsvr%' AND `log` LIKE '%testOr%' AND `log` LIKE '%testAnd%') OR `log` LIKE '%test111%')) AND `log` = 'test111'",
		},
		"test - many tPHRASE ": {
			q: `loglevel: ("TRACE" OR "DEBUG" OR "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			e: &AndExpr{
				Left: &ConditionMatchExpr{
					Field: &StringExpr{Value: "loglevel"},
					Value: &ConditionExpr{
						Values: [][]Expr{{&StringExpr{Value: "TRACE"}}, {&StringExpr{Value: "DEBUG"}}, {&StringExpr{Value: "INFO "}}, {&StringExpr{Value: "WARN "}}, {&StringExpr{Value: "ERROR"}}},
					},
				},
				Right: &ConditionMatchExpr{
					Field: &StringExpr{Value: "log"},
					Value: &ConditionExpr{
						Values: [][]Expr{{&StringExpr{Value: "friendsvr"}, &StringExpr{Value: "game_app"}, &StringExpr{Value: "testAnd"}}, {&StringExpr{Value: "friendsvr"}, &StringExpr{Value: "testOr"}, &StringExpr{Value: "testAnd"}}, {&StringExpr{Value: "test111"}}},
					},
				},
			},
			es:  `{"bool":{"must":[{"terms":{"loglevel":["TRACE","DEBUG","INFO ","WARN ","ERROR"]}},{"bool":{"minimum_should_match":"1","should":[{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testAnd"}}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"match_phrase":{"log":{"query":"testOr"}}},{"match_phrase":{"log":{"query":"testAnd"}}}]}},{"match_phrase":{"log":{"query":"test111"}}}]}}]}}`,
			sql: "(`loglevel` LIKE '%TRACE%' OR `loglevel` LIKE '%DEBUG%' OR `loglevel` LIKE '%INFO %' OR `loglevel` LIKE '%WARN %' OR `loglevel` LIKE '%ERROR%') AND ((`log` LIKE '%friendsvr%' AND `log` LIKE '%game_app%' AND `log` LIKE '%testAnd%') OR (`log` LIKE '%friendsvr%' AND `log` LIKE '%testOr%' AND `log` LIKE '%testAnd%') OR `log` LIKE '%test111%')",
		},
		"test - Single Bracket And  ": {
			q: `loglevel: ("TRACE" AND "111" AND "DEBUG" AND "INFO" OR "SIMON" OR "222" AND "333" )`,
			e: &ConditionMatchExpr{
				Field: &StringExpr{Value: "loglevel"},
				Value: &ConditionExpr{
					Values: [][]Expr{{&StringExpr{Value: "TRACE"}, &StringExpr{Value: "111"}, &StringExpr{Value: "DEBUG"}, &StringExpr{Value: "INFO"}}, {&StringExpr{Value: "SIMON"}}, {&StringExpr{Value: "222"}, &StringExpr{Value: "333"}}},
				},
			},
			es:  `{"bool":{"minimum_should_match":"1","should":[{"bool":{"must":[{"term":{"loglevel":"TRACE"}},{"term":{"loglevel":"111"}},{"term":{"loglevel":"DEBUG"}},{"term":{"loglevel":"INFO"}}]}},{"term":{"loglevel":"SIMON"}},{"bool":{"must":[{"term":{"loglevel":"222"}},{"term":{"loglevel":"333"}}]}}]}}`,
			sql: "(`loglevel` LIKE '%TRACE%' AND `loglevel` LIKE '%111%' AND `loglevel` LIKE '%DEBUG%' AND `loglevel` LIKE '%INFO%') OR `loglevel` LIKE '%SIMON%' OR (`loglevel` LIKE '%222%' AND `loglevel` LIKE '%333%')",
		},
		"test - Self Bracket ": {
			q: `loglevel: ("TRACE" OR ("DEBUG") OR ("INFO ") OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			e: &AndExpr{
				Left: &ConditionMatchExpr{
					Field: &StringExpr{Value: "loglevel"},
					Value: &ConditionExpr{
						Values: [][]Expr{{&StringExpr{Value: "TRACE"}}, {&StringExpr{Value: "DEBUG"}}, {&StringExpr{Value: "INFO "}}, {&StringExpr{Value: "WARN "}}, {&StringExpr{Value: "ERROR"}}},
					},
				},
				Right: &ConditionMatchExpr{
					Field: &StringExpr{Value: "log"},
					Value: &ConditionExpr{
						Values: [][]Expr{{&StringExpr{Value: "friendsvr"}, &StringExpr{Value: "game_app"}, &StringExpr{Value: "testAnd"}}, {&StringExpr{Value: "friendsvr"}, &StringExpr{Value: "testOr"}, &StringExpr{Value: "testAnd"}}, {&StringExpr{Value: "test111"}}},
					},
				},
			},
			es:  `{"bool":{"must":[{"terms":{"loglevel":["TRACE","DEBUG","INFO ","WARN ","ERROR"]}},{"bool":{"minimum_should_match":"1","should":[{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"match_phrase":{"log":{"query":"game_app"}}},{"match_phrase":{"log":{"query":"testAnd"}}}]}},{"bool":{"must":[{"match_phrase":{"log":{"query":"friendsvr"}}},{"match_phrase":{"log":{"query":"testOr"}}},{"match_phrase":{"log":{"query":"testAnd"}}}]}},{"match_phrase":{"log":{"query":"test111"}}}]}}]}}`,
			sql: "(`loglevel` LIKE '%TRACE%' OR `loglevel` LIKE '%DEBUG%' OR `loglevel` LIKE '%INFO %' OR `loglevel` LIKE '%WARN %' OR `loglevel` LIKE '%ERROR%') AND ((`log` LIKE '%friendsvr%' AND `log` LIKE '%game_app%' AND `log` LIKE '%testAnd%') OR (`log` LIKE '%friendsvr%' AND `log` LIKE '%testOr%' AND `log` LIKE '%testAnd%') OR `log` LIKE '%test111%')",
		},
	}
	parser := NewParser(WithEsSchema(loadEsMapping()))
	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			rt, err := parser.Do(c.q)
			if err != nil {
				t.Errorf("Parse returned an error: %s", err)
				return
			}
			assert.Equal(t, c.e, rt.Expr, "Expression mismatch")

			// Test SQL conversion if expected SQL is provided
			if c.sql != "" {
				assert.Equal(t, c.sql, rt.SQL, "SQL conversion mismatch for query: %s", c.q)
			}

			if c.es != "" {
				esStr, err := queryToJSON(rt.ES)
				assert.Nil(t, err, "ES JSON marshal error for query: %s", c.q)
				assert.Equal(t, c.es, esStr, "ES conversion mismatch for query: %s", c.q)
			}
		})
	}
}

func loadEsMapping() *Schema {
	testSchema := &Schema{fieldTypes: make(map[string]FieldType)}
	testSchema.SetFieldType("age", FieldTypeLong)
	testSchema.SetFieldType("count", FieldTypeLong)
	testSchema.SetFieldType("a", FieldTypeLong)
	testSchema.SetFieldType("b", FieldTypeLong)
	testSchema.SetFieldType("c", FieldTypeLong)
	testSchema.SetFieldType("d", FieldTypeLong)
	testSchema.SetFieldType("status", FieldTypeKeyword)
	testSchema.SetFieldType("level", FieldTypeText)
	testSchema.SetFieldType("loglevel", FieldTypeKeyword)
	testSchema.SetFieldType("author", FieldTypeText)
	testSchema.SetFieldType("message", FieldTypeText)
	testSchema.SetFieldType("log", FieldTypeText)
	testSchema.SetFieldType("path", FieldTypeKeyword)
	return testSchema
}
