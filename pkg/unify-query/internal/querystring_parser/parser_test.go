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
	"testing"

	"github.com/stretchr/testify/assert"
)

/*
精确匹配(支持AND、OR)：
author:"John Smith" AND age:20
字段名匹配(*代表通配符)：
status:active
title:(quick brown)
字段名模糊匹配：
vers\*on:(quick brown)
通配符匹配：
qu?ck bro*
正则匹配：
name:/joh?n(ath[oa]n)/
范围匹配：
count:[1 TO 5]
count:[1 TO 5}
count:[10 TO *]
*/
func TestParser(t *testing.T) {
	testCases := map[string]struct {
		q string
		e Expr
	}{
		"正常查询": {
			q: `test`,
			e: &MatchExpr{
				Value: `test`,
			},
		},
		"负数查询": {
			q: `-test`,
			e: &NotExpr{
				Expr: &MatchExpr{
					Value: `test`,
				},
			},
		},
		"负数查询多条件": {
			q: `-test AND good`,
			e: &AndExpr{
				Left: &NotExpr{
					Expr: &MatchExpr{
						Value: `test`,
					},
				},
				Right: &MatchExpr{
					Value: `good`,
				},
			},
		},
		"通配符匹配": {
			q: `qu?ck bro*`,
			e: &OrExpr{
				Left: &WildcardExpr{
					Value: "qu?ck",
				},
				Right: &WildcardExpr{
					Value: "bro*",
				},
			},
		},
		"无条件正则匹配": {
			q: `/joh?n(ath[oa]n)/`,
			e: &RegexpExpr{
				Value: "joh?n(ath[oa]n)",
			},
		},
		"正则匹配": {
			q: `name: /joh?n(ath[oa]n)/`,
			e: &RegexpExpr{
				Field: "name",
				Value: "joh?n(ath[oa]n)",
			},
		},
		"范围匹配，左闭右开": {
			q: `count:[1 TO 5}`,
			e: &NumberRangeExpr{
				Field:        "count",
				Start:        pointer("1"),
				End:          pointer("5"),
				IncludeStart: true,
				IncludeEnd:   false,
			},
		},
		"范围匹配": {
			q: `count:[1 TO 5]`,
			e: &NumberRangeExpr{
				Field:        "count",
				Start:        pointer("1"),
				End:          pointer("5"),
				IncludeStart: true,
				IncludeEnd:   true,
			},
		},
		"范围匹配（无下限） - 1": {
			q: `count:{* TO 10]`,
			e: &NumberRangeExpr{
				Field:        "count",
				Start:        pointer("*"),
				End:          pointer("10"),
				IncludeStart: false,
				IncludeEnd:   true,
			},
		},
		"范围匹配（无下限）": {
			q: `count:[* TO 10]`,
			e: &NumberRangeExpr{
				Field:        "count",
				Start:        pointer("*"),
				End:          pointer("10"),
				IncludeStart: true,
				IncludeEnd:   true,
			},
		},
		"范围匹配（无上限）": {
			q: `count:[10 TO *]`,
			e: &NumberRangeExpr{
				Field:        "count",
				Start:        pointer("10"),
				End:          pointer("*"),
				IncludeStart: true,
				IncludeEnd:   true,
			},
		},
		"范围匹配（无上限）- 1": {
			q: `count:[10 TO *}`,
			e: &NumberRangeExpr{
				Field:        "count",
				Start:        pointer("10"),
				End:          pointer("*"),
				IncludeStart: true,
				IncludeEnd:   false,
			},
		},
		"字段匹配": {
			q: `status:active`,
			e: &MatchExpr{
				Field: "status",
				Value: "active",
			},
		},
		"字段匹配 + 括号": {
			q: `status:(active)`,
			e: &MatchExpr{
				Field: "status",
				Value: "active",
			},
		},
		"多条件组合，括号调整优先级": {
			q: `author:"John Smith" AND (age:20 OR status:active)`,
			e: &AndExpr{
				Left: &MatchExpr{
					Field: "author",
					Value: "John Smith",
				},
				Right: &OrExpr{
					Left: &MatchExpr{
						Field: "age",
						Value: "20",
					},
					Right: &MatchExpr{
						Field: "status",
						Value: "active",
					},
				},
			},
		},
		"多条件组合，and 和 or 的优先级": {
			q: `(author:"John Smith" AND age:20) OR status:active`,
			e: &OrExpr{
				Left: &AndExpr{
					Left: &MatchExpr{
						Field: "author",
						Value: "John Smith",
					},
					Right: &MatchExpr{
						Field: "age",
						Value: "20",
					},
				},
				Right: &MatchExpr{
					Field: "status",
					Value: "active",
				},
			},
		},
		"嵌套逻辑表达式": {
			q: `a:1 AND (b:2 OR c:3)`,
			e: &AndExpr{
				Left: &MatchExpr{
					Field: "a",
					Value: "1",
				},
				Right: &OrExpr{
					Left: &MatchExpr{
						Field: "b",
						Value: "2",
					},
					Right: &MatchExpr{
						Field: "c",
						Value: "3",
					},
				},
			},
		},
		"嵌套逻辑表达式 - 2": {
			q: `a:1 OR b:2 OR (c:3 OR d:4)`,
			e: &OrExpr{
				Left: &MatchExpr{
					Field: "a",
					Value: "1",
				},
				Right: &OrExpr{
					Left: &MatchExpr{
						Field: "b",
						Value: "2",
					},
					Right: &OrExpr{
						Left: &MatchExpr{
							Field: "c",
							Value: "3",
						},
						Right: &MatchExpr{
							Field: "d",
							Value: "4",
						},
					},
				},
			},
		},
		"嵌套逻辑表达式 - 3": {
			q: `a:1 OR (b:2 OR c:3) OR d:4`,
			e: &OrExpr{
				Left: &MatchExpr{
					Field: "a",
					Value: "1",
				},
				Right: &OrExpr{
					Left: &OrExpr{
						Left: &MatchExpr{
							Field: "b",
							Value: "2",
						},
						Right: &MatchExpr{
							Field: "c",
							Value: "3",
						},
					},
					Right: &MatchExpr{
						Field: "d",
						Value: "4",
					},
				},
			},
		},
		"嵌套逻辑表达式 - 4": {
			q: `a:1 OR (b:2 OR c:3) AND d:4`,
			e: &OrExpr{
				Left: &MatchExpr{
					Field: "a",
					Value: "1",
				},
				Right: &AndExpr{
					Left: &OrExpr{
						Left: &MatchExpr{
							Field: "b",
							Value: "2",
						},
						Right: &MatchExpr{
							Field: "c",
							Value: "3",
						},
					},
					Right: &MatchExpr{
						Field: "d",
						Value: "4",
					},
				},
			},
		},
		"new-1": {
			q: `quick brown +fox -news`,
			e: &OrExpr{
				Left: &MatchExpr{
					Value: "quick",
				},
				Right: &OrExpr{
					Left: &MatchExpr{
						Value: "brown",
					},
					Right: &OrExpr{
						Left: &MatchExpr{
							Value: "fox",
						},
						Right: &NotExpr{
							Expr: &MatchExpr{
								Field: "",
								Value: "news",
							},
						},
					},
				},
			},
		},
		"模糊匹配": {
			q: `quick brown fox`,
			e: &OrExpr{
				Left: &MatchExpr{
					Value: "quick",
				},
				Right: &OrExpr{
					Left: &MatchExpr{
						Value: "brown",
					},
					Right: &MatchExpr{
						Value: "fox",
					},
				},
			},
		},
		"单个条件精确匹配": {
			q: `log: "ERROR MSG"`,
			e: &MatchExpr{
				Field: "log",
				Value: "ERROR MSG",
			},
		},
		"match and time range": {
			q: "message: test\\ value AND datetime: [\"2020-01-01T00:00:00\" TO \"2020-12-31T00:00:00\"]",
			e: &AndExpr{
				Left: &MatchExpr{
					Field: "message",
					Value: "test value",
				},
				Right: &TimeRangeExpr{
					Field:        "datetime",
					Start:        pointer("2020-01-01T00:00:00"),
					End:          pointer("2020-12-31T00:00:00"),
					IncludeStart: true,
					IncludeEnd:   true,
				},
			},
		},
		"mixed or / and": {
			q: "a: 1 OR (b: 2 and c: 4)",
			e: &OrExpr{
				Left: &MatchExpr{
					Field: "a",
					Value: "1",
				},
				Right: &AndExpr{
					Left: &MatchExpr{
						Field: "b",
						Value: "2",
					},
					Right: &MatchExpr{
						Field: "c",
						Value: "4",
					},
				},
			},
		},
		"start without tCOLON": {
			q: "a > 100",
			e: &NumberRangeExpr{
				Field: "a",
				Start: pointer("100"),
			},
		},
		"end without tCOLON": {
			q: "a < 100",
			e: &NumberRangeExpr{
				Field: "a",
				End:   pointer("100"),
			},
		},
		"start and eq without tCOLON": {
			q: "a >= 100",
			e: &NumberRangeExpr{
				Field:        "a",
				Start:        pointer("100"),
				IncludeStart: true,
			},
		},
		"end and eq without tCOLON": {
			q: "a <= 100",
			e: &NumberRangeExpr{
				Field:      "a",
				End:        pointer("100"),
				IncludeEnd: true,
			},
		},
		"start": {
			q: "a: >100",
			e: &NumberRangeExpr{
				Field: "a",
				Start: pointer("100"),
			},
		},
		"one word left star": {
			q: "*test",
			e: &WildcardExpr{
				Value: "*test",
			},
		},
		"one word right star": {
			q: "test*",
			e: &WildcardExpr{
				Value: "test*",
			},
		},
		"one word double star": {
			q: "*test*",
			e: &WildcardExpr{
				Value: "*test*",
			},
		},
		"one int double star": {
			q: "*123*",
			e: &WildcardExpr{
				Value: "*123*",
			},
		},
		"key value with star": {
			q: "events.attributes.message.detail: *66036*",
			e: &WildcardExpr{
				Field: "events.attributes.message.detail",
				Value: "*66036*",
			},
		},
		"value like regex": {
			q: `"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log" and level: "error" and "2_bklog.bkunify_query"`,
			e: &AndExpr{
				Left: &MatchExpr{
					Value: "/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log",
				},
				Right: &AndExpr{
					Left: &MatchExpr{
						Field: "level",
						Value: "error",
					},
					Right: &MatchExpr{
						Value: "2_bklog.bkunify_query",
					},
				},
			},
		},
		"双引号转义符号支持": {
			q: `log: "(reading \\\"remove\\\")"`,
			e: &MatchExpr{
				Field: "log",
				Value: `(reading \"remove\")`,
			},
		},
		"test": {
			q: `path: "/proz/logds/ds-5910974792526317*"`,
			e: &WildcardExpr{
				Field: "path",
				Value: "/proz/logds/ds-5910974792526317*",
			},
		},
		"test-1": {
			q: "\"32221112\" AND path: \"/data/home/user00/log/zonesvr*\"",
			e: &AndExpr{
				Left: &MatchExpr{
					Value: "32221112",
				},
				Right: &WildcardExpr{
					Field: "path",
					Value: "/data/home/user00/log/zonesvr*",
				},
			},
		},
		"test - Many Brack ": {
			q: `(loglevel: ("TRACE" OR "DEBUG" OR  "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")) AND "test111"`,
			e: &AndExpr{
				Left: &AndExpr{
					Left: &ConditionMatchExpr{
						Field: "loglevel",
						Value: &ConditionExpr{
							Values: [][]string{
								{"TRACE"},
								{"DEBUG"},
								{"INFO "},
								{"WARN "},
								{"ERROR"},
							},
						},
					},
					Right: &ConditionMatchExpr{
						Field: "log",
						Value: &ConditionExpr{
							Values: [][]string{
								{"friendsvr", "game_app", "testAnd"},
								{"friendsvr", "testOr", "testAnd"},
								{"test111"},
							},
						},
					},
				},
				Right: &MatchExpr{
					Value: "test111",
				},
			},
		},
		"test - many tPHRASE ": {
			q: `loglevel: ("TRACE" OR "DEBUG" OR  "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			e: &AndExpr{
				Left: &ConditionMatchExpr{
					Field: "loglevel",
					Value: &ConditionExpr{
						Values: [][]string{
							{"TRACE"},
							{"DEBUG"},
							{"INFO "},
							{"WARN "},
							{"ERROR"},
						},
					},
				},
				Right: &ConditionMatchExpr{
					Field: "log",
					Value: &ConditionExpr{
						Values: [][]string{
							{"friendsvr", "game_app", "testAnd"},
							{"friendsvr", "testOr", "testAnd"},
							{"test111"},
						},
					},
				},
			},
		},
		"test - Single Bracket And  ": {
			q: `loglevel: ("TRACE" AND "111" AND "DEBUG" AND "INFO" OR "SIMON" OR "222" AND "333" )`,
			e: &ConditionMatchExpr{
				Field: "loglevel",
				Value: &ConditionExpr{
					Values: [][]string{
						{"TRACE", "111", "DEBUG", "INFO"},
						{"SIMON"},
						{"222", "333"},
					},
				},
			},
		},
		"test - Self Bracket ": {
			q: `loglevel: ("TRACE" OR ("DEBUG") OR  ("INFO ") OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			e: &AndExpr{
				Left: &ConditionMatchExpr{
					Field: "loglevel",
					Value: &ConditionExpr{
						Values: [][]string{
							{"TRACE"},
							{"DEBUG"},
							{"INFO "},
							{"WARN "},
							{"ERROR"},
						},
					},
				},
				Right: &ConditionMatchExpr{
					Field: "log",
					Value: &ConditionExpr{
						Values: [][]string{
							{"friendsvr", "game_app", "testAnd"},
							{"friendsvr", "testOr", "testAnd"},
							{"test111"},
						},
					},
				},
			},
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			expr, err := Parse(c.q)
			if err != nil {
				t.Errorf("parse return error, %s", err)
				return
			}
			assert.Equal(t, c.e, expr)
		})
	}
}

func pointer(s string) *string {
	return &s
}
