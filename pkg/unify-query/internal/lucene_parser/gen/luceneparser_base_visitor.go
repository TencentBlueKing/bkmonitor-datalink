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

type BaseLuceneParserVisitor struct {
	*antlr.BaseParseTreeVisitor
}

func (v *BaseLuceneParserVisitor) VisitTopLevelQuery(ctx *TopLevelQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitQuery(ctx *QueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitDisjQuery(ctx *DisjQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitConjQuery(ctx *ConjQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitModClause(ctx *ModClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitModifier(ctx *ModifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitClause(ctx *ClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitFieldRangeExpr(ctx *FieldRangeExprContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitTerm(ctx *TermContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitGroupingExpr(ctx *GroupingExprContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitFieldName(ctx *FieldNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitTermRangeExpr(ctx *TermRangeExprContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitQuotedTerm(ctx *QuotedTermContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitFuzzy(ctx *FuzzyContext) interface{} {
	return v.VisitChildren(ctx)
}
