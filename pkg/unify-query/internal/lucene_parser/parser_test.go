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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestParseWithVisitor(t *testing.T) {
	testCases := []struct {
		name string
		q    string
		sql  string
		err  error
	}{
		{
			name: "basic term",
			q:    `test`,
			sql:  "test",
		},
		{
			name: "quoted term",
			q:    `"test term"`,
			sql:  `"test term"`,
		},
		{
			name: "field query",
			q:    `field:value`,
			sql:  "field:value",
		},
		{
			name: "field with quoted value",
			q:    `field:"value with space"`,
			sql:  `field:"value with space"`,
		},
		{
			name: "AND operator",
			q:    `term1 AND term2`,
			sql:  "term1 AND term2",
		},
		{
			name: "OR operator",
			q:    `term1 OR term2`,
			sql:  "term1 OR term2",
		},
		{
			name: "multiple AND",
			q:    `a AND b AND c`,
			sql:  "a AND b AND c",
		},
		{
			name: "multiple OR",
			q:    `a OR b OR c`,
			sql:  "a OR b OR c",
		},
		{
			name: "AND OR combination",
			q:    `a AND b OR c`,
			sql:  "a AND b OR c",
		},
		{
			name: "grouping with OR and AND",
			q:    `(a OR b) AND c`,
			sql:  "(a OR b) AND c",
		},
		{
			name: "plus modifier",
			q:    `+term`,
			sql:  "+term",
		},
		{
			name: "minus modifier",
			q:    `-term`,
			sql:  "-term",
		},
		{
			name: "NOT modifier",
			q:    `NOT term`,
			sql:  "NOT term",
		},
		{
			name: "field with modifier",
			q:    `field:-term`,
			sql:  "field:-term",
		},
		{
			name: "inclusive range",
			q:    `[start TO end]`,
			sql:  "[start TO end]",
		},
		{
			name: "exclusive range",
			q:    `{start TO end}`,
			sql:  "{start TO end}",
		},
		{
			name: "field with inclusive range",
			q:    `field:[start TO end]`,
			sql:  "field:[start TO end]",
		},
		{
			name: "field with exclusive range",
			q:    `field:{start TO end}`,
			sql:  "field:{start TO end}",
		},
		{
			name: "numeric range",
			q:    `field:[1 TO 100]`,
			sql:  "field:[1 TO 100]",
		},
		{
			name: "quoted range values",
			q:    `field:["start value" TO "end value"]`,
			sql:  `field:["start value" TO "end value"]`,
		},
		{
			name: "greater than",
			q:    `field>10`,
			sql:  "field>10",
		},
		{
			name: "greater than equal",
			q:    `field>=10`,
			sql:  "field>=10",
		},
		{
			name: "less than",
			q:    `field<10`,
			sql:  "field<10",
		},
		{
			name: "less than equal",
			q:    `field<=10`,
			sql:  "field<=10",
		},
		{
			name: "fuzzy query",
			q:    `term~`,
			sql:  "term~",
		},
		{
			name: "fuzzy with distance",
			q:    `term~2`,
			sql:  "term~2",
		},
		{
			name: "field with fuzzy",
			q:    `field:term~2`,
			sql:  "field:term~2",
		},
		{
			name: "boost query",
			q:    `term^2`,
			sql:  "term^2",
		},
		{
			name: "boost with decimal",
			q:    `term^0.5`,
			sql:  "term^0.5",
		},
		{
			name: "field with boost",
			q:    `field:term^2`,
			sql:  "field:term^2",
		},
		{
			name: "quoted term with boost",
			q:    `"quoted term"^2`,
			sql:  `"quoted term"^2`,
		},
		{
			name: "grouping with boost",
			q:    `(a OR b)^3`,
			sql:  "(a OR b)^3",
		},
		{
			name: "regex query",
			q:    `/pattern.*/`,
			sql:  "/pattern.*/",
		},
		{
			name: "field with regex",
			q:    `field:/pattern.*/`,
			sql:  "field:/pattern.*/",
		},
		{
			name: "regex with boost",
			q:    `/pattern.*/^2`,
			sql:  "/pattern.*/^2",
		},
		{
			name: "complex nested grouping",
			q:    `(a OR b) AND (c OR d)`,
			sql:  "(a OR b) AND (c OR d)",
		},
		{
			name: "field grouping with multiple conditions",
			q:    `field:(a OR b OR c)`,
			sql:  "field:(a OR b OR c)",
		},
		{
			name: "mixed modifiers and logic",
			q:    `+a -b c OR NOT d`,
			sql:  "+a -b c OR NOT d",
		},
		{
			name: "complex real world query",
			q:    `loglevel:("ERROR" OR "WARN") AND timestamp:[2023-01-01 TO 2023-12-31] AND message:"timeout"~3 AND NOT source:/.*test.*/`,
			sql:  `loglevel:("ERROR" OR "WARN") AND timestamp:[2023-01-01 TO 2023-12-31] AND message:"timeout"~3 AND NOT source:/.*test.*/`,
		},
		{
			name: "empty quoted string",
			q:    `""`,
			sql:  `""`,
		},
		{
			name: "single character term",
			q:    `a`,
			sql:  "a",
		},
		{
			name: "special characters in quoted string",
			q:    `"test:colon and space"`,
			sql:  `"test:colon and space"`,
		},
		{
			name: "original test - 1",
			q:    `"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log" and level: "error" and "2_bklog.bkunify_query"`,
			sql:  `"/var/host/data/bcs/lib/docker/containers/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5/e1fe718565fe0a073f024c243e00344d09eb0206ba55ccd0c281fc5f4ffd62a5-json.log" and level: "error" and "2_bklog.bkunify_query"`,
		},
		{
			name: "original test - 2",
			q:    `loglevel: ("TRACE" OR "DEBUG" OR  "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
			sql:  `loglevel: ("TRACE" OR "DEBUG" OR  "INFO " OR "WARN " OR "ERROR") AND log: ("friendsvr" AND ("game_app" OR "testOr") AND "testAnd" OR "test111")`,
		},
		{
			name: "original test - 4",
			q:    `log:test AND message:"bm"`,
			sql:  `log:test AND message:"bm"`,
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
				DimensionTransform: func(s string) (string, bool) {
					if _, ok := fieldAlias[s]; ok {
						return fieldAlias[s], true
					}
					return s, false
				},
			}
			sql, err := ParseLuceneWithVisitor(ctx, c.q, opt)
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
