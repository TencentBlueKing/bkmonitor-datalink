// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may not- use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package lucene_parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLuceneParser(t *testing.T) {
	testCases := map[string]struct {
		q   string
		e   Expr
		es  string
		sql string
	}{
		// =================================================================
		// Test Suite: basic_syntax from antlr4_lucene_test_cases.json
		// =================================================================
		"simple_term": {
			q:   `term`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: `term`}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term"}}`,
			sql: "`log` MATCH_PHRASE 'term'",
		},
		"english_term": {
			q:   `hello`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: `hello`}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello"}}`,
			sql: "`log` MATCH_PHRASE 'hello'",
		},
		"chinese_term": {
			q:   `中国`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: `中国`}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"中国"}}`,
			sql: "`log` MATCH_PHRASE '中国'",
		},
		"accented_term": {
			q:   `café`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: `café`}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"café"}}`,
			sql: "`log` MATCH_PHRASE 'café'",
		},
		"basic_field_query": {
			q:   `status:value`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "value"}},
			es:  `{"term":{"status":"value"}}`,
			sql: "`status` = 'value'",
		},
		// 并不支持 _exists_ 语法糖,不存在于词法文件中
		//"field_query_exists": {
		//	q:   `_exists_:author`,
		//	e:   &OperatorExpr{Field: &StringExpr{Value: "_exists_"}, Op: OpMatch, Value: &StringExpr{Value: "author"}},
		//	es:  `{"exists":{"field":"author"}}`,
		//	sql: "`author` IS NOT NULL",
		//},
		"basic_phrase_query": {
			q:   `"hello world"`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: `hello world`}, IsQuoted: true},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"hello world\""}}`,
			sql: "`log` MATCH_PHRASE 'hello world'",
		},
		"field_phrase_query": {
			q:   `author:"phrase value"`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "author"}, Op: OpMatch, Value: &StringExpr{Value: "phrase value"}, IsQuoted: true},
			es:  `{"match_phrase":{"author":{"query":"phrase value"}}}`,
			sql: "`author` MATCH_PHRASE 'phrase value'",
		},
		"proximity_query": {
			q:   `"hello world"~5`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "hello world"}, IsQuoted: true, Slop: 5},
			es:  `{"match_phrase":{"log":{"query":"hello world","slop":5}}}`,
			sql: "`log` MATCH_PHRASE 'hello world'",
		},
		"boolean_AND": {
			q: `term1 AND term2`,
			e: &AndExpr{
				Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term1"}},
				Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term2"}},
			},
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "`log` MATCH_PHRASE 'term1' AND `log` MATCH_PHRASE 'term2'",
		},
		"boolean_OR": {
			q: `term1 OR term2`,
			e: &OrExpr{
				Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term1"}},
				Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term2"}},
			},
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'term1' OR `log` MATCH_PHRASE 'term2')",
		},
		"boolean_NOT": {
			q: `term1 NOT term2`,
			e: &OrExpr{
				Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term1"}},
				Right: &NotExpr{Expr: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term2"}}},
			},
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}}}]}}`,
			sql: "(`log` MATCH_PHRASE 'term1' OR NOT (`log` MATCH_PHRASE 'term2'))",
		},
		"boolean_required_prohibited": {
			q: `+required -prohibited`,
			e: &OrExpr{
				Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "required"}},
				Right: &NotExpr{Expr: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "prohibited"}}},
			},
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"required"}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"prohibited"}}}}]}}`,
			sql: "(`log` MATCH_PHRASE 'required' OR NOT (`log` MATCH_PHRASE 'prohibited'))",
		},
		"boolean_double_ampersand": {
			q: `term1 && term2`,
			e: &AndExpr{
				Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term1"}},
				Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term2"}},
			},
			es:  `{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "`log` MATCH_PHRASE 'term1' AND `log` MATCH_PHRASE 'term2'",
		},
		"boolean_double_pipe": {
			q: `term1 || term2`,
			e: &OrExpr{
				Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term1"}},
				Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term2"}},
			},
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'term1' OR `log` MATCH_PHRASE 'term2')",
		},
		"wildcard_suffix": {
			q:   `test*`,
			e:   &OperatorExpr{Op: OpWildcard, Value: &StringExpr{Value: "test*"}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"test*"}}`,
			sql: "`log` LIKE 'test%'",
		},
		"wildcard_prefix": {
			q:   `*test`,
			e:   &OperatorExpr{Op: OpWildcard, Value: &StringExpr{Value: "*test"}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"*test"}}`,
			sql: "`log` LIKE '%test'",
		},
		"wildcard_infix": {
			q:   `te*st`,
			e:   &OperatorExpr{Op: OpWildcard, Value: &StringExpr{Value: "te*st"}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"te*st"}}`,
			sql: "`log` LIKE 'te%st'",
		},
		"wildcard_single_char": {
			q:   `t?st`,
			e:   &OperatorExpr{Op: OpWildcard, Value: &StringExpr{Value: "t?st"}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"t?st"}}`,
			sql: "`log` LIKE '%t_st%'",
		},
		"wildcard_field": {
			q:   `path:test*`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "path"}, Op: OpWildcard, Value: &StringExpr{Value: "test*"}},
			es:  `{"wildcard":{"path":{"value":"test*"}}}`,
			sql: "`path` LIKE 'test%'",
		},
		"regex_basic": {
			q:   `/test.*/`,
			e:   &OperatorExpr{Op: OpRegex, Value: &StringExpr{Value: "test.*"}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"/test.*/"}}`,
			sql: "`log` REGEXP 'test.*'",
		},
		"regex_field": {
			q:   `log:/patt.*n/`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpRegex, Value: &StringExpr{Value: "patt.*n"}},
			es:  `{"regexp":{"log":{"value":"patt.*n"}}}`,
			sql: "`log` REGEXP 'patt.*n'",
		},
		"fuzzy_default": {
			q:   `test~`,
			e:   &OperatorExpr{Op: OpFuzzy, Value: &StringExpr{Value: "test"}, Fuzziness: "AUTO"},
			es:  `{"fuzzy":{"log":{"fuzziness":"AUTO","value":"test"}}}`,
			sql: "`log` MATCH_PHRASE 'test'",
		},
		"fuzzy_with_distance": {
			q:   `test~1`,
			e:   &OperatorExpr{Op: OpFuzzy, Value: &StringExpr{Value: "test"}, Fuzziness: "1"},
			es:  `{"fuzzy":{"log":{"fuzziness":"1","value":"test"}}}`,
			sql: "`log` MATCH_PHRASE 'test'",
		},
		"range_inclusive": {
			q:   `count:[1 TO 10]`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "count"}, Op: OpRange, Value: &RangeExpr{Start: &NumberExpr{Value: 1}, End: &NumberExpr{Value: 10}, IncludeStart: &BoolExpr{Value: true}, IncludeEnd: &BoolExpr{Value: true}}},
			es:  `{"range":{"count":{"from":1,"include_lower":true,"include_upper":true,"to":10}}}`,
			sql: "`count` >= 1 AND `count` <= 10",
		},
		"range_exclusive": {
			q:   `age:{18 TO 30}`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "age"}, Op: OpRange, Value: &RangeExpr{Start: &NumberExpr{Value: 18}, End: &NumberExpr{Value: 30}, IncludeStart: &BoolExpr{Value: false}, IncludeEnd: &BoolExpr{Value: false}}},
			es:  `{"range":{"age":{"from":18,"include_lower":false,"include_upper":false,"to":30}}}`,
			sql: "`age` > 18 AND `age` < 30",
		},
		"range_unbounded_lower": {
			q:   `count:[* TO 100]`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "count"}, Op: OpRange, Value: &RangeExpr{Start: &StringExpr{Value: "*"}, End: &NumberExpr{Value: 100}, IncludeStart: &BoolExpr{Value: true}, IncludeEnd: &BoolExpr{Value: true}}},
			es:  `{"range":{"count":{"from":null,"include_lower":true,"include_upper":true,"to":100}}}`,
			sql: "`count` <= 100",
		},
		"range_unbounded_upper": {
			q:   `count:[10 TO *]`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "count"}, Op: OpRange, Value: &RangeExpr{Start: &NumberExpr{Value: 10}, End: &StringExpr{Value: "*"}, IncludeStart: &BoolExpr{Value: true}, IncludeEnd: &BoolExpr{Value: true}}},
			es:  `{"range":{"count":{"from":10,"include_lower":true,"include_upper":true,"to":null}}}`,
			sql: "`count` >= 10",
		},
		"range_date": {
			q:   `datetime:[2021-01-01 TO 2021-12-31]`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "datetime"}, Op: OpRange, Value: &RangeExpr{Start: &StringExpr{Value: "2021-01-01"}, End: &StringExpr{Value: "2021-12-31"}, IncludeStart: &BoolExpr{Value: true}, IncludeEnd: &BoolExpr{Value: true}}},
			es:  `{"range":{"datetime":{"from":"2021-01-01","include_lower":true,"include_upper":true,"to":"2021-12-31"}}}`,
			sql: "`datetime` >= '2021-01-01' AND `datetime` <= '2021-12-31'",
		},
		"boost_integer": {
			q:   `term^2`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term"}, Boost: 2},
			es:  `{"query_string":{"analyze_wildcard":true,"boost":2,"fields":["*","__*"],"lenient":true,"query":"term"}}`,
			sql: "`log` MATCH_PHRASE 'term'",
		},
		"boost_float": {
			q:   `"phrase query"^3.5`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "phrase query"}, IsQuoted: true, Boost: 3.5},
			es:  `{"query_string":{"analyze_wildcard":true,"boost":3.5,"fields":["*","__*"],"lenient":true,"query":"\"phrase query\""}}`,
			sql: "`log` MATCH_PHRASE 'phrase query'",
		},
		"grouping_basic": {
			q: `(term1 OR term2)`,
			e: &GroupingExpr{Expr: &OrExpr{
				Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term1"}},
				Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term2"}},
			}},
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'term1' OR `log` MATCH_PHRASE 'term2')",
		},
		"grouping_field": {
			q: `author:(value1 OR value2)`,
			e: &GroupingExpr{
				Expr: &OrExpr{
					Left:  &OperatorExpr{Field: &StringExpr{Value: "author"}, Op: OpMatch, Value: &StringExpr{Value: "value1"}},
					Right: &OperatorExpr{Field: &StringExpr{Value: "author"}, Op: OpMatch, Value: &StringExpr{Value: "value2"}},
				},
			},
			es:  `{"bool":{"should":[{"match_phrase":{"author":{"query":"value1"}}},{"match_phrase":{"author":{"query":"value2"}}}]}}`,
			sql: "(`author` MATCH_PHRASE 'value1' OR `author` MATCH_PHRASE 'value2')",
		},
		"grouping_with_boost": {
			q: `(term1 AND term2)^2`,
			e: &GroupingExpr{
				Boost: 2,
				Expr: &AndExpr{
					Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term1"}},
					Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "term2"}},
				},
			},
			es:  `{"bool":{"boost":2,"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"term2"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'term1' AND `log` MATCH_PHRASE 'term2')",
		},

		// =================================================================
		// Test Suite: edge_cases from antlr4_lucene_test_cases.json
		// =================================================================
		"escape_colon": {
			q:   `hello\:world`, // 这里的':'不是一个用来分隔“字段名”和“值”的符号。表示“hello:world”是一个整体的搜索词
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: `hello:world`}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello:world"}}`,
			sql: "`log` MATCH_PHRASE 'hello:world'",
		},
		"escape_parentheses": {
			q:   `hello\(world\)`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: `hello(world)`}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello(world)"}}`,
			sql: "`log` MATCH_PHRASE 'hello(world)'",
		},
		"escape_star": {
			q:   `hello\*world`,
			e:   &OperatorExpr{Op: OpWildcard, Value: &StringExpr{Value: `hello*world`}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello*world"}}`,
			sql: "`log` LIKE 'hello%world'",
		},
		"whitespace_multiple_spaces": {
			q: `  hello  world  `,
			e: &OrExpr{
				Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: `hello`}},
				Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: `world`}},
			},
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"hello"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"world"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'hello' OR `log` MATCH_PHRASE 'world')",
		},
		"numeric_integer": {
			q:   `123`, // 默认字段log是text类型,需要用match_phrase
			e:   &OperatorExpr{Op: OpMatch, Value: &NumberExpr{Value: 123}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"123"}}`,
			sql: "`log` MATCH_PHRASE '123'",
		},
		"numeric_float": {
			q:   `12.34`,
			e:   &OperatorExpr{Op: OpMatch, Value: &NumberExpr{Value: 12.34}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"12.34"}}`,
			sql: "`log` MATCH_PHRASE '12.34'",
		},
		"numeric_negative": {
			q:   `-123`,
			e:   &NotExpr{Expr: &OperatorExpr{Op: OpMatch, Value: &NumberExpr{Value: 123}}},
			es:  `{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"123"}}}}`,
			sql: "NOT (`log` MATCH_PHRASE '123')",
		},
		"unicode_russian": {
			q:   `Москва`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "Москва"}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"Москва"}}`,
			sql: "`log` MATCH_PHRASE 'Москва'",
		},
		"unicode_japanese": {
			q:   `日本語`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "日本語"}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"日本語"}}`,
			sql: "`log` MATCH_PHRASE '日本語'",
		},
		// TODO: special_match_all_docs test temporarily commented out
		// "special_match_all_docs": {
		// 	q:   `*:*`,
		// 	e:   &OperatorExpr{Field: &StringExpr{Value: "*"}, Op: OpMatch, Value: &StringExpr{Value: "*"}},
		// 	es:  `{"match_all":{}}`,
		// 	sql: "1 = 1",
		// },
		"special_empty_phrase": {
			q:   `""`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: ""}, IsQuoted: true},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"\"\""}}`,
			sql: "`log` MATCH_PHRASE ''",
		},

		// =================================================================
		// Test Suite: complex_combinations from antlr4_lucene_test_cases.json
		// =================================================================
		"complex_nested_boolean": { // message是需要分词搜索的字段,用match_phrase; loglevel和status是精确值匹配的字段,用term
			q: `(loglevel:java OR loglevel:python) AND (message:tutorial OR message:guide) AND NOT status:deprecated`,
			e: &AndExpr{
				Left: &AndExpr{
					Left: &GroupingExpr{Expr: &OrExpr{
						Left:  &OperatorExpr{Field: &StringExpr{Value: "loglevel"}, Op: OpMatch, Value: &StringExpr{Value: "java"}},
						Right: &OperatorExpr{Field: &StringExpr{Value: "loglevel"}, Op: OpMatch, Value: &StringExpr{Value: "python"}},
					}},
					Right: &GroupingExpr{Expr: &OrExpr{
						Left:  &OperatorExpr{Field: &StringExpr{Value: "message"}, Op: OpMatch, Value: &StringExpr{Value: "tutorial"}},
						Right: &OperatorExpr{Field: &StringExpr{Value: "message"}, Op: OpMatch, Value: &StringExpr{Value: "guide"}},
					}},
				},
				Right: &NotExpr{Expr: &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "deprecated"}}},
			},
			es: `{"bool":{"must":[{"bool":{"should":[{"term":{"loglevel":"java"}},{"term":{"loglevel":"python"}}]}},{"bool":{"should":[{"match_phrase":{"message":{"query":"tutorial"}}},{"match_phrase":{"message":{"query":"guide"}}}]}},{"bool":{"must_not":{"term":{"status":"deprecated"}}}}]}}`,
			// 在doris下如果是text类型
			sql: "(`loglevel` = 'java' OR `loglevel` = 'python') AND (`message` MATCH_PHRASE 'tutorial' OR `message` MATCH_PHRASE 'guide') AND NOT (`status` = 'deprecated')",
		},
		"complex_mixed_operators": {
			q: `+required +(optional1 OR optional2) -excluded`,
			e: &OrExpr{
				Left: &OrExpr{
					Left: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "required"}},
					Right: &GroupingExpr{Expr: &OrExpr{
						Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "optional1"}},
						Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "optional2"}},
					}},
				},
				Right: &NotExpr{Expr: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "excluded"}}},
			},
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"required"}},{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"optional1"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"optional2"}}]}},{"bool":{"must_not":{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"excluded"}}}}]}}`,
			sql: "(`log` MATCH_PHRASE 'required' OR (`log` MATCH_PHRASE 'optional1' OR `log` MATCH_PHRASE 'optional2') OR NOT (`log` MATCH_PHRASE 'excluded'))",
		},
		"complex_scoring_nested_boost": {
			// artificial intelligence需要被识别为phrase
			q: `(author:"machine learning"^3 OR message:"artificial intelligence"^2)^0.5`,
			e: &GroupingExpr{
				Boost: 0.5,
				Expr: &OrExpr{
					Left:  &OperatorExpr{Field: &StringExpr{Value: "author"}, Op: OpMatch, Value: &StringExpr{Value: "machine learning"}, IsQuoted: true, Boost: 3},
					Right: &OperatorExpr{Field: &StringExpr{Value: "message"}, Op: OpMatch, Value: &StringExpr{Value: "artificial intelligence"}, IsQuoted: true, Boost: 2},
				},
			},
			// boost参数应该在ES查询结构中正确处理
			es:  `{"bool":{"boost":0.5,"should":[{"match_phrase":{"author":{"boost":3,"query":"machine learning"}}},{"match_phrase":{"message":{"boost":2,"query":"artificial intelligence"}}}]}}`,
			sql: "(`author` MATCH_PHRASE 'machine learning' OR `message` MATCH_PHRASE 'artificial intelligence')",
		},
		"complex_mixed_types": {
			q: `author:john~ AND count:[* TO 100] AND (status:urgent OR loglevel:high^2)`,
			e: &AndExpr{
				Left: &AndExpr{
					Left:  &OperatorExpr{Field: &StringExpr{Value: "author"}, Op: OpFuzzy, Value: &StringExpr{Value: "john"}, Fuzziness: "AUTO"},
					Right: &OperatorExpr{Field: &StringExpr{Value: "count"}, Op: OpRange, Value: &RangeExpr{Start: &StringExpr{Value: "*"}, End: &NumberExpr{Value: 100}, IncludeStart: &BoolExpr{Value: true}, IncludeEnd: &BoolExpr{Value: true}}},
				},
				Right: &GroupingExpr{Expr: &OrExpr{
					Left:  &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "urgent"}},
					Right: &OperatorExpr{Field: &StringExpr{Value: "loglevel"}, Op: OpMatch, Value: &StringExpr{Value: "high"}, Boost: 2},
				}},
			},
			es:  `{"bool":{"must":[{"fuzzy":{"author":{"fuzziness":"AUTO","value":"john"}}},{"range":{"count":{"from":null,"include_lower":true,"include_upper":true,"to":100}}},{"bool":{"should":[{"term":{"status":"urgent"}},{"term":{"loglevel":{"boost":2,"value":"high"}}}]}}]}}`,
			sql: "`author` MATCH_PHRASE 'john' AND `count` <= 100 AND (`status` = 'urgent' OR `loglevel` = 'high')",
		},

		// =================================================================
		// Test Suite: lucene_extracted from antlr4_lucene_test_cases.json
		// =================================================================
		"lucene_extracted_simple_term_foo": {
			q:   `foo`,
			e:   &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "foo"}},
			es:  `{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"foo"}}`,
			sql: "`log` MATCH_PHRASE 'foo'",
		},
		"lucene_extracted_boolean_plus": {
			q: `+one +two`,
			e: &OrExpr{
				Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "one"}},
				Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "two"}},
			},
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"one"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"two"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'one' OR `log` MATCH_PHRASE 'two')",
		},
		"lucene_extracted_boost_fuzzy": {
			q: `one~0.8 two^2`,
			e: &OrExpr{
				Left:  &OperatorExpr{Op: OpFuzzy, Value: &StringExpr{Value: "one"}, Fuzziness: "0.8"},
				Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "two"}, Boost: 2},
			},
			es:  `{"bool":{"should":[{"fuzzy":{"log":{"fuzziness":"0.8","value":"one"}}},{"query_string":{"analyze_wildcard":true,"boost":2,"fields":["*","__*"],"lenient":true,"query":"two"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'one' OR `log` MATCH_PHRASE 'two')",
		},
		"lucene_extracted_wildcard_multi": {
			q: `one* two*`,
			e: &OrExpr{
				Left:  &OperatorExpr{Op: OpWildcard, Value: &StringExpr{Value: "one*"}},
				Right: &OperatorExpr{Op: OpWildcard, Value: &StringExpr{Value: "two*"}},
			},
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"one*"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"two*"}}]}}`,
			sql: "(`log` LIKE 'one%' OR `log` LIKE 'two%')",
		},
		"lucene_extracted_boolean_precedence": {
			q: `c OR (a AND b)`,
			e: &OrExpr{
				Left: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "c"}},
				Right: &GroupingExpr{Expr: &AndExpr{
					Left:  &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "a"}},
					Right: &OperatorExpr{Op: OpMatch, Value: &StringExpr{Value: "b"}},
				}},
			},
			es:  `{"bool":{"should":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"c"}},{"bool":{"must":[{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"a"}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"b"}}]}}]}}`,
			sql: "(`log` MATCH_PHRASE 'c' OR (`log` MATCH_PHRASE 'a' AND `log` MATCH_PHRASE 'b'))",
		},
		"lucene_extracted_field_numeric": {
			q:   `log:1`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &NumberExpr{Value: 1}},
			es:  `{"match_phrase":{"log":{"query":"1"}}}`,
			sql: "`log` MATCH_PHRASE '1'",
		},
		"lucene_extracted_range_int": {
			q:   `age:[1 TO 3]`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "age"}, Op: OpRange, Value: &RangeExpr{Start: &NumberExpr{Value: 1}, End: &NumberExpr{Value: 3}, IncludeStart: &BoolExpr{Value: true}, IncludeEnd: &BoolExpr{Value: true}}},
			es:  `{"range":{"age":{"from":1,"include_lower":true,"include_upper":true,"to":3}}}`,
			sql: "`age` >= 1 AND `age` <= 3",
		},
		"lucene_extracted_range_float": {
			q:   `price:[1.5 TO 3.6]`,
			e:   &OperatorExpr{Field: &StringExpr{Value: "price"}, Op: OpRange, Value: &RangeExpr{Start: &NumberExpr{Value: 1.5}, End: &NumberExpr{Value: 3.6}, IncludeStart: &BoolExpr{Value: true}, IncludeEnd: &BoolExpr{Value: true}}},
			es:  `{"range":{"price":{"from":1.5,"include_lower":true,"include_upper":true,"to":3.6}}}`,
			sql: "`price` >= 1.5 AND `price` <= 3.6",
		},

		// =================================================================
		// Test Suite: eof_operator_support - 测试末尾操作符支持
		// =================================================================
		"eof_operator_and_basic": {
			q: `log:error AND`,
			// 预期：应该将末尾的AND忽略，只保留log:error部分
			e:   &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &StringExpr{Value: "error"}},
			es:  `{"match_phrase":{"log":{"query":"error"}}}`,
			sql: "`log` MATCH_PHRASE 'error'",
		},
		"eof_operator_or_basic": {
			q: `status:active OR`,
			// 预期：应该将末尾的OR忽略，只保留status:active部分
			e:   &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "active"}},
			es:  `{"term":{"status":"active"}}`,
			sql: "`status` = 'active'",
		},
		"eof_operator_and_complex": {
			q: `log:error and status:active AND`,
			// 预期：应该将末尾的AND忽略，保留前面的正常表达式
			e: &AndExpr{
				Left:  &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &StringExpr{Value: "error"}},
				Right: &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "active"}},
			},
			es:  `{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` = 'active'",
		},

		// =================================================================
		// Test Suite: case_insensitive_operators - 测试大小写不敏感操作符
		// =================================================================
		"case_insensitive_and_lowercase": {
			q: `log:error and status:active`,
			e: &AndExpr{
				Left:  &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &StringExpr{Value: "error"}},
				Right: &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "active"}},
			},
			es:  `{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` = 'active'",
		},
		"case_insensitive_and_mixed": {
			q: `log:error And status:active`,
			e: &AndExpr{
				Left:  &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &StringExpr{Value: "error"}},
				Right: &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "active"}},
			},
			es:  `{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` = 'active'",
		},
		"case_insensitive_and_variations": {
			q: `log:error aNd status:active anD level:info`,
			e: &AndExpr{
				Left: &AndExpr{
					Left:  &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &StringExpr{Value: "error"}},
					Right: &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "active"}},
				},
				Right: &OperatorExpr{Field: &StringExpr{Value: "level"}, Op: OpMatch, Value: &StringExpr{Value: "info"}},
			},
			es:  `{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}},{"term":{"level":"info"}}]}}`,
			sql: "`log` MATCH_PHRASE 'error' AND `status` = 'active' AND `level` = 'info'",
		},
		"case_insensitive_or_lowercase": {
			q: `log:error or status:active`,
			e: &OrExpr{
				Left:  &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &StringExpr{Value: "error"}},
				Right: &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "active"}},
			},
			es:  `{"bool":{"should":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'error' OR `status` = 'active')",
		},
		"case_insensitive_or_mixed": {
			q: `log:error Or status:active oR level:info`,
			e: &OrExpr{
				Left: &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &StringExpr{Value: "error"}},
				Right: &OrExpr{
					Left:  &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "active"}},
					Right: &OperatorExpr{Field: &StringExpr{Value: "level"}, Op: OpMatch, Value: &StringExpr{Value: "info"}},
				},
			},
			es:  `{"bool":{"should":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}},{"term":{"level":"info"}}]}}`,
			sql: "(`log` MATCH_PHRASE 'error' OR (`status` = 'active' OR `level` = 'info'))",
		},
		"case_insensitive_not_lowercase": {
			q: `log:error not status:active`,
			e: &OrExpr{
				Left:  &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &StringExpr{Value: "error"}},
				Right: &NotExpr{Expr: &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "active"}}},
			},
			es:  `{"bool":{"should":[{"match_phrase":{"log":{"query":"error"}}},{"bool":{"must_not":{"term":{"status":"active"}}}}]}}`,
			sql: "(`log` MATCH_PHRASE 'error' OR NOT (`status` = 'active'))",
		},
		"case_insensitive_not_mixed": {
			q: `log:error Not status:active`,
			e: &OrExpr{
				Left:  &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &StringExpr{Value: "error"}},
				Right: &NotExpr{Expr: &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "active"}}},
			},
			es:  `{"bool":{"should":[{"match_phrase":{"log":{"query":"error"}}},{"bool":{"must_not":{"term":{"status":"active"}}}}]}}`,
			sql: "(`log` MATCH_PHRASE 'error' OR NOT (`status` = 'active'))",
		},
		"case_insensitive_mixed_complex": {
			q: `(log:error AND status:active) or (level:warn Not type:system)`,
			e: &OrExpr{
				Left: &GroupingExpr{Expr: &AndExpr{
					Left:  &OperatorExpr{Field: &StringExpr{Value: "log"}, Op: OpMatch, Value: &StringExpr{Value: "error"}},
					Right: &OperatorExpr{Field: &StringExpr{Value: "status"}, Op: OpMatch, Value: &StringExpr{Value: "active"}},
				}},
				Right: &GroupingExpr{Expr: &OrExpr{
					Left:  &OperatorExpr{Field: &StringExpr{Value: "level"}, Op: OpMatch, Value: &StringExpr{Value: "warn"}},
					Right: &NotExpr{Expr: &OperatorExpr{Field: &StringExpr{Value: "type"}, Op: OpMatch, Value: &StringExpr{Value: "system"}}},
				}},
			},
			es:  `{"bool":{"should":[{"bool":{"must":[{"match_phrase":{"log":{"query":"error"}}},{"term":{"status":"active"}}]}},{"bool":{"should":[{"term":{"level":"warn"}},{"bool":{"must_not":{"term":{"type":"system"}}}}]}}]}}`,
			sql: "((`log` MATCH_PHRASE 'error' AND `status` = 'active') OR (`level` = 'warn' OR NOT (`type` = 'system')))",
		},
	}

	parser := NewParser(
		WithMapping(loadTestMapping()),
	)
	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			rt, err := parser.Parse(c.q, false)
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

func loadTestMapping() map[string]FieldOption {
	return map[string]FieldOption{
		"age":      {Type: FieldTypeLong},
		"count":    {Type: FieldTypeLong},
		"price":    {Type: FieldTypeFloat},
		"a":        {Type: FieldTypeLong},
		"b":        {Type: FieldTypeLong},
		"c":        {Type: FieldTypeLong},
		"d":        {Type: FieldTypeLong},
		"status":   {Type: FieldTypeKeyword},
		"level":    {Type: FieldTypeKeyword},
		"loglevel": {Type: FieldTypeKeyword},
		"author":   {Type: FieldTypeText},
		"message":  {Type: FieldTypeText},
		"log":      {Type: FieldTypeText},
		"path":     {Type: FieldTypeKeyword},
		"datetime": {Type: FieldTypeDate},
		"type":     {Type: FieldTypeKeyword},
	}
}
