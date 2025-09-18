// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Code generated from LuceneParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // LuceneParser
import "github.com/antlr4-go/antlr/v4"

// A complete Visitor for a parse tree produced by LuceneParser.
type LuceneParserVisitor interface {
	antlr.ParseTreeVisitor

	// Visit a parse tree produced by LuceneParser#topLevelQuery.
	VisitTopLevelQuery(ctx *TopLevelQueryContext) interface{}

	// Visit a parse tree produced by LuceneParser#query.
	VisitQuery(ctx *QueryContext) interface{}

	// Visit a parse tree produced by LuceneParser#disjQuery.
	VisitDisjQuery(ctx *DisjQueryContext) interface{}

	// Visit a parse tree produced by LuceneParser#conjQuery.
	VisitConjQuery(ctx *ConjQueryContext) interface{}

	// Visit a parse tree produced by LuceneParser#modClause.
	VisitModClause(ctx *ModClauseContext) interface{}

	// Visit a parse tree produced by LuceneParser#modifier.
	VisitModifier(ctx *ModifierContext) interface{}

	// Visit a parse tree produced by LuceneParser#clause.
	VisitClause(ctx *ClauseContext) interface{}

	// Visit a parse tree produced by LuceneParser#fieldRangeExpr.
	VisitFieldRangeExpr(ctx *FieldRangeExprContext) interface{}

	// Visit a parse tree produced by LuceneParser#term.
	VisitTerm(ctx *TermContext) interface{}

	// Visit a parse tree produced by LuceneParser#groupingExpr.
	VisitGroupingExpr(ctx *GroupingExprContext) interface{}

	// Visit a parse tree produced by LuceneParser#fieldName.
	VisitFieldName(ctx *FieldNameContext) interface{}

	// Visit a parse tree produced by LuceneParser#termRangeExpr.
	VisitTermRangeExpr(ctx *TermRangeExprContext) interface{}

	// Visit a parse tree produced by LuceneParser#quotedTerm.
	VisitQuotedTerm(ctx *QuotedTermContext) interface{}

	// Visit a parse tree produced by LuceneParser#fuzzy.
	VisitFuzzy(ctx *FuzzyContext) interface{}
}
