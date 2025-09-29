// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser_old

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildBooleanExpression(t *testing.T) {
	createTerm := func(value string) *OperatorExpr {
		return &OperatorExpr{
			Op:    OpMatch,
			Value: &StringExpr{Value: value},
		}
	}

	createFieldTerm := func(field, value string) *OperatorExpr {
		return &OperatorExpr{
			Field: &StringExpr{Value: field},
			Op:    OpMatch,
			Value: &StringExpr{Value: value},
		}
	}

	testCases := []struct {
		name           string
		mustClauses    []Expr
		mustNotClauses []Expr
		shouldClauses  []Expr
		expectedExpr   Expr
		description    string
	}{
		{
			name:          "must_and_should_single",
			mustClauses:   []Expr{createTerm("required")},
			shouldClauses: []Expr{createTerm("optional")},
			description:   "Must and single should clause - should use distributive law: (optional AND required) OR required",
			expectedExpr: &OrExpr{
				Left: &AndExpr{
					Left:  createTerm("optional"),
					Right: createTerm("required"),
				},
				Right: createTerm("required"),
			},
		},
		{
			name:          "must_and_should_multiple",
			mustClauses:   []Expr{createTerm("required")},
			shouldClauses: []Expr{createTerm("option1"), createTerm("option2")},
			description:   "Must and multiple should clauses - should distribute must across each should: (option1 AND required) OR (option2 AND required) OR required",
			expectedExpr: &OrExpr{
				Left: &OrExpr{
					Left: &AndExpr{
						Left:  createTerm("option1"),
						Right: createTerm("required"),
					},
					Right: &AndExpr{
						Left:  createTerm("option2"),
						Right: createTerm("required"),
					},
				},
				Right: createTerm("required"),
			},
		},
		{
			name:           "complex_combination",
			mustClauses:    []Expr{createFieldTerm("status", "active"), createFieldTerm("type", "user")},
			mustNotClauses: []Expr{createFieldTerm("role", "admin")},
			shouldClauses:  []Expr{createFieldTerm("priority", "high"), createFieldTerm("urgent", "true")},
			description:    "Complex combination of all clause types - tests full distributive logic",
			expectedExpr: &AndExpr{
				Left: &OrExpr{
					Left: &OrExpr{
						Left: &AndExpr{
							Left: createFieldTerm("priority", "high"),
							Right: &AndExpr{
								Left:  createFieldTerm("status", "active"),
								Right: createFieldTerm("type", "user"),
							},
						},
						Right: &AndExpr{
							Left: createFieldTerm("urgent", "true"),
							Right: &AndExpr{
								Left:  createFieldTerm("status", "active"),
								Right: createFieldTerm("type", "user"),
							},
						},
					},
					Right: &AndExpr{
						Left:  createFieldTerm("status", "active"),
						Right: createFieldTerm("type", "user"),
					},
				},
				Right: &NotExpr{Expr: createFieldTerm("role", "admin")},
			},
		},
		{
			name:          "should_only",
			shouldClauses: []Expr{createTerm("maybe1"), createTerm("maybe2")},
			description:   "Only should clauses - should chain with OR",
			expectedExpr: &OrExpr{
				Left:  createTerm("maybe1"),
				Right: createTerm("maybe2"),
			},
		},
		{
			name:           "must_not_only",
			mustNotClauses: []Expr{createTerm("bad1"), createTerm("bad2")},
			description:    "Only must_not clauses - should chain with AND of NotExpr",
			expectedExpr: &AndExpr{
				Left:  &NotExpr{Expr: createTerm("bad1")},
				Right: &NotExpr{Expr: createTerm("bad2")},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := buildBooleanExpression(tc.mustClauses, tc.mustNotClauses, tc.shouldClauses)
			assert.Equal(t, tc.expectedExpr, result, tc.description)
		})
	}
}
