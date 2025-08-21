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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring_parser"
)

// queryToJSON converts elastic.Query to JSON string for comparison
func queryToJSON(query elastic.Query) (string, error) {
	if query == nil {
		return "null", nil
	}

	// Get the source for the query
	src, err := query.Source()
	if err != nil {
		return "", err
	}

	// Marshal to JSON
	jsonBytes, err := json.Marshal(src)
	if err != nil {
		return "", err
	}

	return string(jsonBytes), nil
}

func TestParser(t *testing.T) {
	testCases := map[string]struct {
		q string
		e querystring_parser.Expr
	}{
		"正常查询": {
			q: `test`,
			e: &querystring_parser.MatchExpr{
				Value: `test`,
			},
		},
		"负数查询": {
			q: `-test`,
			e: &querystring_parser.NotExpr{
				Expr: &querystring_parser.MatchExpr{
					Value: `test`,
				},
			},
		},
		"负数查询多条件": {
			q: `-test AND good`,
			e: &querystring_parser.AndExpr{
				Left: &querystring_parser.NotExpr{
					Expr: &querystring_parser.MatchExpr{
						Value: `test`,
					},
				},
				Right: &querystring_parser.MatchExpr{
					Value: `good`,
				},
			},
		},
		"通配符匹配": {
			q: `qu?ck bro*`,
			e: &querystring_parser.OrExpr{
				Left: &querystring_parser.WildcardExpr{
					Value: "qu?ck",
				},
				Right: &querystring_parser.WildcardExpr{
					Value: "bro*",
				},
			},
		},
		"无条件正则匹配": {
			q: `/joh?n(ath[oa]n)/`,
			e: &querystring_parser.RegexpExpr{
				Value: "joh?n(ath[oa]n)",
			},
		},
		"正则匹配": {
			q: `name: /joh?n(ath[oa]n)/`,
			e: &querystring_parser.RegexpExpr{
				Field: "name",
				Value: "joh?n(ath[oa]n)",
			},
		},
		"范围匹配，左闭右开": {
			q: `count:[1 TO 5}`,
			e: &querystring_parser.NumberRangeExpr{
				Field:        "count",
				Start:        pointer("1"),
				End:          pointer("5"),
				IncludeStart: true,
				IncludeEnd:   false,
			},
		},
		"范围匹配": {
			q: `count:[1 TO 5]`,
			e: &querystring_parser.NumberRangeExpr{
				Field:        "count",
				Start:        pointer("1"),
				End:          pointer("5"),
				IncludeStart: true,
				IncludeEnd:   true,
			},
		},
		"范围匹配（无下限） - 1": {
			q: `count:{* TO 10]`,
			e: &querystring_parser.NumberRangeExpr{
				Field:        "count",
				Start:        pointer("*"),
				End:          pointer("10"),
				IncludeStart: false,
				IncludeEnd:   false,
			},
		},
		"范围匹配（无下限）": {
			q: `count:[* TO 10]`,
			e: &querystring_parser.NumberRangeExpr{
				Field:        "count",
				Start:        pointer("*"),
				End:          pointer("10"),
				IncludeStart: true,
				IncludeEnd:   true,
			},
		},
		"范围匹配（无上限）": {
			q: `count:[10 TO *]`,
			e: &querystring_parser.NumberRangeExpr{
				Field:        "count",
				Start:        pointer("10"),
				End:          pointer("*"),
				IncludeStart: true,
				IncludeEnd:   true,
			},
		},
		"范围匹配（无上限）- 1": {
			q: `count:[10 TO *}`,
			e: &querystring_parser.NumberRangeExpr{
				Field:        "count",
				Start:        pointer("10"),
				End:          pointer("*"),
				IncludeStart: true,
				IncludeEnd:   false,
			},
		},
		"字段匹配": {
			q: `status:active`,
			e: &querystring_parser.MatchExpr{
				Field: "status",
				Value: "active",
			},
		},
		"字段匹配 + 括号": {
			q: `status:(active)`,
			e: &querystring_parser.MatchExpr{
				Field: "status",
				Value: "active",
			},
		},
		"多条件组合，括号调整优先级": {
			q: `author:"John Smith" AND (age:20 OR status:active)`,
			e: &querystring_parser.AndExpr{
				Left: &querystring_parser.MatchExpr{
					Field: "author",
					Value: "John Smith",
				},
				Right: &querystring_parser.OrExpr{
					Left: &querystring_parser.MatchExpr{
						Field: "age",
						Value: "20",
					},
					Right: &querystring_parser.MatchExpr{
						Field: "status",
						Value: "active",
					},
				},
			},
		},
		"多条件组合，and 和 or 的优先级": {
			q: `(author:"John Smith" AND age:20) OR status:active`,
			e: &querystring_parser.OrExpr{
				Left: &querystring_parser.AndExpr{
					Left: &querystring_parser.MatchExpr{
						Field: "author",
						Value: "John Smith",
					},
					Right: &querystring_parser.MatchExpr{
						Field: "age",
						Value: "20",
					},
				},
				Right: &querystring_parser.MatchExpr{
					Field: "status",
					Value: "active",
				},
			},
		},
		"嵌套逻辑表达式": {
			q: `a:1 AND (b:2 OR c:3)`,
			e: &querystring_parser.AndExpr{
				Left: &querystring_parser.MatchExpr{
					Field: "a",
					Value: "1",
				},
				Right: &querystring_parser.OrExpr{
					Left: &querystring_parser.MatchExpr{
						Field: "b",
						Value: "2",
					},
					Right: &querystring_parser.MatchExpr{
						Field: "c",
						Value: "3",
					},
				},
			},
		},
		"嵌套逻辑表达式 - 2": {
			q: `a:1 OR b:2 OR (c:3 OR d:4)`,
			e: &querystring_parser.OrExpr{
				Left: &querystring_parser.OrExpr{
					Left: &querystring_parser.MatchExpr{
						Field: "a",
						Value: "1",
					},
					Right: &querystring_parser.MatchExpr{
						Field: "b",
						Value: "2",
					},
				},
				Right: &querystring_parser.OrExpr{
					Left: &querystring_parser.MatchExpr{
						Field: "c",
						Value: "3",
					},
					Right: &querystring_parser.MatchExpr{
						Field: "d",
						Value: "4",
					},
				},
			},
		},
		"嵌套逻辑表达式 - 3": {
			q: `a:1 OR (b:2 OR c:3) OR d:4`,
			e: &querystring_parser.OrExpr{
				Left: &querystring_parser.OrExpr{
					Left: &querystring_parser.MatchExpr{
						Field: "a",
						Value: "1",
					},
					Right: &querystring_parser.OrExpr{
						Left: &querystring_parser.MatchExpr{
							Field: "b",
							Value: "2",
						},
						Right: &querystring_parser.MatchExpr{
							Field: "c",
							Value: "3",
						},
					},
				},
				Right: &querystring_parser.MatchExpr{
					Field: "d",
					Value: "4",
				},
			},
		},
		"嵌套逻辑表达式 - 4": {
			q: `a:1 OR (b:2 OR c:3) AND d:4`,
			e: &querystring_parser.OrExpr{
				Left: &querystring_parser.MatchExpr{
					Field: "a",
					Value: "1",
				},
				Right: &querystring_parser.AndExpr{
					Left: &querystring_parser.OrExpr{
						Left: &querystring_parser.MatchExpr{
							Field: "b",
							Value: "2",
						},
						Right: &querystring_parser.MatchExpr{
							Field: "c",
							Value: "3",
						},
					},
					Right: &querystring_parser.MatchExpr{
						Field: "d",
						Value: "4",
					},
				},
			},
		},
		"new-1": {
			q: `quick brown +fox -news`,
			e: &querystring_parser.OrExpr{
				Left: &querystring_parser.OrExpr{
					Left: &querystring_parser.OrExpr{
						Left: &querystring_parser.MatchExpr{
							Value: "quick",
						},
						Right: &querystring_parser.MatchExpr{
							Value: "brown",
						},
					},
					Right: &querystring_parser.MatchExpr{
						Value: "fox",
					},
				},
				Right: &querystring_parser.NotExpr{
					Expr: &querystring_parser.MatchExpr{
						Field: "",
						Value: "news",
					},
				},
			},
		},
		"模糊匹配": {
			q: `quick brown fox`,
			e: &querystring_parser.OrExpr{
				Left: &querystring_parser.OrExpr{
					Left: &querystring_parser.MatchExpr{
						Value: "quick",
					},
					Right: &querystring_parser.MatchExpr{
						Value: "brown",
					},
				},
				Right: &querystring_parser.MatchExpr{
					Value: "fox",
				},
			},
		},
		"单个条件精确匹配": {
			q: `log: "ERROR MSG"`,
			e: &querystring_parser.MatchExpr{
				Field: "log",
				Value: "ERROR MSG",
			},
		},
		"match and time range": {
			q: "message: test\\ value AND datetime: [\"2020-01-01T00:00:00\" TO \"2020-12-31T00:00:00\"]",
			e: &querystring_parser.AndExpr{
				Left: &querystring_parser.MatchExpr{
					Field: "message",
					Value: "test value",
				},
				Right: &querystring_parser.TimeRangeExpr{
					Field:        "datetime",
					Start:        pointer("2020-01-01T00:00:00"),
					End:          pointer("2020-12-31T00:00:00"),
					IncludeStart: true,
					IncludeEnd:   true,
				},
			},
		},
		"mixed or / and": {
			q: "a:1 OR (b:2 AND c:4)",
			e: &querystring_parser.OrExpr{
				Left: &querystring_parser.MatchExpr{
					Field: "a",
					Value: "1",
				},
				Right: &querystring_parser.AndExpr{
					Left: &querystring_parser.MatchExpr{
						Field: "b",
						Value: "2",
					},
					Right: &querystring_parser.MatchExpr{
						Field: "c",
						Value: "4",
					},
				},
			},
		},
		// "start without tCOLON": {
		// 	q: "a > 100",
		// 	e: &querystring_parser.NumberRangeExpr{
		// 		Field: "a",
		// 		Start: pointer("100"),
		// 	},
		// },
		// "end without tCOLON": {
		// 	q: "a < 100",
		// 	e: &querystring_parser.NumberRangeExpr{
		// 		Field: "a",
		// 		End:   pointer("100"),
		// 	},
		// },
		// "start and eq without tCOLON": {
		// 	q: "a >= 100",
		// 	e: &querystring_parser.NumberRangeExpr{
		// 		Field:        "a",
		// 		Start:        pointer("100"),
		// 		IncludeStart: true,
		// 	},
		// },
		// "end and eq without tCOLON": {
		// 	q: "a <= 100",
		// 	e: &querystring_parser.NumberRangeExpr{
		// 		Field:      "a",
		// 		End:        pointer("100"),
		// 		IncludeEnd: true,
		// 	},
		// },
		// "start": {
		// 	q: "a:>100",
		// 	e: &querystring_parser.NumberRangeExpr{
		// 		Field: "a",
		// 		Start: pointer("100"),
		// 	},
		// },
		"one word left star": {
			q: "*test",
			e: &querystring_parser.WildcardExpr{
				Value: "*test",
			},
		},
		"one word right star": {
			q: "test*",
			e: &querystring_parser.WildcardExpr{
				Value: "test*",
			},
		},
		"one word double star": {
			q: "*test*",
			e: &querystring_parser.WildcardExpr{
				Value: "*test*",
			},
		},
		"one int double star": {
			q: "*123*",
			e: &querystring_parser.WildcardExpr{
				Value: "*123*",
			},
		},
		"key value with star": {
			q: "events.attributes.message.detail: *66036*",
			e: &querystring_parser.WildcardExpr{
				Field: "events.attributes.message.detail",
				Value: "*66036*",
			},
		},
		"value like regex": {
			q: `"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log" and level: "error" and "2_bklog.bkunify_query"`,
			e: &querystring_parser.OrExpr{
				Left: &querystring_parser.OrExpr{
					Left: &querystring_parser.MatchExpr{
						Value: "/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log",
					},
					Right: &querystring_parser.MatchExpr{
						Field: "level",
						Value: "error",
					},
				},
				Right: &querystring_parser.MatchExpr{
					Value: "2_bklog.bkunify_query",
				},
			},
		},
		"双引号转义符号支持": {
			q: `log: "(reading \\\"remove\\\")"`,
			e: &querystring_parser.MatchExpr{
				Field: "log",
				Value: `(reading \"remove\")`,
			},
		},
		"test": {
			q: `path: "/proz/logds/ds-5910974792526317*"`,
			e: &querystring_parser.WildcardExpr{
				Field: "path",
				Value: "/proz/logds/ds-5910974792526317*",
			},
		},
		"test-1": {
			q: "\"32221112\" AND path: \"/data/home/user00/log/zonesvr*\"",
			e: &querystring_parser.AndExpr{
				Left: &querystring_parser.MatchExpr{
					Value: "32221112",
				},
				Right: &querystring_parser.WildcardExpr{
					Field: "path",
					Value: "/data/home/user00/log/zonesvr*",
				},
			},
		},
		//Complex bracket expressions with ConditionMatchExpr are not yet supported
		"test - Many Brack ": {
			q: `(loglevel: ("TRACE" OR "DEBUG" OR  "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")) AND "test111"`,
			e: &querystring_parser.AndExpr{
				Left: &querystring_parser.AndExpr{
					Left: &querystring_parser.ConditionMatchExpr{
						Field: "loglevel",
						Value: &querystring_parser.ConditionExpr{
							Values: [][]string{
								{"TRACE"},
								{"DEBUG"},
								{"INFO "},
								{"WARN "},
								{"ERROR"},
							},
						},
					},
					Right: &querystring_parser.ConditionMatchExpr{
						Field: "log",
						Value: &querystring_parser.ConditionExpr{
							Values: [][]string{
								{"friendsvr", "game_app", "testAnd"},
								{"friendsvr", "testOr", "testAnd"},
								{"test111"},
							},
						},
					},
				},
				Right: &querystring_parser.MatchExpr{
					Value: "test111",
				},
			},
		},
		"test - many tPHRASE ": {
			q: `loglevel: ("TRACE" OR "DEBUG" OR  "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			e: &querystring_parser.AndExpr{
				Left: &querystring_parser.ConditionMatchExpr{
					Field: "loglevel",
					Value: &querystring_parser.ConditionExpr{
						Values: [][]string{
							{"TRACE"},
							{"DEBUG"},
							{"INFO "},
							{"WARN "},
							{"ERROR"},
						},
					},
				},
				Right: &querystring_parser.ConditionMatchExpr{
					Field: "log",
					Value: &querystring_parser.ConditionExpr{
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
			e: &querystring_parser.ConditionMatchExpr{
				Field: "loglevel",
				Value: &querystring_parser.ConditionExpr{
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
			e: &querystring_parser.AndExpr{
				Left: &querystring_parser.ConditionMatchExpr{
					Field: "loglevel",
					Value: &querystring_parser.ConditionExpr{
						Values: [][]string{
							{"TRACE"},
							{"DEBUG"},
							{"INFO "},
							{"WARN "},
							{"ERROR"},
						},
					},
				},
				Right: &querystring_parser.ConditionMatchExpr{
					Field: "log",
					Value: &querystring_parser.ConditionExpr{
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
			expr, err := ParseLuceneToSQL(t.Context(), c.q, nil)
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
